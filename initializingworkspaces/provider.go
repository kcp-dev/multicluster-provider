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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"sigs.k8s.io/multicluster-runtime/pkg/clusters"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
	kcptenancyv1alpha1 "github.com/kcp-dev/sdk/apis/tenancy/v1alpha1"

	"github.com/kcp-dev/multicluster-provider/pkg/handlers"
	"github.com/kcp-dev/multicluster-provider/pkg/provider"
)

var _ multicluster.Provider = &Provider{}
var _ multicluster.ProviderRunnable = &Provider{}

// Provider is a [sigs.k8s.io/multicluster-runtime/pkg/multicluster.Provider] that represents each [logical cluster]
// (in the kcp sense) having a specific initializer and exposed via the initializingworkspaces virtual workspace endpoint
// as a cluster in the [sigs.k8s.io/multicluster-runtime] sense.
//
// [logical cluster]: https://docs.kcp.io/kcp/latest/concepts/terminology/#logical-cluster
type Provider struct {
	clusters *provider.Clusters
	aware    multicluster.Aware
	context  context.Context

	providersLock sync.RWMutex
	providers     map[string]*provider.Provider

	log      logr.Logger
	handlers handlers.Handlers

	config *rest.Config
	scheme *runtime.Scheme
	object client.Object
	cache  cache.Cache
}

// Options are the options for creating a new instance of the initializing workspaces provider.
type Options struct {
	// Scheme is the scheme to use for the provider. If this is nil, it defaults
	// to the client-go scheme.
	Scheme *runtime.Scheme

	// Log is the logger to use for the provider. If this is nil, it defaults
	// to the controller-runtime default logger.
	Log *logr.Logger

	// ObjectToWatch is the object type that the provider watches via a /clusters/*
	// wildcard endpoint to extract information about logical clusters joining and
	// leaving the "fleet" of (logical) clusters in kcp. If this is nil, it defaults
	// to [kcpcorev1alpha1.LogicalCluster]. This might be useful when using this provider
	// against custom virtual workspaces that are not the APIExport one but share
	// the same endpoint semantics.
	ObjectToWatch client.Object

	// Handlers are lifecycle handlers, ran for each logical cluster in the provider represented
	// by LogicalCluster object.
	Handlers handlers.Handlers
}

// New creates a new KCP initializing workspaces provider.
func New(cfg *rest.Config, workspaceTypeName string, options Options) (*Provider, error) {
	// Do the defaulting controller-runtime would do for those fields we need.
	if options.Scheme == nil {
		options.Scheme = scheme.Scheme
	}

	if options.ObjectToWatch == nil {
		options.ObjectToWatch = &kcpcorev1alpha1.LogicalCluster{}
	}

	if options.Log == nil {
		options.Log = ptr.To(log.Log.WithName("kcp-initializingworkspaces-cluster-provider"))
	}

	c, err := cache.New(cfg, cache.Options{
		Scheme: options.Scheme,
		ByObject: map[client.Object]cache.ByObject{
			&kcptenancyv1alpha1.WorkspaceType{}: {
				Field: fields.SelectorFromSet(fields.Set{"metadata.name": workspaceTypeName}),
			},
		},
	})
	if err != nil {
		return nil, err
	}

	return &Provider{
		// func to pass into iner provider to lifecycle clusters
		clusters:  ptr.To(clusters.New[cluster.Cluster]()),
		providers: map[string]*provider.Provider{},

		config: cfg,
		scheme: options.Scheme,
		object: options.ObjectToWatch,
		cache:  c,

		log:      *options.Log,
		handlers: options.Handlers,
	}, nil
}

// Start starts the provider and blocks.
func (p *Provider) Start(ctx context.Context, aware multicluster.Aware) error {
	p.aware = aware
	p.context = ctx
	// Create a child context we can cancel when the WorkspaceType goes away.
	ctx, cancel := context.WithCancel(ctx)

	informer, err := p.cache.GetInformer(ctx, &kcptenancyv1alpha1.WorkspaceType{})
	if err != nil {
		cancel()
		return err
	}

	handler, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			wst := obj.(*kcptenancyv1alpha1.WorkspaceType)
			p.log.Info("added WorkspaceType", "name", wst.Name)
			p.update(wst)
		},
		UpdateFunc: func(oldObj any, newObj any) {
			wst := newObj.(*kcptenancyv1alpha1.WorkspaceType)
			p.log.Info("updated WorkspaceType", "name", wst.Name)
			p.update(wst)
		},
		DeleteFunc: func(obj any) {
			p.log.Info("deleted WorkspaceType, stopping provider")
			cancel()
		},
	})
	if err != nil {
		return err
	}

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		<-ctx.Done()
		err := informer.RemoveEventHandler(handler)
		p.log.Info("removed cache event handler")
		return err
	})

	g.Go(func() error {
		err := p.cache.Start(ctx)
		p.log.Info("cache stopped")
		return err
	})

	if !p.cache.WaitForCacheSync(ctx) {
		return fmt.Errorf("failed to wait for sync")
	}

	p.log.V(4).Info("caches have synced")

	return g.Wait()
}

// update will look into the given WorkspaceType and ensure that providers
// are initiated for every URL. It reads every URL and sets up a provider if needed for it,
// and registers it inside current map. Finally, it cleans up any providers that are no longer
// present in the endpoint slice (present in the providers but not in the current map).
func (p *Provider) update(wst *kcptenancyv1alpha1.WorkspaceType) {
	p.providersLock.Lock()
	defer p.providersLock.Unlock()

	if p.aware == nil {
		p.log.Info("aware is not set yet, skipping update")
		return
	}

	var current = make(map[string]bool) // currently registered providers

	for _, endpoint := range wst.Status.VirtualWorkspaces {
		// The slice contains both the URLs for the initializingworkspaces and the
		// terminatingworkspaces virtual workspace, so we need to filter.
		if !strings.Contains(endpoint.URL, "/initializingworkspaces/") {
			continue
		}

		id := hashURL(endpoint.URL)
		current[id] = true

		// Skip already registered endpoints.
		if _, exists := p.providers[id]; exists {
			continue
		}

		// Start provider if we didn't have it registered before.
		// Else, we just mark that we found it (for cleaning up outdated providers later).
		cfg := rest.CopyConfig(p.config)
		cfg.Host = endpoint.URL

		logger := p.log.WithValues("url", endpoint.URL)
		prov, err := provider.New(cfg, p.clusters, provider.Options{
			ObjectToWatch: p.object,
			Scheme:        p.scheme,
			Log:           &logger,
			Handlers:      p.handlers,
		})
		if err != nil {
			p.log.Error(err, "failed to create provider")
			continue
		}

		err = prov.Start(p.context, p.aware)
		if err != nil {
			p.log.Error(err, "failed to start provider")
			continue
		}

		p.providers[id] = prov
		current[id] = true
	}

	// Clean up providers that are no longer present in the endpoint slice.
	for id := range p.providers {
		if _, exists := current[id]; exists {
			continue
		}
		p.log.Info("stopping provider for removed endpoint", "id", id)
		delete(p.providers, id)
	}
}

// Get returns the cluster with the given name as a cluster.Cluster.
func (p *Provider) Get(ctx context.Context, clusterName string) (cluster.Cluster, error) {
	return p.clusters.Get(ctx, clusterName)
}

// IndexField adds an indexer to the clusters managed by this provider.
func (p *Provider) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	return p.clusters.IndexField(ctx, obj, field, extractValue)
}

// hashURL hashes an URL from an WorkspaceType status.
func hashURL(url string) string {
	sha := sha256.New()
	sha.Write([]byte(url))
	return hex.EncodeToString(sha.Sum(nil))[:8]
}
