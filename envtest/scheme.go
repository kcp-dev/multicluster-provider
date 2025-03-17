/*
Copyright 2016 The KCP Authors.

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

package envtest

import (
	"k8s.io/client-go/kubernetes/scheme"

	apisv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	corev1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	tenancyv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	topologyv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/topology/v1alpha1"
)

func init() {
	if err := apisv1alpha1.AddToScheme(scheme.Scheme); err != nil {
		log.Info("WARNING: failed to add apis.kcp.io/v1alpha1 to scheme", "error", err)
	}
	if err := corev1alpha1.AddToScheme(scheme.Scheme); err != nil {
		log.Info("WARNING: failed to add core.kcp.io/v1alpha1 to scheme", "error", err)
	}
	if err := tenancyv1alpha1.AddToScheme(scheme.Scheme); err != nil {
		log.Info("WARNING: failed to add tenancy.kcp.io/v1alpha1 to scheme", "error", err)
	}
	if err := topologyv1alpha1.AddToScheme(scheme.Scheme); err != nil {
		log.Info("WARNING: failed to add topology.kcp.io/v1alpha1 to scheme", "error", err)
	}
}
