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

package cache

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

// WildcardCache is a cache that operates on a '/clusters/*' endpoint.
type WildcardCache interface {
	cache.Cache
	GetSharedInformer(obj runtime.Object) (k8scache.SharedIndexInformer, schema.GroupVersionKind, apimeta.RESTScopeName, error)
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
		scheme:           opts.Scheme,
		mapper:           opts.Mapper,
		indexTrackerLock: sync.RWMutex{},
		indexTracker:     make(map[string]struct{}),
	}

	opts.NewInformer = func(watcher k8scache.ListerWatcher, obj runtime.Object, duration time.Duration, indexers k8scache.Indexers) k8scache.SharedIndexInformer {
		inf := kcpinformers.NewSharedIndexInformer(watcher, obj, duration, indexers)
		if err := inf.AddIndexers(k8scache.Indexers{
			kcpcache.ClusterIndexName:             ClusterIndexFunc,
			kcpcache.ClusterAndNamespaceIndexName: ClusterAndNamespaceIndexFunc,
		}); err != nil {
			utilruntime.HandleError(fmt.Errorf("unable to add cluster name indexers: %w", err))
		}
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
	scheme *runtime.Scheme
	mapper apimeta.RESTMapper

	indexTrackerLock sync.RWMutex
	indexTracker     map[string]struct{}
}

func (c *wildcardCache) GetSharedInformer(obj runtime.Object) (k8scache.SharedIndexInformer, schema.GroupVersionKind, apimeta.RESTScopeName, error) {
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

	var emptyObject client.Object
	switch obj.(type) {
	case runtime.Unstructured:
		emptyObject = &unstructured.Unstructured{}
		emptyObject.GetObjectKind().SetGroupVersionKind(gvk)
	case *metav1.PartialObjectMetadata, *metav1.PartialObjectMetadataList:
		emptyObject = &metav1.PartialObjectMetadata{}
		emptyObject.GetObjectKind().SetGroupVersionKind(gvk)
	default:
		o, err := c.scheme.New(gvk)
		if err != nil {
			return nil, gvk, "", fmt.Errorf("failed to create new object for GVK: %w", err)
		}
		var ok bool
		emptyObject, ok = o.(client.Object)
		if !ok {
			return nil, gvk, "", fmt.Errorf("runtime.Object returned from scheme is not a client.Object")
		}
	}

	inf, err := c.Cache.GetInformer(context.TODO(), emptyObject)
	if err != nil {
		return nil, gvk, "", fmt.Errorf("failed to get informer: %w", err)
	}

	sii, ok := inf.(k8scache.SharedIndexInformer)
	if !ok {
		return nil, gvk, "", fmt.Errorf("informer returned by cache is not a SharedIndexInformer")
	}

	return sii, gvk, mapping.Scope.Name(), nil
}

// IndexField adds an index for the given object kind.
func (c *wildcardCache) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	gvk := obj.GetObjectKind().GroupVersionKind()
	c.indexTrackerLock.Lock()
	key := fmt.Sprintf("%s|%s", gvk.String(), field)
	if _, exists := c.indexTracker[key]; exists {
		// already indexed
		c.indexTrackerLock.Unlock()
		return nil
	}
	c.indexTracker[key] = struct{}{}
	c.indexTrackerLock.Unlock()

	return c.Cache.IndexField(ctx, obj, field, func(obj client.Object) []string {
		keys := extractValue(obj)
		withCluster := make([]string, len(keys)*2)
		for i, key := range keys {
			withCluster[i] = fmt.Sprintf("%s/%s", logicalcluster.From(obj), key)
			withCluster[i+len(keys)] = fmt.Sprintf("*/%s", key)
		}
		return withCluster
	})
}
