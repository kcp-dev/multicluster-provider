/*
Copyright 2025 The kcp Authors.

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
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
)

type cancellableProvider struct {
	provider *Provider
	cancel   context.CancelFunc
}

// Providers manages [Provider].
type Providers struct {
	lock      sync.RWMutex
	providers map[string]*cancellableProvider
}

// NewProviders returns an initialized Providers.
func NewProviders() *Providers {
	return &Providers{
		providers: map[string]*cancellableProvider{},
	}
}

// Get returns the [Provider] with the given name.
// If there is no [Provider] with that name nil is returned.
func (p *Providers) Get(name string) *Provider {
	p.lock.RLock()
	defer p.lock.RUnlock()
	cp, ok := p.providers[name]
	if !ok {
		return nil
	}
	return cp.provider
}

func (p *Providers) has(name string) bool {
	p.lock.RLock()
	defer p.lock.RUnlock()
	_, ok := p.providers[name]
	return ok
}

func (p *Providers) keys() []string {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return slices.Collect(maps.Keys(p.providers))
}

func (p *Providers) setup(ctx context.Context, name string, prov *Provider, aware multicluster.Aware) (func() error, error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	if _, ok := p.providers[name]; ok {
		return nil, fmt.Errorf("provider with name %q already exists", name)
	}

	ctx, cancel := context.WithCancel(ctx)
	cp := &cancellableProvider{
		provider: prov,
		cancel:   cancel,
	}

	if err := cp.provider.Setup(ctx, aware); err != nil {
		return nil, err
	}

	p.providers[name] = cp

	return func() error {
		err := prov.Start(ctx, aware)
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}, nil
}

func (p *Providers) stop(name string) {
	p.lock.Lock()
	defer p.lock.Unlock()

	provider, ok := p.providers[name]
	if !ok || provider == nil {
		return
	}
	provider.cancel()
	delete(p.providers, name)
}
