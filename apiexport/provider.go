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
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	"sigs.k8s.io/multicluster-runtime/providers/multi"

	apisv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"

	"github.com/kcp-dev/multicluster-provider/apiexport/internal/provider"
)

var _ multicluster.Provider = &Provider{}
var _ multicluster.ProviderRunnable = &Provider{}

// Provider is a [sigs.k8s.io/multicluster-runtime/pkg/multicluster.Provider] that represents each [logical cluster]
// (in the kcp sense) exposed via a APIExport virtual workspace as a cluster in the [sigs.k8s.io/multicluster-runtime] sense.
//
// [logical cluster]: https://docs.kcp.io/kcp/latest/concepts/terminology/#logical-cluster
type Provider struct {
	*multi.Provider

	log logr.Logger

	config *rest.Config
	scheme *runtime.Scheme
	object client.Object
	cache  cache.Cache
}

// Options are the options for creating a new instance of the apiexport provider.
type Options struct {
	// Scheme is the scheme to use for the provider. If this is nil, it defaults
	// to the client-go scheme.
	Scheme *runtime.Scheme

	// ObjectToWatch is the object type that the provider watches via a /clusters/*
	// wildcard endpoint to extract information about logical clusters joining and
	// leaving the "fleet" of (logical) clusters in kcp. If this is nil, it defaults
	// to [apisv1alpha1.APIBinding]. This might be useful when using this provider
	// against custom virtual workspaces that are not the APIExport one but share
	// the same endpoint semantics.
	ObjectToWatch client.Object
}

// New creates a new kcp virtual workspace provider. The provided [rest.Config]
// must point to a virtual workspace apiserver base path, i.e. up to but without
// the '/clusters/*' suffix. This information can be extracted from the APIExport
// status (deprecated) or an APIExportEndpointSlice status.
func New(cfg *rest.Config, endpointSliceName string, options Options) (*Provider, error) {
	// Do the defaulting controller-runtime would do for those fields we need.
	if options.Scheme == nil {
		options.Scheme = scheme.Scheme
	}
	if options.ObjectToWatch == nil {
		options.ObjectToWatch = &apisv1alpha1.APIBinding{}
	}

	c, err := cache.New(cfg, cache.Options{
		Scheme: options.Scheme,
		ByObject: map[client.Object]cache.ByObject{
			&apisv1alpha1.APIExportEndpointSlice{}: {
				Field: fields.SelectorFromSet(fields.Set{"metadata.name": endpointSliceName}),
			},
		},
	})
	if err != nil {
		return nil, err
	}

	return &Provider{
		Provider: multi.New(multi.Options{}),

		config: cfg,
		scheme: options.Scheme,
		object: options.ObjectToWatch,
		cache:  c,

		log: log.Log.WithName("kcp-apiexport-cluster-provider"),
	}, nil
}

// Start starts the provider and blocks.
func (p *Provider) Start(ctx context.Context, aware multicluster.Aware) error {
	// Create a child context we can cancel when the APIExportEndpointSlice goes away.
	ctx, cancel := context.WithCancel(ctx)

	informer, err := p.cache.GetInformer(ctx, &apisv1alpha1.APIExportEndpointSlice{})
	if err != nil {
		cancel()
		return err
	}

	handler, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			es := obj.(*apisv1alpha1.APIExportEndpointSlice)
			p.log.Info("added APIExportEndpointSlice", "name", es.Name)
			p.update(es)
		},
		UpdateFunc: func(oldObj any, newObj any) {
			es := newObj.(*apisv1alpha1.APIExportEndpointSlice)
			p.log.Info("updated APIExportEndpointSlice", "name", es.Name)
			p.update(es)
		},
		DeleteFunc: func(obj any) {
			p.log.Info("deleted APIExportEndpointSlice, stopping provider")
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
	g.Go(func() error {
		err := p.Provider.Start(ctx, aware)
		p.log.Info("provider stopped")
		return err
	})

	if !p.cache.WaitForCacheSync(ctx) {
		return fmt.Errorf("failed to wait for sync")
	}

	p.log.V(4).Info("caches have synced")

	return g.Wait()
}

func (p *Provider) update(es *apisv1alpha1.APIExportEndpointSlice) {
	currrent := map[string]struct{}{}

	for _, endpoint := range es.Status.APIExportEndpoints {
		id := HashAPIExportURL(endpoint.URL)
		// Skip already registered endpoints.
		if _, exists := p.GetProvider(id); exists {
			currrent[id] = struct{}{}
			continue
		}

		// Start provider if we didn't have it registered before.
		// Else, we just mark that we found it (for cleaning up outdated providers later).
		cfg := rest.CopyConfig(p.config)
		cfg.Host = endpoint.URL

		logger := p.log.WithValues("endpoint", id)
		prov, err := provider.New(cfg, provider.Options{ObjectToWatch: p.object, Scheme: p.scheme, Log: &logger})
		if err != nil {
			p.log.Error(err, "failed to create provider")
			continue
		}
		if err := p.AddProvider(id, prov); err != nil {
			p.log.Error(err, "failed to add provider")
			continue
		}
		p.log.Info("added endpoint as new provider", "endpoint", id, "url", endpoint.URL)
		currrent[id] = struct{}{}
	}

	for _, prefix := range p.ProviderNames() {
		if _, exists := currrent[prefix]; !exists {
			p.RemoveProvider(prefix)
			p.log.Info("removed endpoint provider", "endpoint", prefix)
		}
	}
}

// HashAPIExportURL hashes an URL from an APIExportEndpointSlice status.
// It can be used to address clusters by calling GetCluster with "<URL hash>#<logical cluster name>".
func HashAPIExportURL(url string) string {
	sha := sha256.New()
	sha.Write([]byte(url))
	return hex.EncodeToString(sha.Sum(nil))[:8]
}
