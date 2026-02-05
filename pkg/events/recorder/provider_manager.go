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

package recorder

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/runtime"
	eventsv1client "k8s.io/client-go/kubernetes/typed/events/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/tools/record"

	"github.com/kcp-dev/logicalcluster/v3"
)

// EventRecorderGetter is an interface for the Provider.
type EventRecorderGetter interface {
	GetEventRecorderFor(string) record.EventRecorder
	GetEventRecorder(string) events.EventRecorder
}

// Manager manages Providers.
type Manager struct {
	scheme *runtime.Scheme
	logger logr.Logger

	lock      sync.RWMutex
	providers map[logicalcluster.Name]*Provider
}

// NewManager sets up a new Manager.
func NewManager(scheme *runtime.Scheme, logger logr.Logger) *Manager {
	return &Manager{
		scheme:    scheme,
		logger:    logger,
		providers: map[logicalcluster.Name]*Provider{},
	}
}

// awareEventBroadcasterProducer returns a function that matches the upstream EventBroadcasterProducer.
func awareEventBroadcasterProducer(cfg *rest.Config, httpClient *http.Client) (EventBroadcasterProducer, error) {
	evtClient, err := eventsv1client.NewForConfigAndClient(cfg, httpClient)
	if err != nil {
		return nil, err
	}

	return func() (record.EventBroadcaster, events.EventBroadcaster, bool) {
		return record.NewBroadcaster(), events.NewBroadcaster(&events.EventSinkImpl{Interface: evtClient}), true
	}, nil
}

// GetProvider returns a Provider for the specified cluster.
func (m *Manager) GetProvider(unawareConfig *rest.Config, clusterName logicalcluster.Name) (*Provider, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	if provider, ok := m.providers[clusterName]; ok {
		return provider, nil
	}

	restConfig := rest.CopyConfig(unawareConfig)
	restConfig.Host += "/clusters/" + clusterName.String()

	httpClient, err := rest.HTTPClientFor(restConfig)
	if err != nil {
		return nil, fmt.Errorf("error constructing cluster-scoped http client: %w", err)
	}

	makeBroadcaster, err := awareEventBroadcasterProducer(restConfig, httpClient)
	if err != nil {
		return nil, fmt.Errorf("error constructing aware EventBroadcasterProducer: %w", err)
	}

	provider, err := NewProvider(restConfig, httpClient, m.scheme, m.logger, makeBroadcaster)
	if err != nil {
		return nil, fmt.Errorf("error constructing event recorder for %q: %w", clusterName, err)
	}

	m.providers[clusterName] = provider
	return provider, nil
}

// GetEventRecorderFor returns an event recorder that broadcasts to this provider's
// broadcaster.  All events will be associated with a component of the given name.
func (m *Manager) GetEventRecorderFor(cfg *rest.Config, name, clusterName logicalcluster.Name) record.EventRecorder {
	provider, err := m.GetProvider(cfg, clusterName)
	if err != nil {
		m.logger.Error(err, "error getting event recorder", "clusterName", clusterName)
		return nil
	}
	return provider.GetEventRecorderFor(name.String())
}

// GetEventRecorder returns an event recorder that broadcasts to this provider's
// broadcaster.  All events will be associated with a component of the given name.
func (m *Manager) GetEventRecorder(cfg *rest.Config, name, clusterName logicalcluster.Name) events.EventRecorder {
	provider, err := m.GetProvider(cfg, clusterName)
	if err != nil {
		m.logger.Error(err, "error getting event recorder", "clusterName", clusterName)
		return nil
	}
	return provider.GetEventRecorder(name.String())
}

// Stop stops all providers.
func (m *Manager) Stop(ctx context.Context) {
	m.lock.Lock()
	defer m.lock.Unlock()
	for _, provider := range m.providers {
		provider.Stop(ctx)
	}
	m.providers = map[logicalcluster.Name]*Provider{}
}

// StopProvider stops the Provider for the specified cluster.
func (m *Manager) StopProvider(ctx context.Context, clusterName logicalcluster.Name) {
	m.lock.Lock()
	defer m.lock.Unlock()
	provider, ok := m.providers[clusterName]
	if !ok {
		return
	}
	provider.Stop(ctx)
	delete(m.providers, clusterName)
}
