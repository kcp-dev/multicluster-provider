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

package paths

import (
	"sync"

	"github.com/kcp-dev/logicalcluster/v3"
)

// New creates a new path Store.
func New() *Store {
	return &Store{
		lock:  sync.RWMutex{},
		paths: make(map[string]logicalcluster.Name),
	}
}

// Store is a thread-safe store that maps logical cluster paths to cluster names.
type Store struct {
	lock  sync.RWMutex
	paths map[string]logicalcluster.Name
}

// Add adds a mapping from path to cluster name.
func (p *Store) Add(path string, cluster logicalcluster.Name) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.paths[path] = cluster
}

// Remove removes a mapping from path to cluster name.
func (p *Store) Remove(path string) {
	p.lock.Lock()
	defer p.lock.Unlock()
	delete(p.paths, path)
}

// Get returns the cluster name for the given path.
func (p *Store) Get(path string) (logicalcluster.Name, bool) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	cluster, exists := p.paths[path]
	return cluster, exists
}
