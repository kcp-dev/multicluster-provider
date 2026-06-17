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

package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	eventsv1 "k8s.io/api/events/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	apisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	apisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	corev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
	tenancyv1alpha1 "github.com/kcp-dev/sdk/apis/tenancy/v1alpha1"
	topologyv1alpha1 "github.com/kcp-dev/sdk/apis/topology/v1alpha1"

	"github.com/kcp-dev/multicluster-provider/envtest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	env       *envtest.Sharded
	kcpConfig *rest.Config
)

func init() {
	runtime.Must(apisv1alpha1.AddToScheme(scheme.Scheme))
	runtime.Must(apisv1alpha2.AddToScheme(scheme.Scheme))
	runtime.Must(corev1alpha1.AddToScheme(scheme.Scheme))
	runtime.Must(tenancyv1alpha1.AddToScheme(scheme.Scheme))
	runtime.Must(topologyv1alpha1.AddToScheme(scheme.Scheme))
	runtime.Must(eventsv1.AddToScheme(scheme.Scheme))
}

func TestE2e(t *testing.T) {
	RegisterFailHandler(Fail)

	env = &envtest.Sharded{}
	require.NoError(t, env.Start(t.Context()), "failed to start sharded envtest environment")
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Errorf("envtest stop: %v", err)
		}
	})

	kcpConfig = env.Config()

	RunSpecs(t, "Provider Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	// Prevent the metrics listener being created
	metricsserver.DefaultBindAddress = "0"
})

var _ = AfterSuite(func() {
	// Put the DefaultBindAddress back
	metricsserver.DefaultBindAddress = ":8080"
})
