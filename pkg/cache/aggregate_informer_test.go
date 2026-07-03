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

package cache

import (
	"testing"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
)

func TestGetAggregateInformer(t *testing.T) {
	tests := []struct {
		name     string
		obj1     runtime.Object
		obj2     runtime.Object
		wantSame bool
		wantErr  bool
	}{
		{
			name:     "same typed object returns same instance",
			obj1:     &corev1.Pod{},
			obj2:     &corev1.Pod{},
			wantSame: true,
		},
		{
			name:     "different typed objects return different instances",
			obj1:     &corev1.Pod{},
			obj2:     &corev1.ConfigMap{},
			wantSame: false,
		},
		{
			name: "unstructured with different GVKs return different instances",
			obj1: func() runtime.Object {
				u := &unstructured.Unstructured{}
				u.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"})
				return u
			}(),
			obj2: func() runtime.Object {
				u := &unstructured.Unstructured{}
				u.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"})
				return u
			}(),
			wantSame: false,
		},
		{
			name: "unstructured with same GVK returns same instance",
			obj1: func() runtime.Object {
				u := &unstructured.Unstructured{}
				u.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"})
				return u
			}(),
			obj2: func() runtime.Object {
				u := &unstructured.Unstructured{}
				u.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"})
				return u
			}(),
			wantSame: true,
		},
		{
			name:    "unstructured without GVK returns error",
			obj1:    &unstructured.Unstructured{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ac := NewAggregateCache(scheme.Scheme)

			inf1, err := ac.GetAggregateInformer(tt.obj1)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			inf2, err := ac.GetAggregateInformer(tt.obj2)
			require.NoError(t, err)

			if tt.wantSame {
				require.Same(t, inf1, inf2, "expected same informer instance")
			} else {
				require.NotSame(t, inf1, inf2, "expected different informer instances")
			}
		})
	}
}
