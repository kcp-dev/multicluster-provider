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

package handlers

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Handlers is a collection of Handler.
type Handlers []Handler

// Handler are lifecycle hook for logical clusters managed by a provider and presented as
// apibindings via APIExport virtual workspace.
// It allows to react to addition, update and deletion of apibindings (consumers) in the provider.
// Handlers should be implemented with care to avoid blocking the main reconciliation loop.
type Handler interface {
	// OnAdd is called when a new logical cluster is added.
	OnAdd(obj client.Object)

	// OnUpdate is called when a logical cluster is updated.
	OnUpdate(oldObj, newObj client.Object)

	// OnDelete is called when a logical cluster is deleted.
	OnDelete(obj client.Object)
}

// RunOnAdd runs OnAdd on all handlers.
func (h Handlers) RunOnAdd(obj client.Object) {
	for _, handler := range h {
		handler.OnAdd(obj)
	}
}

// RunOnUpdate runs OnUpdate on all handlers.
func (h Handlers) RunOnUpdate(oldObj, newObj client.Object) {
	for _, handler := range h {
		handler.OnUpdate(oldObj, newObj)
	}
}

// RunOnDelete runs OnDelete on all handlers.
func (h Handlers) RunOnDelete(obj client.Object) {
	for _, handler := range h {
		handler.OnDelete(obj)
	}
}
