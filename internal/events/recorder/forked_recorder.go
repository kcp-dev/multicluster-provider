/*
Copyright 2018 The Kubernetes Authors.

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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
)

// EventBroadcasterProducer makes an event broadcaster, returning
// whether or not the broadcaster should be stopped with the Provider,
// or not (e.g. if it's shared, it shouldn't be stopped with the Provider).
type EventBroadcasterProducer func() (caster record.EventBroadcaster, stopWithProvider bool)

// Provider is a recorder.Provider that records events to the k8s API server
// and to a logr Logger.
type Provider struct {
	lock sync.RWMutex

	// scheme to specify when creating a recorder
	scheme     *runtime.Scheme
	baseConfig *rest.Config
	httpClient *http.Client
	// logger is the logger to use when logging diagnostic event info
	logger          logr.Logger
	makeBroadcaster EventBroadcasterProducer

	// broadcasters holds the broadcasters created for each logical cluster
	broadcasters map[string]*broadcaster
}

type broadcaster struct {
	broadcaster record.EventBroadcaster
	stop        bool
	stopped     bool
}

// NB(directxman12): this manually implements Stop instead of Being a runnable because we need to
// stop it *after* everything else shuts down, otherwise we'll cause panics as the leader election
// code finishes up and tries to continue emitting events.

// Stop attempts to stop this provider, stopping the underlying broadcaster
// if the broadcaster asked to be stopped.  It kinda tries to honor the given
// context, but the underlying broadcaster has an indefinite wait that doesn't
// return until all queued events are flushed, so this may end up just returning
// before the underlying wait has finished instead of cancelling the wait.
// This is Very Frustrating™.
func (p *Provider) Stop(shutdownCtx context.Context) {
	doneCh := make(chan struct{})

	go func() {
		// technically, this could start the broadcaster, but practically, it's
		// almost certainly already been started (e.g. by leader election).  We
		// need to invoke this to ensure that we don't inadvertently race with
		// an invocation of getBroadcaster.
		for key := range p.broadcasters {
			broadcaster, err := p.getBroadcaster(nil, key)
			if err == nil && p.broadcasters[key].stop {
				p.lock.Lock()
				broadcaster.Shutdown()
				p.broadcasters[key].stopped = true
				p.lock.Unlock()
			}
		}
		close(doneCh)
	}()

	select {
	case <-shutdownCtx.Done():
	case <-doneCh:
	}
}

// getBroadcaster ensures that a broadcaster is started for this
// provider, and returns it.  It's threadsafe.
func (p *Provider) getBroadcaster(cfg *rest.Config, clusterName string) (record.EventBroadcaster, error) {
	// NB(directxman12): this can technically still leak if something calls
	// "getBroadcaster" (i.e. Emits an Event) but never calls Start, but if we
	// create the broadcaster in start, we could race with other things that
	// are started at the same time & want to emit events.  The alternative is
	// silently swallowing events and more locking, but that seems suboptimal.
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.broadcasters == nil {
		p.broadcasters = make(map[string]*broadcaster)
	}

	if _, ok := p.broadcasters[clusterName]; !ok {
		corev1Client, err := corev1client.NewForConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to init core v1 client for event broadcaster: %w", err)
		}
		caster, stop := p.makeBroadcaster()
		caster.StartRecordingToSink(&corev1client.EventSinkImpl{Interface: corev1Client.Events("")})
		caster.StartEventWatcher(
			func(e *corev1.Event) {
				p.logger.V(1).Info(e.Message, "type", e.Type, "object", e.InvolvedObject, "reason", e.Reason, "clusterName", clusterName)
			})
		p.broadcasters[clusterName] = &broadcaster{broadcaster: caster, stop: stop}
	}

	return p.broadcasters[clusterName].broadcaster, nil
}

// StopBroadcaster shuts down an individual broadcaster for a specific logical cluster name
func (p *Provider) StopBroadcaster(clusterName string) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	if caster, ok := p.broadcasters[clusterName]; ok {
		caster.broadcaster.Shutdown()
		delete(p.broadcasters, clusterName)
	}

	return nil
}

// NewProvider create a new Provider instance.
func NewProvider(baseConfig *rest.Config, httpClient *http.Client, scheme *runtime.Scheme, logger logr.Logger, makeBroadcaster EventBroadcasterProducer) (*Provider, error) {
	if httpClient == nil {
		panic("httpClient must not be nil")
	}

	p := &Provider{scheme: scheme, logger: logger, makeBroadcaster: makeBroadcaster, baseConfig: baseConfig, httpClient: httpClient}
	return p, nil
}

// GetEventRecorderFor returns an event recorder that broadcasts to this provider's
// broadcaster.  All events will be associated with a component of the given name.
func (p *Provider) GetEventRecorderFor(cfg *rest.Config, name, clusterName string) record.EventRecorder {
	return &lazyRecorder{
		prov:        p,
		name:        name,
		clusterName: clusterName,
		cfg:         cfg,
	}
}

// lazyRecorder is a recorder that doesn't actually instantiate any underlying
// recorder until the first event is emitted.
type lazyRecorder struct {
	cfg *rest.Config

	prov        *Provider
	name        string
	clusterName string

	recOnce sync.Once
	rec     record.EventRecorder
}

// ensureRecording ensures that a concrete recorder is populated for this recorder.
func (l *lazyRecorder) ensureRecording() {
	l.recOnce.Do(func() {
		broadcaster, err := l.prov.getBroadcaster(l.cfg, l.clusterName)
		if err == nil {
			l.rec = broadcaster.NewRecorder(l.prov.scheme, corev1.EventSource{Component: l.name})
		}
	})
}

func (l *lazyRecorder) Event(object runtime.Object, eventtype, reason, message string) {
	l.ensureRecording()

	l.prov.lock.RLock()
	if !l.prov.broadcasters[l.clusterName].stopped && l.rec != nil {
		l.rec.Event(object, eventtype, reason, message)
	}
	l.prov.lock.RUnlock()
}
func (l *lazyRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...any) {
	l.ensureRecording()

	l.prov.lock.RLock()
	if !l.prov.broadcasters[l.clusterName].stopped && l.rec != nil {
		l.rec.Eventf(object, eventtype, reason, messageFmt, args...)
	}
	l.prov.lock.RUnlock()
}
func (l *lazyRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...any) {
	l.ensureRecording()

	l.prov.lock.RLock()
	if !l.prov.broadcasters[l.clusterName].stopped && l.rec != nil {
		l.rec.AnnotatedEventf(object, annotations, eventtype, reason, messageFmt, args...)
	}
	l.prov.lock.RUnlock()
}
