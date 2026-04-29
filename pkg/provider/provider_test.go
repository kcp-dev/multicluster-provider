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
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"

	"sigs.k8s.io/multicluster-runtime/pkg/clusters"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	mcpcache "github.com/kcp-dev/multicluster-provider/pkg/cache"
)

type mockAware struct{}

func (m *mockAware) Engage(ctx context.Context, name multicluster.ClusterName, clstr cluster.Cluster) error {
	return nil
}

func testProvider(t *testing.T, extractURLs func(client.Object) ([]string, error)) *Provider {
	t.Helper()
	logger := testr.New(t)

	p := &Provider{
		opts: Options{
			Scheme:                       scheme.Scheme,
			ExtractURLsFromEndpointSlice: extractURLs,
			Log:                          &logger,
		},
		Clusters:         clusters.New[cluster.Cluster](),
		watchedEndpoints: map[string]*watchedEndpoint{},
		aggregateCache:   mcpcache.NewAggregateCache(),
	}

	p.watchEndpointFunc = func(ctx context.Context, url string, aware multicluster.Aware) (*watchedEndpoint, error) {
		_, cancel := context.WithCancel(ctx)
		return &watchedEndpoint{
			cancel:   cancel,
			provider: p,
		}, nil
	}

	return p
}

func TestEndpointSliceUpdate(t *testing.T) {
	var vwURLs []string
	extractURLs := func(_ client.Object) ([]string, error) {
		return vwURLs, nil
	}

	p := testProvider(t, extractURLs)
	aware := &mockAware{}
	obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test"}}

	t.Log("Start with no VWs")
	p.endpointSliceUpdate(t.Context(), aware, obj)
	require.Empty(t, p.watchedEndpoints)

	t.Log("Add two URLs")
	vwURLs = []string{
		"https://kcp.example.com/services/apiexport/default/export-1",
		"https://kcp.example.com/services/apiexport/default/export-2",
	}
	p.endpointSliceUpdate(t.Context(), aware, obj)
	assert.Len(t, p.watchedEndpoints, 2)
	assert.Contains(t, p.watchedEndpoints, vwURLs[0])
	assert.Contains(t, p.watchedEndpoints, vwURLs[1])

	t.Log("Add one more URL")
	vwURLs = []string{
		"https://kcp.example.com/services/apiexport/default/export-1",
		"https://kcp.example.com/services/apiexport/default/export-2",
		"https://kcp.example.com/services/apiexport/default/export-3",
	}
	p.endpointSliceUpdate(t.Context(), aware, obj)
	assert.Len(t, p.watchedEndpoints, 3)
	assert.Contains(t, p.watchedEndpoints, vwURLs[2])

	t.Log("Remove all URLs — endpoints are cancelled and removed")
	vwURLs = []string{}
	p.endpointSliceUpdate(t.Context(), aware, obj)
	assert.Empty(t, p.watchedEndpoints)

	t.Log("Re-add a previously removed URL — should be re-watched")
	vwURLs = []string{
		"https://kcp.example.com/services/apiexport/default/export-1",
	}
	p.endpointSliceUpdate(t.Context(), aware, obj)
	assert.Len(t, p.watchedEndpoints, 1)
	assert.Contains(t, p.watchedEndpoints, vwURLs[0])
}

func TestEndpointSliceUpdate_IdempotentAdd(t *testing.T) {
	callCount := 0
	var vwURLs []string
	extractURLs := func(_ client.Object) ([]string, error) {
		return vwURLs, nil
	}

	p := testProvider(t, extractURLs)
	p.watchEndpointFunc = func(ctx context.Context, url string, aware multicluster.Aware) (*watchedEndpoint, error) {
		callCount++
		_, cancel := context.WithCancel(ctx)
		return &watchedEndpoint{cancel: cancel, provider: p}, nil
	}

	aware := &mockAware{}
	obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test"}}

	vwURLs = []string{"https://kcp.example.com/endpoint-1"}
	p.endpointSliceUpdate(t.Context(), aware, obj)
	assert.Equal(t, 1, callCount)

	// Same URL again — should not call watchEndpointFunc a second time.
	p.endpointSliceUpdate(t.Context(), aware, obj)
	assert.Equal(t, 1, callCount)
}
