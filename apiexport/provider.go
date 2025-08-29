/*
Copyright 2025 The KCP Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package apiexport

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	kcpcache "github.com/kcp-dev/apimachinery/v2/pkg/cache"
	apisv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"

	mcpcache "github.com/kcp-dev/multicluster-provider/internal/cache"
	mcrecorder "github.com/kcp-dev/multicluster-provider/internal/events/recorder"
)

var _ multicluster.Provider = &Provider{}

// Provider is a [sigs.k8s.io/multicluster-runtime/pkg/multicluster.Provider] that represents each [logical cluster]
// (in the kcp sense) exposed via a APIExport virtual workspace as a cluster in the [sigs.k8s.io/multicluster-runtime] sense.
//
// [logical cluster]: https://docs.kcp.io/kcp/latest/concepts/terminology/#logical-cluster
type Provider struct {
	config *rest.Config
	scheme *runtime.Scheme
	cache  mcpcache.WildcardCache
	object client.Object

	log logr.Logger

	lock      sync.RWMutex
	clusters  map[logicalcluster.Name]cluster.Cluster
	cancelFns map[logicalcluster.Name]context.CancelFunc

	recorderProvider *mcrecorder.Provider
}

// Options are the options for creating a new instance of the apiexport provider.
type Options struct {
	// Scheme is the scheme to use for the provider. If this is nil, it defaults
	// to the client-go scheme.
	Scheme *runtime.Scheme

	// WildcardCache is the wildcard cache to use for the provider. If this is
	// nil, a new wildcard cache will be created for the given rest.Config.
	WildcardCache mcpcache.WildcardCache

	// ObjectToWatch is the object type that the provider watches via a /clusters/*
	// wildcard endpoint to extract information about logical clusters joining and
	// leaving the "fleet" of (logical) clusters in kcp. If this is nil, it defaults
	// to [apisv1alpha1.APIBinding]. This might be useful when using this provider
	// against custom virtual workspaces that are not the APIExport one but share
	// the same endpoint semantics.
	ObjectToWatch client.Object

	// makeBroadcaster allows deferring the creation of the broadcaster to
	// avoid leaking goroutines if we never call Start on this manager.  It also
	// returns whether or not this is an "owned" broadcaster, and as such should be
	// stopped with the manager.
	makeBroadcaster mcrecorder.EventBroadcasterProducer
}

// New creates a new kcp virtual workspace provider. The provided [rest.Config]
// must point to a virtual workspace apiserver base path, i.e. up to but without
// the '/clusters/*' suffix. This information can be extracted from the APIExport
// status (deprecated) or an APIExportEndpointSlice status.
func New(cfg *rest.Config, options Options) (*Provider, error) {
	// Do the defaulting controller-runtime would do for those fields we need.
	if options.Scheme == nil {
		options.Scheme = scheme.Scheme
	}
	if options.WildcardCache == nil {
		var err error
		options.WildcardCache, err = mcpcache.NewWildcardCache(cfg, cache.Options{
			Scheme: options.Scheme,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create wildcard cache: %w", err)
		}
	}
	if options.ObjectToWatch == nil {
		options.ObjectToWatch = &apisv1alpha1.APIBinding{}
	}

	if options.makeBroadcaster == nil {
		options.makeBroadcaster = func() (record.EventBroadcaster, bool) {
			return record.NewBroadcaster(), true
		}
	}

	eventClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client for events: %w", err)
	}

	recorderProvider, err := mcrecorder.NewProvider(eventClient, options.Scheme, logr.Discard(), options.makeBroadcaster)
	if err != nil {
		return nil, err
	}

	return &Provider{
		config: cfg,
		scheme: options.Scheme,
		cache:  options.WildcardCache,
		object: options.ObjectToWatch,

		log: log.Log.WithName("kcp-apiexport-cluster-provider"),

		clusters:  map[logicalcluster.Name]cluster.Cluster{},
		cancelFns: map[logicalcluster.Name]context.CancelFunc{},

		recorderProvider: recorderProvider,
	}, nil
}

// Run starts the provider and blocks.
func (p *Provider) Run(ctx context.Context, mgr mcmanager.Manager) error {
	g, ctx := errgroup.WithContext(ctx)

	// Watch logical clusters and engage them as clusters in multicluster-runtime.
	inf, err := p.cache.GetInformer(ctx, p.object, cache.BlockUntilSynced(false))
	if err != nil {
		return fmt.Errorf("failed to get %T informer: %w", p.object, err)
	}
	shInf, _, _, err := p.cache.GetSharedInformer(p.object)
	if err != nil {
		return fmt.Errorf("failed to get shared informer: %w", err)
	}
	if _, err := inf.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			cobj, ok := obj.(client.Object)
			if !ok {
				klog.Errorf("unexpected object type %T", obj)
				return
			}
			clusterName := logicalcluster.From(cobj)

			// fast path: cluster exists already, there is nothing to do.
			p.lock.RLock()
			if _, ok := p.clusters[clusterName]; ok {
				p.lock.RUnlock()
				return
			}
			p.lock.RUnlock()

			// slow path: take write lock to add a new cluster (unless it appeared in the meantime).
			p.lock.Lock()
			if _, ok := p.clusters[clusterName]; ok {
				p.lock.Unlock()
				return
			}

			// create new scoped cluster.
			clusterCtx, cancel := context.WithCancel(ctx)
			cl, err := mcpcache.NewScopedCluster(p.config, clusterName, p.cache, p.scheme, p.recorderProvider)
			if err != nil {
				p.log.Error(err, "failed to create cluster", "cluster", clusterName)
				cancel()
				p.lock.Unlock()
				return
			}
			p.clusters[clusterName] = cl
			p.cancelFns[clusterName] = cancel
			p.lock.Unlock()

			p.log.Info("engaging cluster", "cluster", clusterName)
			if err := mgr.Engage(clusterCtx, clusterName.String(), cl); err != nil {
				p.log.Error(err, "failed to engage cluster", "cluster", clusterName)
				p.lock.Lock()
				cancel()
				if p.clusters[clusterName] == cl {
					delete(p.clusters, clusterName)
					delete(p.cancelFns, clusterName)
				}
				p.lock.Unlock()
			}
		},
		DeleteFunc: func(obj any) {
			cobj, ok := obj.(client.Object)
			if !ok {
				tombstone, ok := obj.(toolscache.DeletedFinalStateUnknown)
				if !ok {
					klog.Errorf("Couldn't get object from tombstone %#v", obj)
					return
				}
				cobj, ok = tombstone.Obj.(client.Object)
				if !ok {
					klog.Errorf("Tombstone contained object that is not expected %#v", obj)
					return
				}
			}

			clusterName := logicalcluster.From(cobj)

			// check if there is no object left in the index.
			keys, err := shInf.GetIndexer().IndexKeys(kcpcache.ClusterIndexName, clusterName.String())
			if err != nil {
				p.log.Error(err, "failed to get index keys", "cluster", clusterName)
				return
			}
			if len(keys) == 0 {
				p.lock.Lock()

				// shut down individual event broadaster
				if err := p.recorderProvider.StopBroadcaster(clusterName.String()); err != nil {
					p.lock.Unlock()
					p.log.Error(err, "failed to stop event broadcaster", "cluster", clusterName)
					return
				}

				cancel, ok := p.cancelFns[clusterName]
				if ok {
					p.log.Info("disengaging cluster", "cluster", clusterName)
					cancel()
					delete(p.cancelFns, clusterName)
					delete(p.clusters, clusterName)
				}
				p.lock.Unlock()
			}
		},
	}); err != nil {
		return fmt.Errorf("failed to add EventHandler: %w", err)
	}

	g.Go(func() error { return p.cache.Start(ctx) })
	g.Go(func() error {
		// wait for context stop and try to shut down event broadcasters
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			p.recorderProvider.Stop(shutdownCtx)
		default:
		}
		return nil
	})

	syncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if _, err := p.cache.GetInformer(syncCtx, p.object, cache.BlockUntilSynced(true)); err != nil {
		return fmt.Errorf("failed to sync %T informer: %w", p.object, err)
	}

	return g.Wait()
}

// Get returns a [cluster.Cluster] by logical cluster name. Be aware that workspace paths do not work.
func (p *Provider) Get(_ context.Context, name string) (cluster.Cluster, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if cl, ok := p.clusters[logicalcluster.Name(name)]; ok {
		return cl, nil
	}

	return nil, multicluster.ErrClusterNotFound
}

// GetWildcard returns the wildcard cache.
func (p *Provider) GetWildcard() cache.Cache {
	return p.cache
}

// IndexField indexes the given object by the given field on all engaged clusters, current and future.
func (p *Provider) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	return p.cache.IndexField(ctx, obj, field, extractValue)
}
