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
	"net/http"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
)

func TestEventBroadcasterProvider(t *testing.T) {
	makeBroadcaster := func() (record.EventBroadcaster, bool) { return record.NewBroadcaster(), true }

	provider, err := NewProvider(http.DefaultClient, scheme.Scheme, logr.Discard(), makeBroadcaster)
	require.NoError(t, err)

	recorder := provider.GetEventRecorderFor(&rest.Config{}, "test", "cluster")
	recorder.Event(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "configmap", Namespace: "default"}}, corev1.EventTypeNormal, "reason", "message")
	require.Len(t, provider.broadcasters, 1)

	err = provider.StopBroadcaster("cluster")
	require.NoError(t, err)
	require.Len(t, provider.broadcasters, 0)
}
