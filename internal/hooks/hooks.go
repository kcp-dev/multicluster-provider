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

package hooks

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Hooks is a collection of Hook.
type Hooks []Hook

// Hook are lifecycle hook for logical clusters managed by a provider and presented as
// apibindings via APIExport virtual workspace.
// It allows to react to addition, update and deletion of apibindings (consumers) in the provider.
// Hooks should be implemented with care to avoid blocking the main reconciliation loop.
type Hook interface {
	// OnAdd is called when a new logical cluster is added.
	OnAdd(obj client.Object)

	// OnUpdate is called when a logical cluster is updated.
	OnUpdate(oldObj, newObj client.Object)

	// OnDelete is called when a logical cluster is deleted.
	OnDelete(obj client.Object)
}

// RunOnAdd runs OnAdd on all hooks.
func (h Hooks) RunOnAdd(obj client.Object) {
	for _, hook := range h {
		hook.OnAdd(obj)
	}
}

// RunOnUpdate runs OnUpdate on all hooks.
func (h Hooks) RunOnUpdate(oldObj, newObj client.Object) {
	for _, hook := range h {
		hook.OnUpdate(oldObj, newObj)
	}
}

// RunOnDelete runs OnDelete on all hooks.
func (h Hooks) RunOnDelete(obj client.Object) {
	for _, hook := range h {
		hook.OnDelete(obj)
	}
}
