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

	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	kcpcorev1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"

	mcpcache "github.com/kcp-dev/multicluster-provider/internal/cache"
)

var _ multicluster.Provider = &Provider{}

// Provider is a [sigs.k8s.io/multicluster-runtime/pkg/multicluster.Provider] that represents each [logical cluster]
// (in the kcp sense) having a specific initializer and exposed via the initializingworkspaces virtual workspace endpoint as a cluster in the [sigs.k8s.io/multicluster-runtime] sense.
//
// [logical cluster]: https://docs.kcp.io/kcp/latest/concepts/terminology/#logical-cluster
type Provider struct {
	config          *rest.Config
	scheme          *runtime.Scheme
	wildcardCache   mcpcache.WildcardCache
	initializerName string

	log logr.Logger

	lock      sync.RWMutex
	aware     multicluster.Aware
	clusters  map[logicalcluster.Name]cluster.Cluster
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

	// InitializerName is the name of the initializer to watch for in LogicalCluster.Status.Initializers
	InitializerName string
}

// New creates a new KCP initializing workspaces provider.
func New(cfg *rest.Config, options Options) (*Provider, error) {
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

	if options.InitializerName == "" {
		return nil, fmt.Errorf("initializer name cannot be empty")
	}

	return &Provider{
		config:          cfg,
		scheme:          options.Scheme,
		wildcardCache:   options.WildcardCache,
		initializerName: options.InitializerName,
		log:             log.Log.WithName("kcp-initializing-workspaces-provider"),

		clusters:  map[logicalcluster.Name]cluster.Cluster{},
		cancelFns: map[logicalcluster.Name]context.CancelFunc{},
	}, nil
}

// Start starts the provider and blocks.
func (p *Provider) Start(ctx context.Context, aware multicluster.Aware) error {
	p.aware = aware

	g, ctx := errgroup.WithContext(ctx)
	inf, err := p.wildcardCache.GetInformer(ctx, &kcpcorev1alpha1.LogicalCluster{}, cache.BlockUntilSynced(true))
	if err != nil {
		return fmt.Errorf("failed to get informer: %w", err)
	}

	if _, err := inf.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			p.handleLogicalClusterEvent(ctx, obj, p.aware)
		},
		UpdateFunc: func(_, newObj any) {
			p.handleLogicalClusterEvent(ctx, newObj, p.aware)
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
					delete(p.clusters, clusterName)
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
		err := p.wildcardCache.Start(ctx)
		if err != nil {
			return err
		}
		return nil
	})

	syncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if _, err := p.wildcardCache.GetInformer(syncCtx, &kcpcorev1alpha1.LogicalCluster{}, cache.BlockUntilSynced(true)); err != nil {
		return fmt.Errorf("failed to sync informer: %w", err)
	}

	return g.Wait()
}

// handleLogicalClusterEvent processes LogicalCluster events and engages initializing workspaces
func (p *Provider) handleLogicalClusterEvent(ctx context.Context, obj any, aware multicluster.Aware) {
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
		defer p.lock.Unlock()
		cancel, ok := p.cancelFns[clusterName]
		if ok {
			p.log.Info("disengaging non-initializing workspace", "cluster", clusterName)
			cancel()
			delete(p.cancelFns, clusterName)
			delete(p.clusters, clusterName)
		}
		return
	}

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

	p.log.Info("LogicalCluster added", "object", clusterName)
	// create new specific cluster with correct host endpoint for fetching specific logical cluster.
	clusterCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cl, err := mcpcache.NewScopedInitializingCluster(p.config, clusterName, p.wildcardCache, p.scheme)
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
	if err := aware.Engage(clusterCtx, clusterName.String(), cl); err != nil {
		p.log.Error(err, "failed to engage cluster", "cluster", clusterName)
		p.lock.Lock()
		cancel()
		if p.clusters[clusterName] == cl {
			delete(p.clusters, clusterName)
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
	if cl, ok := p.clusters[logicalcluster.Name(name)]; ok {
		return cl, nil
	}

	return nil, multicluster.ErrClusterNotFound
}

// IndexField indexes the given object by the given field on all engaged clusters, current and future.
func (p *Provider) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	return p.wildcardCache.IndexField(ctx, obj, field, extractValue)
}
