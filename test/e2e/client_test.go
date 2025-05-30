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

package e2e

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kcp-dev/kcp/sdk/apis/core"

	clusterclient "github.com/kcp-dev/multicluster-provider/client"
	"github.com/kcp-dev/multicluster-provider/envtest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Cluster Client", Ordered, func() {
	It("can access the root cluster", func(ctx context.Context) {
		cli, err := clusterclient.New(kcpConfig, client.Options{})
		Expect(err).NotTo(HaveOccurred())

		ns := &corev1.Namespace{}
		err = cli.Cluster(core.RootCluster.Path()).Get(ctx, client.ObjectKey{Name: "default"}, ns)
		Expect(err).NotTo(HaveOccurred())
	})

	It("can create a workspace and access it", func(ctx context.Context) {
		cli, err := clusterclient.New(kcpConfig, client.Options{})
		Expect(err).NotTo(HaveOccurred())

		_, ws := envtest.NewWorkspaceFixture(GinkgoT(), cli, core.RootCluster.Path())

		ns := &corev1.Namespace{}
		err = cli.Cluster(ws).Get(ctx, client.ObjectKey{Name: "default"}, ns)
		Expect(err).NotTo(HaveOccurred())
	})
})
