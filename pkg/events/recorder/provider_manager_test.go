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
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	"github.com/kcp-dev/logicalcluster/v3"
)

func TestManager(t *testing.T) {
	m := NewManager(scheme.Scheme, testr.New(t))

	cfg := &rest.Config{
		// The events.k8s.io client (several layers deep) calls
		// rest.DefaultServerURL, which returns an error when the host
		// is not set.
		Host: "http://localhost:8080",
	}

	clusterName := logicalcluster.Name("cluster")

	provider, err := m.GetProvider(cfg, clusterName)
	require.NoError(t, err)
	require.NotNil(t, provider)

	deprecatedRecorder := m.GetEventRecorderFor(cfg, "test", clusterName)
	require.NotNil(t, deprecatedRecorder)
	deprecatedRecorder.Event(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "configmap", Namespace: "default"}}, corev1.EventTypeNormal, "reason", "message")

	recorder := m.GetEventRecorder(cfg, "test", clusterName)
	require.NotNil(t, recorder)
	recorder.Eventf(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "configmap", Namespace: "default"}}, nil, corev1.EventTypeNormal, "reason", "message", "note")

	require.Len(t, m.providers, 1)

	m.StopProvider(t.Context(), clusterName)
	require.Empty(t, m.providers)
}
