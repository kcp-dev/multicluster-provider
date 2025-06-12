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
	"fmt"

	"golang.org/x/sync/errgroup"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kcp-dev/kcp/sdk/apis/core"
	kcpcorev1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"

	clusterclient "github.com/kcp-dev/multicluster-provider/client"
	"github.com/kcp-dev/multicluster-provider/envtest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("InitializingWorkspaces Provider", Ordered, func() {
	const initializerName = "test:initializer"

	var (
		ctx    context.Context
		cancel context.CancelFunc

		cli                    clusterclient.ClusterClient
		workspace1, workspace2 logicalcluster.Path
		lc1, lc2               *kcpcorev1alpha1.LogicalCluster
		//mgr                        mcmanager.Manager
		//cluster1Name, cluster2Name string
	)

	BeforeAll(func() {
		ctx, cancel = context.WithCancel(context.Background())

		var err error
		cli, err = clusterclient.New(kcpConfig, client.Options{})
		Expect(err).NotTo(HaveOccurred())

		// Create test workspaces
		_, workspace1 = envtest.NewWorkspaceFixture(GinkgoT(), cli, core.RootCluster.Path(), envtest.WithNamePrefix("init-ws1"))
		_, workspace2 = envtest.NewWorkspaceFixture(GinkgoT(), cli, core.RootCluster.Path(), envtest.WithNamePrefix("init-ws2"))

		By(fmt.Sprintf("getting LogicalCluster for workspace %q", workspace1))
		lc1 = &kcpcorev1alpha1.LogicalCluster{}
		err = cli.Cluster(workspace1).Get(ctx, client.ObjectKey{Name: "cluster"}, lc1)
		Expect(err).NotTo(HaveOccurred())
		//cluster1Name = string(logicalcluster.From(lc1))

		By(fmt.Sprintf("getting LogicalCluster for workspace %q", workspace2))
		lc2 = &kcpcorev1alpha1.LogicalCluster{}
		err = cli.Cluster(workspace2).Get(ctx, client.ObjectKey{Name: "cluster"}, lc2)
		Expect(err).NotTo(HaveOccurred())
		//cluster2Name = string(logicalcluster.From(lc2))
	})

	Describe("with a multicluster provider and manager", func() {
		var (
			//lock         sync.RWMutex
			//engaged      = sets.NewString()
			//p            *initializingworkspaces.Provider
			g           *errgroup.Group
			cancelGroup context.CancelFunc
			//clusterNames = []string{}
		)

		AfterAll(func() {
			cancelGroup()
			err := g.Wait()
			Expect(err).NotTo(HaveOccurred())
		})
	})

	AfterAll(func() {
		cancel()
	})
})
