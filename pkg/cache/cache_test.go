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

package cache

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

func TestScopedIndexInformerEventHandler(t *testing.T) {
	cs := fake.NewClientset()
	informers := informers.NewSharedInformerFactory(cs, 0)
	configMapInformer := informers.Core().V1().ConfigMaps().Informer()

	// set up scoped informer derived from fake client.
	fooConfigMaps := make([]*corev1.ConfigMap, 0)
	fooConfigMapsLock := sync.RWMutex{}
	fooScopedInformer := &scopedInformer{informer: configMapInformer, clusterName: "foo"}
	_, err := fooScopedInformer.AddEventHandlerWithOptions(&cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			cm := obj.(*corev1.ConfigMap)
			fooConfigMapsLock.Lock()
			defer fooConfigMapsLock.Unlock()
			fooConfigMaps = append(fooConfigMaps, cm)
		},
	}, cache.HandlerOptions{})
	require.NoError(t, err, "failed to add event handler to 'foo' scopedInformer")

	barConfigMaps := make([]*corev1.ConfigMap, 0)
	barConfigMapsLock := sync.RWMutex{}
	barScopedInformer := &scopedInformer{informer: configMapInformer, clusterName: "bar"}
	_, err = barScopedInformer.AddEventHandlerWithOptions(&cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			cm := obj.(*corev1.ConfigMap)
			barConfigMapsLock.Lock()
			defer barConfigMapsLock.Unlock()
			barConfigMaps = append(barConfigMaps, cm)
		},
	}, cache.HandlerOptions{})
	require.NoError(t, err, "failed to add event handler to 'bar' scopedInformer")

	// Make sure informers are started.
	informers.Start(t.Context().Done())
	require.True(t, cache.WaitForCacheSync(t.Context().Done(), configMapInformer.HasSynced), "cache failed to sync")

	// Create my-configmap in "foo" cluster.
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "my-configmap", Annotations: map[string]string{"kcp.io/cluster": "foo"}}}
	_, err = cs.CoreV1().ConfigMaps("default").Create(t.Context(), cm, metav1.CreateOptions{})
	require.NoError(t, err, "failed to create 'my-configmap'")

	// Create not-my-configmap in "bar" cluster.
	cm = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "not-my-configmap", Annotations: map[string]string{"kcp.io/cluster": "bar"}}}
	_, err = cs.CoreV1().ConfigMaps("default").Create(t.Context(), cm, metav1.CreateOptions{})
	require.NoError(t, err, "failed to create 'not-my-configmap")

	// Wait for events to be collected into slices.
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		fooConfigMapsLock.RLock()
		defer fooConfigMapsLock.RUnlock()
		require.Len(collect, fooConfigMaps, 1)
		require.Equal(collect, "my-configmap", fooConfigMaps[0].Name)

		barConfigMapsLock.RLock()
		defer barConfigMapsLock.RUnlock()
		require.Len(collect, barConfigMaps, 1)
		require.Equal(collect, "not-my-configmap", barConfigMaps[0].Name)
	}, wait.ForeverTestTimeout, time.Second)
}
