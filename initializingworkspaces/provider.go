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

package initializingworkspaces

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	kcpcorev1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"

	mcpcache "github.com/kcp-dev/multicluster-provider/internal/cache"
)

var _ multicluster.Provider = &Provider{}

// Provider is a [sigs.k8s.io/multicluster-runtime/pkg/multicluster.Provider] that represents each [logical cluster]
// (in the kcp sense) having specific initializer and exposed via initializingworkspaces virtual workspace endpoint as a cluster in the [sigs.k8s.io/multicluster-runtime] sense.
//
// [logical cluster]: https://docs.kcp.io/kcp/latest/concepts/terminology/#logical-cluster
type Provider struct {
	config          *rest.Config
	scheme          *runtime.Scheme
	cache           cache.Cache
	wildcardCache   mcpcache.WildcardCache
	object          client.Object
	initializerName string

	log logr.Logger

	lock      sync.RWMutex
	Clusters  map[logicalcluster.Name]cluster.Cluster
	cancelFns map[logicalcluster.Name]context.CancelFunc
}

// Options are the options for creating a new instance of the initializing workspaces provider.
type Options struct {
	// Scheme is the scheme to use for the provider. If this is nil, it defaults
	// to the client-go scheme.
	Scheme *runtime.Scheme
	// WildcardCache is the wildcard cache to use for the provider. If this is
	// nil, a new wildcard cache will be created for the given rest.Config.
	WildcardCache mcpcache.WildcardCache
	// ObjectToWatch is the object type that the provider watches. If this is nil,
	// it defaults to LogicalCluster.
	ObjectToWatch client.Object

	// InitializerName is the name of the initializer to watch for in LogicalCluster.Status.Initializers
	InitializerName string
}

// New creates a new KCP initializing workspaces provider.
func New(cfg *rest.Config, options Options) (*Provider, error) {
	if options.Scheme == nil {
		options.Scheme = scheme.Scheme
		if err := kcpcorev1alpha1.AddToScheme(options.Scheme); err != nil {
			return nil, fmt.Errorf("failed to add kcp core scheme: %w", err)
		}
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
		options.ObjectToWatch = &kcpcorev1alpha1.LogicalCluster{}
	}

	if options.InitializerName == "" {
		return nil, fmt.Errorf("initializer name cannot be empty")
	}

	return &Provider{
		config:          cfg,
		scheme:          options.Scheme,
		wildcardCache:   options.WildcardCache,
		object:          options.ObjectToWatch,
		initializerName: options.InitializerName,

		log: log.Log.WithName("kcp-initializing-workspaces-provider"),

		Clusters:  map[logicalcluster.Name]cluster.Cluster{},
		cancelFns: map[logicalcluster.Name]context.CancelFunc{},
	}, nil
}

// Run starts the provider and blocks.
func (p *Provider) Run(ctx context.Context, mgr mcmanager.Manager) error {
	g, ctx := errgroup.WithContext(ctx)
	inf, err := p.wildcardCache.GetInformer(ctx, p.object, cache.BlockUntilSynced(true))
	if err != nil {
		return fmt.Errorf("failed to get %T informer: %w", p.object, err)
	}

	if _, err := inf.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			log.Log.Info("LogicalCluster added", "object", obj.(client.Object).GetUID())
			p.handleLogicalClusterEvent(ctx, obj, mgr)
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
			// check if there is no object left in the index
			if lc, ok := cobj.(*kcpcorev1alpha1.LogicalCluster); ok {
				clusterName := logicalcluster.From(lc)
				p.lock.Lock()
				cancel, ok := p.cancelFns[clusterName]
				if ok {
					p.log.Info("disengaging non-initializing workspace", "cluster", clusterName)
					cancel()
					delete(p.cancelFns, clusterName)
					delete(p.Clusters, clusterName)
				}
				p.lock.Unlock()
			} else {
				klog.Errorf("unexpected object type %T, expected LogicalCluster", cobj)
			}
		},
	}); err != nil {
		return fmt.Errorf("failed to add EventHandler: %w", err)
	}

	g.Go(func() error {
		return p.wildcardCache.Start(ctx)
	})

	syncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if _, err := p.wildcardCache.GetInformer(syncCtx, p.object, cache.BlockUntilSynced(true)); err != nil {
		return fmt.Errorf("failed to sync %T informer: %w", p.object, err)
	}

	return g.Wait()
}

// handleLogicalClusterEvent processes LogicalCluster events and engages initializing workspaces
func (p *Provider) handleLogicalClusterEvent(ctx context.Context, obj any, mgr mcmanager.Manager) {
	cobj, ok := obj.(client.Object)
	if !ok {
		klog.Errorf("unexpected object type %T", obj)
		return
	}

	lc, ok := cobj.(*kcpcorev1alpha1.LogicalCluster)
	if !ok {
		klog.Errorf("unexpected object type %T, expected LogicalCluster", cobj)
		return
	}

	// Check if our initializer is in the initializers list
	hasInitializer := slices.Contains(lc.Status.Initializers, kcpcorev1alpha1.LogicalClusterInitializer(p.initializerName))
	clusterName := logicalcluster.From(cobj)
	// If our initializer is not present, we need to disengage the cluster if it exists
	if !hasInitializer {
		p.lock.Lock()
		cancel, ok := p.cancelFns[clusterName]
		if ok {
			p.log.Info("disengaging non-initializing workspace", "cluster", clusterName)
			cancel()
			delete(p.cancelFns, clusterName)
			delete(p.Clusters, clusterName)
		}
		p.lock.Unlock()
		return
	}

	p.log.Info("LogicalCluster added", "object", clusterName)
	// fast path: cluster exists already, there is nothing to do.
	p.lock.RLock()
	if _, ok := p.Clusters[clusterName]; ok {
		p.lock.RUnlock()
		return
	}
	p.lock.RUnlock()

	// slow path: take write lock to add a new cluster (unless it appeared in the meantime).
	p.lock.Lock()
	if _, ok := p.Clusters[clusterName]; ok {
		p.lock.Unlock()
		return
	}

	// create new specific cluster with correct host endpoint for fetching specific logical cluster.
	ctx, cancel := context.WithCancel(ctx)
	cfg := rest.CopyConfig(p.config)
	host := cfg.Host
	host = strings.TrimSuffix(host, "/clusters/*")
	cfg.Host = fmt.Sprintf("%s/clusters/%s", host, clusterName)

	cl, err := mcpcache.NewScopedCluster(cfg, clusterName, p.wildcardCache, p.scheme)
	if err != nil {
		p.log.Error(err, "failed to create cluster", "cluster", clusterName)
		cancel()
		p.lock.Unlock()
		return
	}
	p.Clusters[clusterName] = cl
	p.cancelFns[clusterName] = cancel
	p.lock.Unlock()

	_, syncCancel := context.WithTimeout(ctx, 10*time.Second)
	defer syncCancel()

	p.log.Info("engaging cluster", "cluster", clusterName)
	if err := mgr.Engage(ctx, clusterName.String(), cl); err != nil {
		p.log.Error(err, "failed to engage cluster", "cluster", clusterName)
		p.lock.Lock()
		cancel()
		if p.Clusters[clusterName] == cl {
			delete(p.Clusters, clusterName)
			delete(p.cancelFns, clusterName)
		}
		p.lock.Unlock()
	}
	p.log.Info("engaged and registered cluster", "cluster", clusterName)
}

// Get returns a [cluster.Cluster] by logical cluster name.
func (p *Provider) Get(_ context.Context, name string) (cluster.Cluster, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if cl, ok := p.Clusters[logicalcluster.Name(name)]; ok {
		return cl, nil
	}

	return nil, multicluster.ErrClusterNotFound
}

// IndexField indexes the given object by the given field on all engaged clusters, current and future.
func (p *Provider) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	return p.cache.IndexField(ctx, obj, field, extractValue)
}
