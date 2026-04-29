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
	"k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Lister can list objects across all registered shard caches.
type Lister interface {
	List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error
}

// AggregateCache provides an aggregated view across multiple per-shard WildcardCaches.
type AggregateCache struct {
	lock   sync.RWMutex
	caches map[string]WildcardCache
}

// NewAggregateCache returns an initialized AggregateCache.
func NewAggregateCache() *AggregateCache {
	return &AggregateCache{
		caches: make(map[string]WildcardCache),
	}
}

// AddCache registers a WildcardCache under the given id.
func (a *AggregateCache) AddCache(id string, c WildcardCache) {
	a.lock.Lock()
	defer a.lock.Unlock()
	a.caches[id] = c
}

// RemoveCache unregisters the WildcardCache with the given id.
func (a *AggregateCache) RemoveCache(id string) {
	a.lock.Lock()
	defer a.lock.Unlock()
	delete(a.caches, id)
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

	return errors.NewAggregate(errs)
}
