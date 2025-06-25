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
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
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
	"github.com/kcp-dev/multicluster-provider/providers/initializingworkspaces"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("InitializingWorkspaces Provider", Ordered, func() {
	const initName = "root:e2e-test-ws-type"
	const workspaceTypeName = "e2e-test-ws-type"

	var (
		ctx    context.Context
		cancel context.CancelFunc

		cli                    clusterclient.ClusterClient
		workspace1, workspace2 logicalcluster.Path
		mgr                    mcmanager.Manager
	)

	BeforeAll(func() {
		ctx, cancel = context.WithCancel(context.Background())

		var err error
		cli, err = clusterclient.New(kcpConfig, client.Options{})
		Expect(err).NotTo(HaveOccurred())

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
		err = cli.Cluster(core.RootCluster.Path()).Create(ctx, workspaceType)
		Expect(err).NotTo(HaveOccurred())

		// Wait for the WorkspaceType to be ready
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			wt := &tenancyv1alpha1.WorkspaceType{}
			err := cli.Cluster(core.RootCluster.Path()).Get(ctx, client.ObjectKey{Name: workspaceTypeName}, wt)
			if err != nil {
				return false, fmt.Sprintf("failed to get WorkspaceType: %v", err)
			}
			return len(wt.Status.VirtualWorkspaces) > 0, fmt.Sprintf("WorkspaceType not ready: %v", wt.Status)
		}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to wait for WorkspaceType to be ready")

		By("creating Workspaces with the WorkspaceType with initializers")
		_, workspace1 = envtest.NewInitializingWorkspaceFixture(GinkgoT(), cli, core.RootCluster.Path(),
			envtest.WithNamePrefix("init-ws1"),
			envtest.WithType(core.RootCluster.Path(), tenancyv1alpha1.WorkspaceTypeName("e2e-test-ws-type")))

		_, workspace2 = envtest.NewInitializingWorkspaceFixture(GinkgoT(), cli, core.RootCluster.Path(),
			envtest.WithNamePrefix("init-ws2"),
			envtest.WithType(core.RootCluster.Path(), tenancyv1alpha1.WorkspaceTypeName("e2e-test-ws-type")))
	})

	It("sees both clusters with initializers", func() {
		By("getting LogicalCluster for workspaces and their cluster names")
		lc1 := &kcpcorev1alpha1.LogicalCluster{}
		err := cli.Cluster(workspace1).Get(ctx, client.ObjectKey{Name: "cluster"}, lc1)
		Expect(err).NotTo(HaveOccurred())

		lc2 := &kcpcorev1alpha1.LogicalCluster{}
		err = cli.Cluster(workspace2).Get(ctx, client.ObjectKey{Name: "cluster"}, lc2)
		Expect(err).NotTo(HaveOccurred())
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			return slices.Contains(lc1.Status.Initializers, kcpcorev1alpha1.LogicalClusterInitializer(initName)) && slices.Contains(lc2.Status.Initializers, kcpcorev1alpha1.LogicalClusterInitializer(initName)),
				fmt.Sprintf("Initializer not set: %v", lc1.Status) + " " + fmt.Sprintf("%v", lc2.Status)
		}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to see initializers in both clusters")
	})

	Describe("with a multicluster provider and manager", func() {
		var (
			lock                sync.RWMutex
			engaged             = sets.NewString()
			p                   *initializingworkspaces.Provider
			g                   *errgroup.Group
			cancelGroup         context.CancelFunc
			configMapsCreated   = sets.NewString()
			initializersRemoved = sets.NewString()
		)

		BeforeAll(func() {
			By("creating a multicluster provider for initializing workspaces")
			var err error

			// Get the initializing workspaces virtual workspace URL
			wt := &tenancyv1alpha1.WorkspaceType{}
			err = cli.Cluster(core.RootCluster.Path()).Get(ctx, client.ObjectKey{Name: "e2e-test-ws-type"}, wt)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(wt.Status.VirtualWorkspaces)).To(BeNumerically(">", 0))

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
				Named("logicalclusters").
				For(&kcpcorev1alpha1.LogicalCluster{}).
				Complete(mcreconcile.Func(func(ctx context.Context, request mcreconcile.Request) (reconcile.Result, error) {
					By(fmt.Sprintf("reconciling LogicalCluster %s in cluster %q", request.Name, request.ClusterName))
					lock.Lock()
					defer lock.Unlock()
					engaged.Insert(request.ClusterName)
					cl, err := mgr.GetCluster(ctx, request.ClusterName)
					if err != nil {
						return reconcile.Result{}, err
					}

					lc := &kcpcorev1alpha1.LogicalCluster{}
					if err := cl.GetClient().Get(ctx, request.NamespacedName, lc); err != nil {
						return reconcile.Result{}, client.IgnoreNotFound(err)
					}

					initializer := kcpcorev1alpha1.LogicalClusterInitializer(initName)
					hasInitializer := slices.Contains(lc.Status.Initializers, initializer)

					if hasInitializer {
						cm := &corev1.ConfigMap{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "initializer-test-cm",
								Namespace: "default",
							},
							Data: map[string]string{
								"cluster": request.ClusterName,
								"test":    "value",
							},
						}

						if err := cl.GetClient().Create(ctx, cm); err == nil {
							lock.Lock()
							configMapsCreated.Insert(request.ClusterName)
							lock.Unlock()
						}

						patch := client.MergeFrom(lc.DeepCopy())
						lc.Status.Initializers = initialization.EnsureInitializerAbsent(initializer, lc.Status.Initializers)
						if err := cl.GetClient().Status().Patch(ctx, lc, patch); err == nil {
							lock.Lock()
							initializersRemoved.Insert(request.ClusterName)
							lock.Unlock()
						}
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
		It("creates ConfigMaps in both workspaces", func() {
			// Wait for ConfigMap in workspace1
			envtest.Eventually(GinkgoT(), func() (bool, string) {
				cm := &corev1.ConfigMap{}
				err := cli.Cluster(workspace1).Get(ctx,
					client.ObjectKey{Namespace: "default", Name: "initializer-test-cm"},
					cm)
				if err != nil {
					return false, fmt.Sprintf("failed to get ConfigMap in workspace1: %v", err)
				}
				return true, ""
			}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to create ConfigMap in workspace1")

			// Wait for ConfigMap in workspace2
			envtest.Eventually(GinkgoT(), func() (bool, string) {
				cm := &corev1.ConfigMap{}
				err := cli.Cluster(workspace2).Get(ctx,
					client.ObjectKey{Namespace: "default", Name: "initializer-test-cm"},
					cm)
				if err != nil {
					return false, fmt.Sprintf("failed to get ConfigMap in workspace2: %v", err)
				}
				return true, ""
			}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to create ConfigMap in workspace2")
		})

		It("has removed initializers from both workspaces", func() {
			// Verify initializer removed in workspace1
			lc1 := &kcpcorev1alpha1.LogicalCluster{}
			err := cli.Cluster(workspace1).Get(ctx, client.ObjectKey{Name: "cluster"}, lc1)
			Expect(err).NotTo(HaveOccurred())

			hasInitializer := false
			for _, init := range lc1.Status.Initializers {
				if string(init) == "root:e2e-test-ws-type" {
					hasInitializer = true
					break
				}
			}
			Expect(hasInitializer).To(BeFalse())

			// Verify initializer removed in workspace2
			lc2 := &kcpcorev1alpha1.LogicalCluster{}
			err = cli.Cluster(workspace2).Get(ctx, client.ObjectKey{Name: "cluster"}, lc2)
			Expect(err).NotTo(HaveOccurred())

			hasInitializer = false
			for _, init := range lc2.Status.Initializers {
				if string(init) == "root:e2e-test-ws-type" {
					hasInitializer = true
					break
				}
			}
			Expect(hasInitializer).To(BeFalse())
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
