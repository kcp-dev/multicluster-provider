/*
Copyright 2025 The kcp Authors.

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

package provider

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"sigs.k8s.io/multicluster-runtime/pkg/clusters"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	kcpcache "github.com/kcp-dev/apimachinery/v2/pkg/cache"
	"github.com/kcp-dev/logicalcluster/v3"
	apisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"

	mcpcache "github.com/kcp-dev/multicluster-provider/pkg/cache"
	mcrecorder "github.com/kcp-dev/multicluster-provider/pkg/events/recorder"
	"github.com/kcp-dev/multicluster-provider/pkg/handlers"
)

// NewClusterFunc allows customizing the concrete cluster implementation used for
// every engaged cluster.
type NewClusterFunc func(cfg *rest.Config, clusterName logicalcluster.Name, wildcardCA mcpcache.WildcardCache, scheme *runtime.Scheme, recorderProvider mcrecorder.EventRecorderGetter) (*mcpcache.ScopedCluster, error)

// Options are the options for a provider.
type Options struct {
	// Scheme is the scheme to use for the provider.
	// If this is nil it defaults to the client-go scheme.
	Scheme *runtime.Scheme

	// EndpointSliceObject is the object type for the endpoint slice,
	// that records virtual workspace URLs of different shards.
	// If this is nil it defaults to [apisv1alpha2.APIExportEndpointSlice].
	EndpointSliceObject client.Object

	// ExtractURLsFromEndpointSlice is used to extract the URLs from the EndpointSliceObject.
	// If this is nil it defaults to [DefaultExtractURLsFromEndpointSlice].
	ExtractURLsFromEndpointSlice func(obj client.Object) ([]string, error)

	// ObjectToWatch is the object type the provider watches inside of
	// the virtual workspaces to observe logical clusters joining and
	// leaving the virtual workspaces.
	// If this is nil it defaults to [apisv1alpha2.APIBinding].
	ObjectToWatch client.Object

	// Log is the logger used to write any logs.
	Log *logr.Logger

	// NewCluster allows to customize the cluster instance that is being created for
	// each engaged cluster. If this is not set, it defaults to a ScopedCache that
	// uses the wildcard endpoint for its cache.
	NewCluster NewClusterFunc

	// AddFilter is called to filter objects to engage.
	// If false is returned the object is not engaged.
	AddFilter func(obj client.Object) (bool, error)

	// UpdateFilter is called to filter objects to engage.
	// If false is returned the object is not engaged.
	UpdateFilter func(obj client.Object) (bool, error)

	// Handlers are lifecycle handlers for logical clusters managed by this provider represented
	// by apibinding object.
	Handlers handlers.Handlers
}

func (opts *Options) defaults() {
	if opts.Scheme == nil {
		opts.Scheme = scheme.Scheme
	}

	if opts.EndpointSliceObject == nil {
		opts.EndpointSliceObject = &apisv1alpha2.APIExportEndpointSlice{}
	}

	if opts.ExtractURLsFromEndpointSlice == nil {
		opts.ExtractURLsFromEndpointSlice = DefaultExtractURLsFromEndpointSlice
	}

	if opts.ObjectToWatch == nil {
		opts.ObjectToWatch = &apisv1alpha2.APIBinding{}
	}

	if opts.Log == nil {
		logger := log.Log.WithName("kcp-provider")
		opts.Log = &logger
	}

	if opts.NewCluster == nil {
		opts.NewCluster = mcpcache.NewScopedCluster
	}
}

var _ multicluster.Provider = &Provider{}

// Provider is a [sigs.k8s.io/multicluster-runtime/pkg/multicluster.Provider] that
// yields each [logical cluster] (in the kcp sense) exposed via a virtual workspace
// as a cluster in the [sigs.k8s.io/multicluster-runtime] sense.
//
// [logical cluster]: https://docs.kcp.io/kcp/latest/concepts/terminology/#logical-cluster
type Provider struct {
	opts Options
	// config is the config pointing to the control plane where the endpoint slice resides.
	config *rest.Config

	// Clusters keeps track of all engaged clusters.
	Clusters clusters.Clusters[cluster.Cluster]

	// cache for the control plane where the endpoint slice resides
	cache cache.Cache

	// recorderManager manages per-cluster event recorder providers, creating
	// cluster-scoped broadcasters on engagement and stopping them on disengagement.
	recorderManager *mcrecorder.Manager

	// watchedEndpoints keeps track of which endpoints in the endpoint
	// slice are already being watched.
	watchedEndpoints map[string]*watchedEndpoint

	// watchEndpointFunc starts watching an endpoint.
	// Defaults to p.watchEndpoint; override for testing.
	watchEndpointFunc func(ctx context.Context, url string, aware multicluster.Aware) (*watchedEndpoint, error)

	// aggregateCache provides an aggregated view across all shard caches.
	aggregateCache *mcpcache.AggregateCache
}

// NewProvider returns a Provider.
// The config must point at the control plane containing the endpoint slice object.
func NewProvider(cfg *rest.Config, endpointSliceName string, opts Options) (*Provider, error) {
	opts.defaults()

	p := new(Provider)
	p.opts = opts
	p.config = cfg

	p.Clusters = clusters.New[cluster.Cluster]()

	c, err := cache.New(p.config, cache.Options{
		Scheme: p.opts.Scheme,
		ByObject: map[client.Object]cache.ByObject{
			p.opts.EndpointSliceObject: {
				Field: fields.SelectorFromSet(fields.Set{"metadata.name": endpointSliceName}),
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error building cache for %T %q: %w", p.opts.EndpointSliceObject, endpointSliceName, err)
	}
	p.cache = c

	p.recorderManager = mcrecorder.NewManager(p.opts.Scheme, p.opts.Log.WithName("recorder-manager"))
	p.watchedEndpoints = map[string]*watchedEndpoint{}
	p.watchEndpointFunc = p.watchEndpoint
	p.aggregateCache = mcpcache.NewAggregateCache()

	return p, nil
}

// Get returns the cluster with the given name as a cluster.Cluster.
func (p *Provider) Get(ctx context.Context, clusterName multicluster.ClusterName) (cluster.Cluster, error) {
	return p.Clusters.Get(ctx, clusterName)
}

// Lister returns a cache.Lister that lists objects across all shards.
func (p *Provider) Lister() mcpcache.Lister {
	return p.aggregateCache
}

// IndexField adds an indexer to the clusters managed by this provider.
func (p *Provider) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	return p.Clusters.IndexField(ctx, obj, field, extractValue)
}

// Start starts the provider, watches for VW endpoints and
func (p *Provider) Start(ctx context.Context, aware multicluster.Aware) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer func() {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		p.recorderManager.Stop(ctx)
	}()

	informer, err := p.cache.GetInformer(ctx, p.opts.EndpointSliceObject, cache.BlockUntilSynced(false))
	if err != nil {
		return err
	}

	handler, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			t := obj.(client.Object)
			p.opts.Log.Info("new endpointslice object", "object", t)
			p.endpointSliceUpdate(ctx, aware, t)
		},
		UpdateFunc: func(oldObj any, newObj any) {
			t := newObj.(client.Object)
			p.opts.Log.Info("updated endpointslice object", "object", t)
			p.endpointSliceUpdate(ctx, aware, t)
		},
		DeleteFunc: func(obj any) {
			p.opts.Log.Info("deleted endpointslice object", "object", obj)
			p.stopAllWatchedEndpoints()
		},
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := informer.RemoveEventHandler(handler); err != nil {
			p.opts.Log.Error(err, "failed to remove event handler")
		}
	}()

	p.opts.Log.Info("starting provider")
	return p.cache.Start(ctx)
}

func (p *Provider) endpointSliceUpdate(ctx context.Context, aware multicluster.Aware, obj client.Object) {
	urls, err := p.opts.ExtractURLsFromEndpointSlice(obj)
	if err != nil {
		p.opts.Log.Error(err, "error getting virtual workspace URLs from endpointslice object", "obj", obj, "name", obj.GetName())
		return
	}

	for _, url := range urls {
		if _, ok := p.watchedEndpoints[url]; ok {
			// endpoint is already handled
			continue
		}

		we, err := p.watchEndpointFunc(ctx, url, aware)
		if err != nil {
			p.opts.Log.Error(err, "failed to watch endpoint", "url", url)
			continue
		}
		p.watchedEndpoints[url] = we
		p.aggregateCache.AddCache(url, we.wildcardCache)
	}

	// check which watched urls can be stopped
	for watchedURL, we := range p.watchedEndpoints {
		if slices.Contains(urls, watchedURL) {
			continue
		}
		// URL no longer present in endpoint slice, cancel watch
		we.cancel()
		p.aggregateCache.RemoveCache(watchedURL)
		delete(p.watchedEndpoints, watchedURL)
	}
}

func (p *Provider) stopAllWatchedEndpoints() {
	for watchedURL, we := range p.watchedEndpoints {
		we.cancel()
		p.aggregateCache.RemoveCache(watchedURL)
	}
	p.watchedEndpoints = make(map[string]*watchedEndpoint)
}

type watchedEndpoint struct {
	cancel        context.CancelFunc
	provider      *Provider
	config        *rest.Config
	wildcardCache mcpcache.WildcardCache
	logger        *logr.Logger
}

func (p *Provider) watchEndpoint(ctx context.Context, url string, aware multicluster.Aware) (*watchedEndpoint, error) {
	ctx, cancel := context.WithCancel(ctx)

	we := new(watchedEndpoint)
	we.cancel = cancel
	we.provider = p

	we.config = rest.CopyConfig(p.config)
	we.config.Host = url

	wildcardCache, err := mcpcache.NewWildcardCache(we.config, cache.Options{Scheme: we.provider.opts.Scheme})
	if err != nil {
		we.cancel()
		return nil, fmt.Errorf("error starting cache: %w", err)
	}
	we.wildcardCache = wildcardCache

	logger := p.opts.Log.WithValues("url", url)
	we.logger = &logger

	if err := we.start(ctx, aware); err != nil {
		we.cancel()
		we.logger.Error(err, "error starting endpoint watcher")
		return nil, err
	}

	return we, nil
}

func (we *watchedEndpoint) start(ctx context.Context, aware multicluster.Aware) error {
	informer, err := we.wildcardCache.GetInformer(ctx, we.provider.opts.ObjectToWatch, cache.BlockUntilSynced(false))
	if err != nil {
		return fmt.Errorf("failed to get %T informer: %w", we.provider.opts.ObjectToWatch, err)
	}

	handler, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			t := obj.(client.Object)
			we.logger.Info("new endpoint object", "object", t)
			if filter := we.provider.opts.AddFilter; filter != nil {
				accept, err := filter(t)
				if err != nil {
					we.logger.Error(err, "error in filter function")
					return
				}
				if !accept {
					return
				}
			}
			if err := we.update(ctx, t, aware); err != nil {
				we.logger.Error(err, "unexpected error handling add event")
			}
			we.provider.opts.Handlers.RunOnAdd(t)
		},
		UpdateFunc: func(oldObj any, newObj any) {
			t := newObj.(client.Object)
			we.logger.Info("updated endpoint object", "object", t)
			if filter := we.provider.opts.UpdateFilter; filter != nil {
				accept, err := filter(t)
				if err != nil {
					we.logger.Error(err, "error in filter function")
					return
				}
				if !accept {
					return
				}
			}
			if err := we.update(ctx, t, aware); err != nil {
				we.logger.Error(err, "unexpected error handling update event")
			}
			we.provider.opts.Handlers.RunOnUpdate(oldObj.(client.Object), t)
		},
		DeleteFunc: func(obj any) {
			t, ok := obj.(client.Object)
			if !ok {
				tombstone, ok := obj.(toolscache.DeletedFinalStateUnknown)
				if !ok {
					we.logger.Error(nil, "couldn't get object from tombstone", "obj", obj)
					return
				}
				t, ok = tombstone.Obj.(client.Object)
				if !ok {
					we.logger.Error(nil, "tombstone contained object that is not expected", "obj", obj)
					return
				}
			}
			we.logger.Info("deleted endpoint object", "object", obj)
			clusterName := logicalcluster.From(t)

			shInf, _, _, err := we.wildcardCache.GetSharedInformer(we.provider.opts.ObjectToWatch)
			if err != nil {
				we.logger.Error(err, "failed to get shared informer for delete check", "cluster", clusterName)
				return
			}

			keys, err := shInf.GetIndexer().IndexKeys(kcpcache.ClusterIndexName, clusterName.String())
			if err != nil {
				we.logger.Error(err, "failed to get index keys", "cluster", clusterName)
				return
			}

			if len(keys) > 0 {
				return
			}

			we.provider.recorderManager.StopProvider(ctx, clusterName)
			we.provider.Clusters.Remove(multicluster.ClusterName(clusterName.String()))
			we.provider.opts.Handlers.RunOnDelete(t)
		},
	})
	if err != nil {
		return fmt.Errorf("error adding event handler: %w", err)
	}

	go func() {
		defer informer.RemoveEventHandler(handler) //nolint:errcheck
		if err := we.wildcardCache.Start(ctx); err != nil {
			we.logger.Error(err, "error in cache for endpoint")
		}
	}()

	return nil
}

func (we *watchedEndpoint) update(ctx context.Context, obj client.Object, aware multicluster.Aware) error {
	clusterName := logicalcluster.From(obj)

	// check if cluster already exists before creating. There is small chance for race but its ok.
	if _, err := we.provider.Clusters.Get(ctx, multicluster.ClusterName(clusterName.String())); err == nil {
		return nil
	}

	recorder, err := we.provider.recorderManager.GetProvider(we.config, clusterName)
	if err != nil {
		return fmt.Errorf("failed to get broadcaster: %w", err)
	}

	// create new scoped cluster.
	cl, err := we.provider.opts.NewCluster(we.config, clusterName, we.wildcardCache, we.provider.opts.Scheme, recorder)
	if err != nil {
		return fmt.Errorf("failed to create cluster %q: %w", clusterName, err)
	}

	if err := we.provider.Clusters.Add(ctx, multicluster.ClusterName(clusterName.String()), cl, aware); err != nil {
		return fmt.Errorf("failed to add cluster %q: %w", clusterName, err)
	}

	return nil
}
