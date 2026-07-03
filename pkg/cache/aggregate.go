/*
Copyright 2026 The kcp Authors.

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
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// Lister can list objects across all registered shard caches.
type Lister interface {
	List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error
}

// AggregateCache provides an aggregated view across multiple per-shard WildcardCaches.
type AggregateCache struct {
	scheme *runtime.Scheme

	lock   sync.RWMutex
	caches map[string]WildcardCache

	// Track aggregate informers for dynamic shard updates
	informersLock sync.RWMutex
	informers     map[schema.GroupVersionKind]*aggregateSharedIndexInformer
}

// NewAggregateCache returns an initialized AggregateCache.
func NewAggregateCache(scheme *runtime.Scheme) *AggregateCache {
	return &AggregateCache{
		scheme:    scheme,
		caches:    make(map[string]WildcardCache),
		informers: make(map[schema.GroupVersionKind]*aggregateSharedIndexInformer),
	}
}

// AddCache registers a WildcardCache under the given id.
func (a *AggregateCache) AddCache(id string, c WildcardCache) {
	a.lock.Lock()
	a.caches[id] = c
	a.lock.Unlock()

	// Notify all aggregate informers to register their handlers with the new shard
	a.informersLock.RLock()
	for _, inf := range a.informers {
		inf.onCacheAdded(id, c)
	}
	a.informersLock.RUnlock()
}

// RemoveCache unregisters the WildcardCache with the given id.
func (a *AggregateCache) RemoveCache(id string) {
	a.lock.Lock()
	delete(a.caches, id)
	a.lock.Unlock()

	// Notify all aggregate informers to clean up registrations for this shard
	a.informersLock.RLock()
	for _, inf := range a.informers {
		inf.onCacheRemoved(id)
	}
	a.informersLock.RUnlock()
}

// GetAggregateInformer returns an AggregateSharedIndexInformer for the given object type.
// The returned informer aggregates events from all current and future shards.
// Calling this method multiple times with the same object type returns the same informer instance.
func (a *AggregateCache) GetAggregateInformer(obj runtime.Object) (AggregateSharedIndexInformer, error) {
	gvk, err := apiutil.GVKForObject(obj, a.scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to get GVK for object: %w", err)
	}

	a.informersLock.Lock()
	defer a.informersLock.Unlock()

	if inf, ok := a.informers[gvk]; ok {
		return inf, nil
	}

	inf := newAggregateSharedIndexInformer(a, obj)
	a.informers[gvk] = inf
	return inf, nil
}

// List aggregates results from all registered caches.
// If any error occurred while aggregating results from all caches the errors will be returned as an [errors.Aggregate].
// Even if errors are returned the list will be filled with any results that could be aggregated.
func (a *AggregateCache) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	a.lock.RLock()
	defer a.lock.RUnlock()

	var allItems []runtime.Object
	var errs []error

	// TODO parallelize this for larger deployments
	for id, c := range a.caches {
		perCacheList, ok := list.DeepCopyObject().(client.ObjectList)
		if !ok {
			errs = append(errs, fmt.Errorf("cache for %q failed: list object %T does not implement DeepCopyObject correctly", id, list))
			continue
		}

		if err := c.List(ctx, perCacheList, opts...); err != nil {
			errs = append(errs, fmt.Errorf("cache for %q failed: listing objects failed: %w", id, err))
			continue
		}

		items, err := meta.ExtractList(perCacheList)
		if err != nil {
			errs = append(errs, fmt.Errorf("cache for %q failed: extracting items failed: %w", id, err))
			continue
		}
		allItems = append(allItems, items...)
	}

	if err := meta.SetList(list, allItems); err != nil {
		errs = append(errs, fmt.Errorf("error setting aggregated items on passed object list: %w", err))
	}

	return kerrors.NewAggregate(errs)
}
