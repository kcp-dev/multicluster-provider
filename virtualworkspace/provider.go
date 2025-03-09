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

package virtualworkspace

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	kcpcache "github.com/kcp-dev/apimachinery/v2/pkg/cache"
	"github.com/kcp-dev/logicalcluster/v3"
	"golang.org/x/sync/errgroup"

	mcmanager "github.com/multicluster-runtime/multicluster-runtime/pkg/manager"
	"github.com/multicluster-runtime/multicluster-runtime/pkg/multicluster"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var _ multicluster.Provider = &Provider{}

// Provider is a cluster provider that represents each logical cluster in the
// kcp sense as a cluster in the multicluster-runtime sense.
type Provider struct {
	config *rest.Config
	scheme *runtime.Scheme
	cache  WildcardCache
	object client.Object

	log logr.Logger

	lock      sync.RWMutex
	clusters  map[logicalcluster.Name]cluster.Cluster
	cancelFns map[logicalcluster.Name]context.CancelFunc
}

// Options are the options for creating a new kcp virtual workspace provider.
type Options struct {
	// Scheme is the scheme to use for the provider. It defaults to the
	// client-go scheme.
	Scheme *runtime.Scheme

	// WildcardCache is the wildcard cache to use for the provider. If this is
	// nil, a new wildcard cache will be created for the given rest.Config.
	WildcardCache WildcardCache
}

// New creates a new kcp virtual workspace provider. The provided rest.Config
// must point to a virtual workspace apiserver base path, i.e. up to but without
// the "/clusters/*" suffix.
func New(cfg *rest.Config, obj client.Object, options Options) (*Provider, error) {
	// Do the defaulting controller-runtime would do for those fields we need.
	if options.Scheme == nil {
		options.Scheme = scheme.Scheme
	}
	if options.WildcardCache == nil {
		var err error
		options.WildcardCache, err = NewWildcardCache(cfg, cache.Options{
			Scheme: options.Scheme,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create wildcard cache: %w", err)
		}
	}

	return &Provider{
		config: cfg,
		scheme: options.Scheme,
		cache:  options.WildcardCache,
		object: obj,

		log: log.Log.WithName("kcp-virtualworkspace-cluster-provider"),

		clusters:  map[logicalcluster.Name]cluster.Cluster{},
		cancelFns: map[logicalcluster.Name]context.CancelFunc{},
	}, nil
}

// Run starts the provider and blocks.
func (p *Provider) Run(ctx context.Context, mgr mcmanager.Manager) error {
	g, ctx := errgroup.WithContext(ctx)

	// Watch logical clusters and engage them as clusters in multicluster-runtime.
	inf, err := p.cache.GetInformer(ctx, p.object, cache.BlockUntilSynced(false))
	if err != nil {
		return fmt.Errorf("failed to get logical cluster informer: %w", err)
	}
	shInf, _, _, err := p.cache.getSharedInformer(p.object)
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
			cl, err := newScopedCluster(p.config, clusterName, p.cache, p.scheme)
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

	syncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !p.cache.WaitForCacheSync(syncCtx) {
		return fmt.Errorf("failed to sync wildcard cache")
	}

	return g.Wait()
}

// Get returns a cluster by name.
func (p *Provider) Get(_ context.Context, name string) (cluster.Cluster, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if cl, ok := p.clusters[logicalcluster.Name(name)]; ok {
		return cl, nil
	}

	return nil, fmt.Errorf("cluster %q not found", name)
}

// GetWildcard returns the wildcard cache.
func (p *Provider) GetWildcard() cache.Cache {
	return p.cache
}

// IndexField indexes the given field for the given object.
func (p *Provider) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	return p.cache.IndexField(ctx, obj, "cluster/"+field, func(obj client.Object) []string {
		keys := extractValue(obj)

		clusterName := logicalcluster.From(obj)

		ret := make([]string, 0, len(keys))
		for _, key := range keys {
			ret = append(ret, fmt.Sprintf("%s|%s", clusterName, key))
		}
		return ret
	})
}
