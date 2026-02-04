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

package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"

	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	mcpcache "github.com/kcp-dev/multicluster-provider/pkg/cache"
	"github.com/kcp-dev/multicluster-provider/pkg/handlers"
)

// Factory generates Providers
type Factory struct {
	Clusters *Clusters

	Providers map[string]*Provider

	Log      logr.Logger
	Handlers handlers.Handlers

	GetVWs func(obj client.Object) ([]string, error)

	Config        *rest.Config
	Scheme        *runtime.Scheme
	Outer, Inner  client.Object
	Cache         cache.Cache
	WildcardCache mcpcache.WildcardCache
	NewCluster    NewClusterFunc
}

// Get returns the cluster with the given name as a cluster.Cluster.
func (f *Factory) Get(ctx context.Context, clusterName string) (cluster.Cluster, error) {
	return f.Clusters.Get(ctx, clusterName)
}

// IndexField adds an indexer to the clusters managed by this provider.
func (f *Factory) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	return f.Clusters.IndexField(ctx, obj, field, extractValue)
}

// Start starts watching for objects of type T.
func (f *Factory) Start(ctx context.Context, aware multicluster.Aware) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if f.GetVWs == nil {
		return fmt.Errorf("auxiliary function to retrieve virtual workspace URLs is unset")
	}

	informer, err := f.Cache.GetInformer(ctx, f.Outer)
	if err != nil {
		cancel()
		return err
	}

	handler, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			t := obj.(client.Object)
			f.Log.Info("detected new object", "object", t)
			f.update(ctx, aware, t)
		},
		UpdateFunc: func(oldObj any, newObj any) {
			t := newObj.(client.Object)
			f.Log.Info("detected updated object", "object", t)
			f.update(ctx, aware, t)
		},
		DeleteFunc: func(obj any) {
			f.Log.Info("detected deleted object, stopping all providers", "object", obj)
			cancel()
		},
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := informer.RemoveEventHandler(handler); err != nil {
			f.Log.Error(err, "failed to remove event handler")
		}
	}()

	f.Log.Info("starting factory")
	return f.Cache.Start(ctx)
}

func (f *Factory) update(ctx context.Context, aware multicluster.Aware, obj client.Object) {
	var current = make(map[string]bool) // currently registered providers

	if f.GetVWs == nil {
		f.Log.Error(nil, "auxiliary function to retrieve virtual workspace URLs is unset")
		return
	}

	vws, err := f.GetVWs(obj)
	if err != nil {
		f.Log.Error(err, "failed to get virtual workspace URLs")
		return
	}

	for _, vw := range vws {
		id := hashString(vw)
		current[id] = true

		// Skip already registered endpoints.
		if _, exists := f.Providers[id]; exists {
			continue
		}

		// Start provider if we didn't have it registered before.
		// Else, we just mark that we found it (for cleaning up outdated providers later).
		cfg := rest.CopyConfig(f.Config)
		cfg.Host = vw

		logger := f.Log.WithValues("url", vw)
		prov, err := New(cfg, f.Clusters, Options{
			ObjectToWatch: f.Inner,
			Scheme:        f.Scheme,
			Log:           &logger,
			Handlers:      f.Handlers,
			WildcardCache: f.WildcardCache,
			NewCluster:    f.NewCluster,
		})
		if err != nil {
			f.Log.Error(err, "failed to create provider")
			continue
		}

		if err := prov.Start(ctx, aware); err != nil {
			f.Log.Error(err, "failed to start provider")
			continue
		}

		f.Providers[id] = prov
		current[id] = true
	}

	// Clean up providers that are no longer present in the endpoint slice.
	for id := range f.Providers {
		if _, exists := current[id]; exists {
			continue
		}
		f.Log.Info("stopping provider for removed endpoint", "id", id)
		delete(f.Providers, id)
	}
}

func hashString(s string) string {
	sha := sha256.New()
	sha.Write([]byte(s))
	return hex.EncodeToString(sha.Sum(nil))[:8]
}
