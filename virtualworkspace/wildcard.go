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
	"strings"
	"sync"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	k8scache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	kcpcache "github.com/kcp-dev/apimachinery/v2/pkg/cache"
	kcpinformers "github.com/kcp-dev/apimachinery/v2/third_party/informers"
	"github.com/kcp-dev/logicalcluster/v3"
)

// WildcardCache is a cache that operates on a /clusters/* endpoint.
type WildcardCache interface {
	cache.Cache
	getSharedInformer(obj runtime.Object) (k8scache.SharedIndexInformer, schema.GroupVersionKind, apimeta.RESTScopeName, error)
}

// NewWildcardCache returns a cache.Cache that handles multi-cluster watches
// against a /clusters/* endpoint. It wires SharedIndexInformers with additional
// indexes for cluster and cluster+namespace.
func NewWildcardCache(config *rest.Config, opts cache.Options) (WildcardCache, error) {
	config = rest.CopyConfig(config)
	config.Host = strings.TrimSuffix(config.Host, "/") + "/clusters/*"

	// setup everything we need to get a working REST mapper.
	if opts.Scheme == nil {
		opts.Scheme = scheme.Scheme
	}
	if opts.HTTPClient == nil {
		var err error
		opts.HTTPClient, err = rest.HTTPClientFor(config)
		if err != nil {
			return nil, fmt.Errorf("could not create HTTP client from config: %w", err)
		}
	}
	if opts.Mapper == nil {
		var err error
		opts.Mapper, err = apiutil.NewDynamicRESTMapper(config, opts.HTTPClient)
		if err != nil {
			return nil, fmt.Errorf("could not create RESTMapper from config: %w", err)
		}
	}

	ret := &wildcardCache{
		scheme: opts.Scheme,
		mapper: opts.Mapper,
		tracker: informerTracker{
			Structured:   make(map[schema.GroupVersionKind]k8scache.SharedIndexInformer),
			Unstructured: make(map[schema.GroupVersionKind]k8scache.SharedIndexInformer),
			Metadata:     make(map[schema.GroupVersionKind]k8scache.SharedIndexInformer),
		},

		readerFailOnMissingInformer: opts.ReaderFailOnMissingInformer,
	}

	opts.NewInformer = func(watcher k8scache.ListerWatcher, obj runtime.Object, duration time.Duration, indexers k8scache.Indexers) k8scache.SharedIndexInformer {
		gvk, err := apiutil.GVKForObject(obj, opts.Scheme)
		if err != nil {
			panic(err)
		}

		inf := kcpinformers.NewSharedIndexInformer(watcher, obj, duration, indexers)
		if err := inf.AddIndexers(k8scache.Indexers{
			kcpcache.ClusterIndexName:             ClusterIndexFunc,
			kcpcache.ClusterAndNamespaceIndexName: ClusterAndNamespaceIndexFunc,
		}); err != nil {
			utilruntime.HandleError(fmt.Errorf("unable to add cluster name indexers: %w", err))
		}

		infs := ret.tracker.informersByType(obj)
		ret.tracker.lock.Lock()
		if _, ok := infs[gvk]; ok {
			panic(fmt.Sprintf("informer for %s already exists", gvk))
		}
		infs[gvk] = inf
		ret.tracker.lock.Unlock()

		return inf
	}

	var err error
	ret.Cache, err = cache.New(config, opts)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

type wildcardCache struct {
	cache.Cache
	scheme  *runtime.Scheme
	mapper  apimeta.RESTMapper
	tracker informerTracker

	readerFailOnMissingInformer bool
}

func (c *wildcardCache) getSharedInformer(obj runtime.Object) (k8scache.SharedIndexInformer, schema.GroupVersionKind, apimeta.RESTScopeName, error) {
	gvk, err := apiutil.GVKForObject(obj, c.scheme)
	if err != nil {
		return nil, gvk, "", fmt.Errorf("failed to get GVK for object: %w", err)
	}

	// We need the non-list GVK, so chop off the "List" from the end of the kind.
	gvk.Kind = strings.TrimSuffix(gvk.Kind, "List")

	mapping, err := c.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, gvk, "", fmt.Errorf("failed to get REST mapping: %w", err)
	}

	infs := c.tracker.informersByType(obj)
	c.tracker.lock.RLock()
	inf, ok := infs[gvk]
	c.tracker.lock.RUnlock()

	// we need to create a new informer here.
	if !ok {
		// we have been instructed to fail if the informer is missing.
		if c.readerFailOnMissingInformer {
			return nil, gvk, "", &cache.ErrResourceNotCached{}
		}

		// Let's generate a new object from the chopped GVK, since the original obj might be of *List type.
		o, err := c.scheme.New(gvk)
		if err != nil {
			return nil, gvk, "", fmt.Errorf("failed to create object for GVK: %w", err)
		}

		// Call GetInformer, but we don't care about the output. We just need to make sure that our NewInformer
		// func has been called, which registers the new informer in our tracker.
		if _, err := c.Cache.GetInformer(context.TODO(), o.(client.Object)); err != nil {
			return nil, gvk, "", fmt.Errorf("failed to create informer: %w", err)
		}

		// Now we should be able to find the informer.
		infs := c.tracker.informersByType(obj)
		c.tracker.lock.RLock()
		inf, ok = infs[gvk]
		c.tracker.lock.RUnlock()

		if !ok {
			return nil, gvk, "", fmt.Errorf("failed to find newly started informer for %v", gvk)
		}
	}

	return inf, gvk, mapping.Scope.Name(), nil
}

// IndexField adds an index for the given object kind.
func (c *wildcardCache) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	return c.Cache.IndexField(ctx, obj, "cluster/"+field, func(obj client.Object) []string {
		keys := extractValue(obj)
		withCluster := make([]string, len(keys)*2)
		for i, key := range keys {
			withCluster[i] = fmt.Sprintf("%s/%s", logicalcluster.From(obj), key)
			withCluster[i+len(keys)] = fmt.Sprintf("*/%s", key)
		}
		return withCluster
	})
}

type informerTracker struct {
	lock         sync.RWMutex
	Structured   map[schema.GroupVersionKind]k8scache.SharedIndexInformer
	Unstructured map[schema.GroupVersionKind]k8scache.SharedIndexInformer
	Metadata     map[schema.GroupVersionKind]k8scache.SharedIndexInformer
}

func (t *informerTracker) informersByType(obj runtime.Object) map[schema.GroupVersionKind]k8scache.SharedIndexInformer {
	switch obj.(type) {
	case runtime.Unstructured:
		return t.Unstructured
	case *metav1.PartialObjectMetadata, *metav1.PartialObjectMetadataList:
		return t.Metadata
	default:
		return t.Structured
	}
}
