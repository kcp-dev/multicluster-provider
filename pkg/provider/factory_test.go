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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	k8scache "k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache/informertest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"

	"sigs.k8s.io/multicluster-runtime/pkg/clusters"

	"github.com/kcp-dev/logicalcluster/v3"

	mcpcache "github.com/kcp-dev/multicluster-provider/pkg/cache"
	mcrecorder "github.com/kcp-dev/multicluster-provider/pkg/events/recorder"
)

type mockCache struct {
	cache.Cache
	fakeInformers informertest.FakeInformers
}

func (mockCache *mockCache) GetInformer(ctx context.Context, obj client.Object, opts ...cache.InformerGetOption) (cache.Informer, error) {
	return mockCache.fakeInformers.FakeInformerFor(ctx, obj)
}

func (mockCache *mockCache) Start(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

type mockAware struct{}

func (m *mockAware) AddCluster(ctx context.Context, name string) error {
	return nil
}

func (m *mockAware) RemoveCluster(name string) {}

func (m *mockAware) Engage(ctx context.Context, name string, clstr cluster.Cluster) error {
	return nil
}

type mockWildcardCache struct {
	mcpcache.WildcardCache
	fakeInformers informertest.FakeInformers
}

func (mockWildcard *mockWildcardCache) GetInformer(ctx context.Context, obj client.Object, _ ...cache.InformerGetOption) (cache.Informer, error) {
	return mockWildcard.fakeInformers.FakeInformerFor(ctx, obj)
}

func (mockWildcard *mockWildcardCache) GetSharedInformer(obj runtime.Object) (k8scache.SharedIndexInformer, schema.GroupVersionKind, apimeta.RESTScopeName, error) {
	// The provider passes in a client.Object so that is asserted here.
	clientObject := obj.(client.Object)
	inf, err := mockWildcard.GetInformer(context.TODO(), clientObject)
	// The test only cares about the informer and discards the other
	// values. If this is needed in the future this needs to be
	// fleshed out.
	return inf.(k8scache.SharedIndexInformer), schema.GroupVersionKind{}, apimeta.RESTScopeName(""), err
}

func (mockWildcard *mockWildcardCache) Start(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func mockNewClusterFunc() NewClusterFunc {
	return func(cfg *rest.Config, clusterName logicalcluster.Name, wildcardCache mcpcache.WildcardCache, scheme *runtime.Scheme, recorderProvider mcrecorder.EventRecorderGetter) (*mcpcache.ScopedCluster, error) {
		return nil, nil
	}
}

func mockFactory(t *testing.T, outer, inner client.Object) *Factory {
	factory := &Factory{
		Clusters:  ptr.To(clusters.New[cluster.Cluster]()),
		Providers: NewProviders(),
		Log:       testr.New(t),
		GetVWs: func(obj client.Object) ([]string, error) {
			return []string{}, nil
		},
		Config:        &rest.Config{},
		Scheme:        scheme.Scheme,
		Outer:         outer,
		Inner:         inner,
		Cache:         &mockCache{},
		WildcardCache: &mockWildcardCache{},
		NewCluster:    mockNewClusterFunc(),
	}
	return factory
}

func TestFactory(t *testing.T) {
	factory := mockFactory(t, &appsv1.Deployment{}, &corev1.Pod{})

	vwURLs := []string{}
	factory.GetVWs = func(obj client.Object) ([]string, error) {
		return vwURLs, nil
	}
	validateProviderURLs := func() {
		expectedHashes := make(map[string]struct{}, len(vwURLs))
		for _, url := range vwURLs {
			expectedHashes[hashString(url)] = struct{}{}
		}
		assert.Len(t, expectedHashes, len(vwURLs))

		for _, id := range factory.Providers.keys() {
			assert.Contains(t, expectedHashes, id)
		}
	}

	aware := &mockAware{}

	t.Logf("Start with no VWs")
	factory.update(t.Context(), aware, &appsv1.Deployment{})

	require.Empty(t, factory.Providers.keys())
	validateProviderURLs()

	t.Logf("Add two URLs")
	vwURLs = []string{
		"https://kcp.example.com/services/apiexport/default/export-1",
		"https://kcp.example.com/services/apiexport/default/export-2",
	}
	factory.update(t.Context(), aware, &appsv1.Deployment{})
	assert.Len(t, factory.Providers.keys(), 2)
	validateProviderURLs()

	t.Logf("Add one more URL")
	vwURLs = []string{
		"https://kcp.example.com/services/apiexport/default/export-1",
		"https://kcp.example.com/services/apiexport/default/export-2",
		"https://kcp.example.com/services/apiexport/default/export-3",
	}
	factory.update(t.Context(), aware, &appsv1.Deployment{})
	assert.Len(t, factory.Providers.keys(), 3)
	validateProviderURLs()

	t.Logf("Remove all URLs")
	vwURLs = []string{}
	factory.update(t.Context(), aware, &appsv1.Deployment{})
	assert.Empty(t, factory.Providers.keys())
	validateProviderURLs()
}
