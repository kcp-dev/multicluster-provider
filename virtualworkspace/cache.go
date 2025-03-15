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
	"errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kcp-dev/logicalcluster/v3"
)

var _ cache.Cache = &scopedCache{}

// scopedCache is a Cache that operates on a specific cluster.
type scopedCache struct {
	base        WildcardCache
	clusterName logicalcluster.Name
}

func (c *scopedCache) Start(ctx context.Context) error {
	return errors.New("scoped cache cannot be started")
}

func (c *scopedCache) WaitForCacheSync(ctx context.Context) bool {
	return c.base.WaitForCacheSync(ctx)
}

func (c *scopedCache) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	return c.base.IndexField(ctx, obj, field, extractValue)
}

// Get returns a single object from the cache.
func (c *scopedCache) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	inf, gvk, scope, err := c.base.getSharedInformer(obj)
	if err != nil {
		return fmt.Errorf("failed to get informer for %T %s: %w", obj, obj.GetObjectKind().GroupVersionKind(), err)
	}

	cr := cacheReader{
		indexer:          inf.GetIndexer(),
		groupVersionKind: gvk,
		scopeName:        scope,
		disableDeepCopy:  false,
		clusterName:      c.clusterName,
	}

	return cr.Get(ctx, key, obj, opts...)
}

// List returns a list of objects from the cache.
func (c *scopedCache) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	inf, gvk, scope, err := c.base.getSharedInformer(list)
	if err != nil {
		return fmt.Errorf("failed to get informer for %T %s: %w", list, list.GetObjectKind().GroupVersionKind(), err)
	}

	cr := cacheReader{
		indexer:          inf.GetIndexer(),
		groupVersionKind: gvk,
		scopeName:        scope,
		disableDeepCopy:  false,
		clusterName:      c.clusterName,
	}

	return cr.List(ctx, list, opts...)
}

// GetInformer returns an informer for the given object kind.
func (c *scopedCache) GetInformer(ctx context.Context, obj client.Object, opts ...cache.InformerGetOption) (cache.Informer, error) {
	inf, err := c.base.GetInformer(ctx, obj, opts...)
	if err != nil {
		return nil, err
	}
	return &scopedInformer{clusterName: c.clusterName, Informer: inf}, nil
}

// GetInformerForKind returns an informer for the given GroupVersionKind.
func (c *scopedCache) GetInformerForKind(ctx context.Context, gvk schema.GroupVersionKind, opts ...cache.InformerGetOption) (cache.Informer, error) {
	inf, err := c.base.GetInformerForKind(ctx, gvk, opts...)
	if err != nil {
		return nil, err
	}
	return &scopedInformer{clusterName: c.clusterName, Informer: inf}, nil
}

// RemoveInformer removes an informer from the cache.
func (c *scopedCache) RemoveInformer(ctx context.Context, obj client.Object) error {
	return errors.New("informer cannot be removed from scoped cache")
}

// scopedInformer is an informer that operates on a specific namespace.
type scopedInformer struct {
	clusterName logicalcluster.Name
	cache.Informer
}

// AddEventHandler adds an event handler to the informer.
func (i *scopedInformer) AddEventHandler(handler toolscache.ResourceEventHandler) (toolscache.ResourceEventHandlerRegistration, error) {
	return i.Informer.AddEventHandler(toolscache.ResourceEventHandlerDetailedFuncs{
		AddFunc: func(obj any, isInInitialList bool) {
			if cobj := obj.(client.Object); logicalcluster.From(cobj) == i.clusterName {
				handler.OnAdd(obj, isInInitialList)
			}
		},
		UpdateFunc: func(oldObj, newObj any) {
			if cobj := newObj.(client.Object); logicalcluster.From(cobj) == i.clusterName {
				handler.OnUpdate(oldObj, newObj)
			}
		},
		DeleteFunc: func(obj any) {
			var cobj client.Object
			if tombStone, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
				cobj = tombStone.Obj.(client.Object)
			} else {
				cobj = obj.(client.Object)
			}
			if logicalcluster.From(cobj) == i.clusterName {
				handler.OnDelete(obj)
			}
		},
	})
}

// AddEventHandlerWithResyncPeriod adds an event handler to the informer with a resync period.
func (i *scopedInformer) AddEventHandlerWithResyncPeriod(handler toolscache.ResourceEventHandler, resyncPeriod time.Duration) (toolscache.ResourceEventHandlerRegistration, error) {
	return i.Informer.AddEventHandlerWithResyncPeriod(toolscache.ResourceEventHandlerDetailedFuncs{
		AddFunc: func(obj any, isInInitialList bool) {
			if cobj := obj.(client.Object); logicalcluster.From(cobj) == i.clusterName {
				handler.OnAdd(obj, isInInitialList)
			}
		},
		UpdateFunc: func(oldObj, newObj any) {
			if obj := newObj.(client.Object); logicalcluster.From(obj) == i.clusterName {
				handler.OnUpdate(oldObj, newObj)
			}
		},
		DeleteFunc: func(obj any) {
			var cobj client.Object
			if tombStone, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
				cobj = tombStone.Obj.(client.Object)
			} else {
				cobj = obj.(client.Object)
			}
			if logicalcluster.From(cobj) == i.clusterName {
				handler.OnDelete(obj)
			}
		},
	}, resyncPeriod)
}

// AddIndexers adds indexers to the informer.
func (i *scopedInformer) AddIndexers(_ toolscache.Indexers) error {
	return errors.New("AddIndexers is not supported on scoped informers")
}
