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
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	toolscache "k8s.io/client-go/tools/cache"
)

// AggregateSharedIndexInformer provides SharedIndexInformer-like functionality
// that aggregates across all shards and handles dynamic cache changes.
type AggregateSharedIndexInformer interface {
	// AddEventHandler registers a handler that receives events from all
	// current and future shards.
	AddEventHandler(handler toolscache.ResourceEventHandler) (toolscache.ResourceEventHandlerRegistration, error)

	// AddEventHandlerWithResyncPeriod registers a handler with a resync period.
	AddEventHandlerWithResyncPeriod(handler toolscache.ResourceEventHandler, resyncPeriod time.Duration) (toolscache.ResourceEventHandlerRegistration, error)

	// HasSynced returns true when all current shard informers have synced.
	HasSynced() bool
}

// handlerEntry tracks a registered handler and its per-cache registrations.
type handlerEntry struct {
	handler       toolscache.ResourceEventHandler
	resyncPeriod  time.Duration
	registrations map[string]toolscache.ResourceEventHandlerRegistration
}

// aggregateSharedIndexInformer implements AggregateSharedIndexInformer.
type aggregateSharedIndexInformer struct {
	cache *AggregateCache
	obj   runtime.Object

	lock     sync.RWMutex
	handlers []*handlerEntry
}

var _ AggregateSharedIndexInformer = &aggregateSharedIndexInformer{}

// newAggregateSharedIndexInformer creates a new aggregate informer for the given object type.
func newAggregateSharedIndexInformer(cache *AggregateCache, obj runtime.Object) *aggregateSharedIndexInformer {
	return &aggregateSharedIndexInformer{
		cache:    cache,
		obj:      obj,
		handlers: make([]*handlerEntry, 0),
	}
}

// AddEventHandler registers a handler that receives events from all current and future shards.
func (a *aggregateSharedIndexInformer) AddEventHandler(handler toolscache.ResourceEventHandler) (toolscache.ResourceEventHandlerRegistration, error) {
	return a.AddEventHandlerWithResyncPeriod(handler, 0)
}

// AddEventHandlerWithResyncPeriod registers a handler with a resync period.
func (a *aggregateSharedIndexInformer) AddEventHandlerWithResyncPeriod(handler toolscache.ResourceEventHandler, resyncPeriod time.Duration) (toolscache.ResourceEventHandlerRegistration, error) {
	a.cache.lock.RLock()
	defer a.cache.lock.RUnlock()

	a.lock.Lock()
	defer a.lock.Unlock()

	handlerEntry := &handlerEntry{
		handler:       handler,
		resyncPeriod:  resyncPeriod,
		registrations: make(map[string]toolscache.ResourceEventHandlerRegistration),
	}

	// Register with all current caches
	for cacheID, c := range a.cache.caches {
		inf, _, _, err := c.GetSharedInformer(a.obj)
		if err != nil {
			a.cleanupRegistrations(handlerEntry)
			return nil, fmt.Errorf("cache %q: failed to get informer: %w", cacheID, err)
		}
		reg, err := inf.AddEventHandlerWithResyncPeriod(handler, resyncPeriod)
		if err != nil {
			a.cleanupRegistrations(handlerEntry)
			return nil, fmt.Errorf("cache %q: failed to add handler: %w", cacheID, err)
		}
		handlerEntry.registrations[cacheID] = reg
	}

	// Track handler so we can register it with future caches
	a.handlers = append(a.handlers, handlerEntry)

	return &aggregateRegistration{
		informer:     a,
		handlerEntry: handlerEntry,
	}, nil
}

// cleanupRegistrations removes all registrations for an entry.
func (a *aggregateSharedIndexInformer) cleanupRegistrations(entry *handlerEntry) {
	for id, reg := range entry.registrations {
		if c, ok := a.cache.caches[id]; ok {
			if inf, _, _, err := c.GetSharedInformer(a.obj); err == nil {
				_ = inf.RemoveEventHandler(reg)
			}
		}
	}
}

// onCacheAdded is called when a new cache is added - registers all existing handlers.
func (a *aggregateSharedIndexInformer) onCacheAdded(cacheID string, c WildcardCache) {
	a.lock.Lock()
	defer a.lock.Unlock()

	inf, _, _, err := c.GetSharedInformer(a.obj)
	if err != nil {
		// Cache may not support this object type - skip silently
		return
	}

	for _, entry := range a.handlers {
		reg, err := inf.AddEventHandlerWithResyncPeriod(entry.handler, entry.resyncPeriod)
		if err != nil {
			continue
		}
		entry.registrations[cacheID] = reg
	}
}

// onCacheRemoved is called when a cache is removed - cleans up registrations.
func (a *aggregateSharedIndexInformer) onCacheRemoved(cacheID string) {
	a.lock.Lock()
	defer a.lock.Unlock()

	for _, entry := range a.handlers {
		delete(entry.registrations, cacheID)
	}
}

// HasSynced returns true when all current shard informers have synced.
func (a *aggregateSharedIndexInformer) HasSynced() bool {
	a.cache.lock.RLock()
	defer a.cache.lock.RUnlock()

	// If no shards, consider synced
	if len(a.cache.caches) == 0 {
		return true
	}

	for _, c := range a.cache.caches {
		inf, _, _, err := c.GetSharedInformer(a.obj)
		if err != nil {
			// If we can't get the informer, it's not synced
			return false
		}
		if !inf.HasSynced() {
			return false
		}
	}
	return true
}

// aggregateRegistration implements toolscache.ResourceEventHandlerRegistration
// for aggregate informers.
type aggregateRegistration struct {
	informer     *aggregateSharedIndexInformer
	handlerEntry *handlerEntry
}

var _ toolscache.ResourceEventHandlerRegistration = &aggregateRegistration{}

// HasSynced returns true when all shard registrations have synced.
func (r *aggregateRegistration) HasSynced() bool {
	r.informer.lock.RLock()
	defer r.informer.lock.RUnlock()

	for _, reg := range r.handlerEntry.registrations {
		if !reg.HasSynced() {
			return false
		}
	}
	return true
}

// HasSyncedChecker returns nil - use HasSynced() for sync checking.
func (r *aggregateRegistration) HasSyncedChecker() toolscache.DoneChecker {
	return nil
}
