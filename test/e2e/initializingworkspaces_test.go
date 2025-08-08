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
	"slices"
	"time"

	"golang.org/x/sync/errgroup"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/kcp-dev/kcp/sdk/apis/core"
	kcpcorev1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	"github.com/kcp-dev/kcp/sdk/apis/tenancy/initialization"
	tenancyv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"

	clusterclient "github.com/kcp-dev/multicluster-provider/client"
	"github.com/kcp-dev/multicluster-provider/envtest"
	"github.com/kcp-dev/multicluster-provider/initializingworkspaces"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("InitializingWorkspaces Provider", Ordered, func() {
	const workspaceTypeName = "e2e-test-ws-type"

	var (
		ctx    context.Context
		cancel context.CancelFunc

		initName string

		cli                           clusterclient.ClusterClient
		clusterPath, ws1Path, ws2Path logicalcluster.Path
		ws1, ws2                      *tenancyv1alpha1.Workspace
		mgr                           mcmanager.Manager
	)

	BeforeAll(func() {
		ctx, cancel = context.WithCancel(context.Background())

		var err error
		cli, err = clusterclient.New(kcpConfig, client.Options{})
		Expect(err).NotTo(HaveOccurred())

		var ws *tenancyv1alpha1.Workspace
		ws, clusterPath = envtest.NewWorkspaceFixture(GinkgoT(), cli, core.RootCluster.Path(), envtest.WithNamePrefix("initializers"))
		initName = fmt.Sprintf("%s:%s", ws.Spec.Cluster, workspaceTypeName)

		// Create test workspaces with the WorkspaceType that has our initializer
		By("creating WorkspaceType with initializer")
		workspaceType := &tenancyv1alpha1.WorkspaceType{
			ObjectMeta: metav1.ObjectMeta{
				Name: workspaceTypeName,
			},
			Spec: tenancyv1alpha1.WorkspaceTypeSpec{
				Initializer: true,
			},
		}
		err = cli.Cluster(clusterPath).Create(ctx, workspaceType)
		Expect(err).NotTo(HaveOccurred())

		// Wait for the WorkspaceType to be ready
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			wt := &tenancyv1alpha1.WorkspaceType{}
			err := cli.Cluster(clusterPath).Get(ctx, client.ObjectKey{Name: workspaceTypeName}, wt)
			if err != nil {
				return false, fmt.Sprintf("failed to get WorkspaceType: %v", err)
			}
			return len(wt.Status.VirtualWorkspaces) > 0, fmt.Sprintf("WorkspaceType not ready: %v", wt.Status)
		}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to wait for WorkspaceType to be ready")

		By("creating Workspaces with the WorkspaceType with initializers")
		ws1, ws1Path = envtest.NewInitializingWorkspaceFixture(GinkgoT(), cli, clusterPath,
			envtest.WithNamePrefix("init-ws1"),
			envtest.WithType(clusterPath, tenancyv1alpha1.WorkspaceTypeName(workspaceTypeName)))

		ws2, ws2Path = envtest.NewInitializingWorkspaceFixture(GinkgoT(), cli, clusterPath,
			envtest.WithNamePrefix("init-ws2"),
			envtest.WithType(clusterPath, tenancyv1alpha1.WorkspaceTypeName(workspaceTypeName)))
	})

	It("sees both clusters with initializers", func() {
		By("getting LogicalCluster for workspaces and their cluster names")
		lc1 := &kcpcorev1alpha1.LogicalCluster{}
		err := cli.Cluster(ws1Path).Get(ctx, client.ObjectKey{Name: "cluster"}, lc1)
		Expect(err).NotTo(HaveOccurred())

		lc2 := &kcpcorev1alpha1.LogicalCluster{}
		err = cli.Cluster(ws2Path).Get(ctx, client.ObjectKey{Name: "cluster"}, lc2)
		Expect(err).NotTo(HaveOccurred())
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			return slices.Contains(lc1.Status.Initializers, kcpcorev1alpha1.LogicalClusterInitializer(initName)) && slices.Contains(lc2.Status.Initializers, kcpcorev1alpha1.LogicalClusterInitializer(initName)),
				fmt.Sprintf("Initializer not set: %v", lc1.Status) + " " + fmt.Sprintf("%v", lc2.Status)
		}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to see initializers in both clusters")
	})

	Describe("with a multicluster provider and manager", func() {
		var (
			engaged             = sets.NewString()
			p                   *initializingworkspaces.Provider
			g                   *errgroup.Group
			cancelGroup         context.CancelFunc
			initializersRemoved = sets.NewString()
		)

		BeforeAll(func() {
			cli, err := clusterclient.New(kcpConfig, client.Options{})
			Expect(err).NotTo(HaveOccurred())
			By("creating a multicluster provider for initializing workspaces")

			// Get the initializing workspaces virtual workspace URL
			wt := &tenancyv1alpha1.WorkspaceType{}
			err = cli.Cluster(clusterPath).Get(ctx, client.ObjectKey{Name: workspaceTypeName}, wt)
			Expect(err).NotTo(HaveOccurred())
			Expect(wt.Status.VirtualWorkspaces).ToNot(BeEmpty())

			vwConfig := rest.CopyConfig(kcpConfig)
			vwConfig.Host = wt.Status.VirtualWorkspaces[0].URL

			// Create the provider with the initializer name from the WorkspaceType
			p, err = initializingworkspaces.New(vwConfig, initializingworkspaces.Options{
				InitializerName: initName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("creating a manager that uses the provider")
			mgr, err = mcmanager.New(vwConfig, p, mcmanager.Options{})
			Expect(err).NotTo(HaveOccurred())

			By("creating a reconciler for LogicalClusters")
			err = mcbuilder.ControllerManagedBy(mgr).
				Named("kcp-initializer-controller").
				For(&kcpcorev1alpha1.LogicalCluster{}).
				Complete(mcreconcile.Func(
					func(ctx context.Context, request mcreconcile.Request) (ctrl.Result, error) {
						By(fmt.Sprintf("reconciling LogicalCluster %s in cluster %q", request.Name, request.ClusterName))
						cl, err := mgr.GetCluster(ctx, request.ClusterName)
						if err != nil {
							return reconcile.Result{}, err
						}

						clusterClient := cl.GetClient()
						lc := &kcpcorev1alpha1.LogicalCluster{}
						if err := clusterClient.Get(ctx, request.NamespacedName, lc); err != nil {
							return reconcile.Result{}, fmt.Errorf("failed to get logical cluster: %w", err)
						}

						engaged.Insert(request.ClusterName)
						initializer := kcpcorev1alpha1.LogicalClusterInitializer(initName)

						if slices.Contains(lc.Status.Initializers, initializer) {
							By(fmt.Sprintf("removing initializer %q from LogicalCluster %s in cluster %q", initName, request.Name, request.ClusterName))

							patch := client.MergeFrom(lc.DeepCopy())
							lc.Status.Initializers = initialization.EnsureInitializerAbsent(initializer, lc.Status.Initializers)
							if err := clusterClient.Status().Patch(ctx, lc, patch); err != nil {
								return reconcile.Result{}, err
							}
							initializersRemoved.Insert(request.ClusterName)
						}
						return reconcile.Result{}, nil
					}))
			Expect(err).NotTo(HaveOccurred())

			By("starting the provider and manager")
			var groupContext context.Context
			groupContext, cancelGroup = context.WithCancel(ctx)
			g, groupContext = errgroup.WithContext(groupContext)
			g.Go(func() error {
				return p.Run(groupContext, mgr)
			})
			g.Go(func() error {
				return mgr.Start(groupContext)
			})
		})
		It("engages both Logical Clusters with initializers", func() {
			envtest.Eventually(GinkgoT(), func() (bool, string) {
				return engaged.Has(ws1.Spec.Cluster), fmt.Sprintf("failed to see workspace %q engaged as a cluster: %v", ws1.Spec.Cluster, engaged.List())
			}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to see workspace %q engaged as a cluster: %v", ws1.Spec.Cluster, engaged.List())

			envtest.Eventually(GinkgoT(), func() (bool, string) {
				return engaged.Has(ws2.Spec.Cluster), fmt.Sprintf("failed to see workspace %q engaged as a cluster: %v", ws2.Spec.Cluster, engaged.List())
			}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to see workspace %q engaged as a cluster: %v", ws2.Spec.Cluster, engaged.List())
		})

		It("removes initializers from the both clusters after engaging", func() {
			envtest.Eventually(GinkgoT(), func() (bool, string) {
				return initializersRemoved.Has(ws1.Spec.Cluster), fmt.Sprintf("failed to see removed initializer from %q cluster: %v", ws1.Spec.Cluster, initializersRemoved.List())
			}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to see removed initializer from %q cluster: %v", ws1.Spec.Cluster, initializersRemoved.List())

			envtest.Eventually(GinkgoT(), func() (bool, string) {
				return initializersRemoved.Has(ws2.Spec.Cluster), fmt.Sprintf("failed to see removed initializer from %q cluster: %v", ws2.Spec.Cluster, initializersRemoved.List())
			}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to see removed initializer from %q cluster: %v", ws2.Spec.Cluster, initializersRemoved.List())

			By("checking if LogicalClusters objects have no initializers left")
			var err error
			lc1 := &kcpcorev1alpha1.LogicalCluster{}
			err = cli.Cluster(ws1Path).Get(ctx, client.ObjectKey{Name: "cluster"}, lc1)
			Expect(err).NotTo(HaveOccurred())

			lc2 := &kcpcorev1alpha1.LogicalCluster{}
			err = cli.Cluster(ws2Path).Get(ctx, client.ObjectKey{Name: "cluster"}, lc2)
			Expect(err).NotTo(HaveOccurred())
			envtest.Eventually(GinkgoT(), func() (bool, string) {
				return !slices.Contains(lc1.Status.Initializers, kcpcorev1alpha1.LogicalClusterInitializer(initName)) && !slices.Contains(lc2.Status.Initializers, kcpcorev1alpha1.LogicalClusterInitializer(initName)),
					fmt.Sprintf("Initializer not set: %v", lc1.Status) + " " + fmt.Sprintf("%v", lc2.Status)
			}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to see removed initializers in both clusters")
		})

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
