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
	"sync"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	apisv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	"github.com/kcp-dev/kcp/sdk/apis/core"
	tenancyv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"

	mcbuilder "github.com/multicluster-runtime/multicluster-runtime/pkg/builder"
	mcmanager "github.com/multicluster-runtime/multicluster-runtime/pkg/manager"
	mcreconcile "github.com/multicluster-runtime/multicluster-runtime/pkg/reconcile"

	clusterclient "github.com/kcp-dev/multicluster-provider/client"
	"github.com/kcp-dev/multicluster-provider/envtest"
	"github.com/kcp-dev/multicluster-provider/virtualworkspace"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("VirtualWorkspace Provider", Ordered, func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc

		cli                clusterclient.ClusterClient
		provider, consumer logicalcluster.Path
		consumerWS         *tenancyv1alpha1.Workspace
		mgr                mcmanager.Manager
		vwEndpoint         string
	)

	BeforeAll(func() {
		ctx, cancel = context.WithCancel(context.Background())

		var err error
		cli, err = clusterclient.New(kcpConfig, client.Options{})
		Expect(err).NotTo(HaveOccurred())

		_, provider = envtest.NewWorkspaceFixture(GinkgoT(), cli, core.RootCluster.Path(), envtest.WithNamePrefix("provider"))
		consumerWS, consumer = envtest.NewWorkspaceFixture(GinkgoT(), cli, core.RootCluster.Path(), envtest.WithNamePrefix("consumer"))

		By(fmt.Sprintf("creating a schema in the provider workspace %q", provider))
		schema := &apisv1alpha1.APIResourceSchema{
			ObjectMeta: metav1.ObjectMeta{
				Name: "v20250317.things.example.com",
			},
			Spec: apisv1alpha1.APIResourceSchemaSpec{
				Group: "example.com",
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Kind:     "Thing",
					ListKind: "ThingList",
					Plural:   "things",
					Singular: "thing",
				},
				Scope: apiextensionsv1.ClusterScoped,
				Versions: []apisv1alpha1.APIResourceVersion{{
					Name: "v1",
					Schema: runtime.RawExtension{
						Raw: []byte(`{"type":"object","properties":{"spec":{"type":"object","properties":{"message":{"type":"string"}}}}}`),
					},
					Storage: true,
				}},
			},
		}
		err = cli.Cluster(provider).Create(ctx, schema)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("creating an APIExport in the provider workspace %q", provider))
		export := &apisv1alpha1.APIExport{
			ObjectMeta: metav1.ObjectMeta{
				Name: "example.com",
			},
			Spec: apisv1alpha1.APIExportSpec{
				LatestResourceSchemas: []string{schema.Name},
			},
		}
		err = cli.Cluster(provider).Create(ctx, export)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("creating an APIExportEndpointSlice in the provider workspace %q", provider))
		endpoitns := &apisv1alpha1.APIExportEndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name: "example.com",
			},
			Spec: apisv1alpha1.APIExportEndpointSliceSpec{
				APIExport: apisv1alpha1.ExportBindingReference{
					Path: provider.String(),
					Name: export.Name,
				},
			},
		}
		err = cli.Cluster(provider).Create(ctx, endpoitns)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("creating an APIBinding in the consumer workspace %q", consumer))
		binding := &apisv1alpha1.APIBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: "example.com",
			},
			Spec: apisv1alpha1.APIBindingSpec{
				Reference: apisv1alpha1.BindingReference{
					Export: &apisv1alpha1.ExportBindingReference{
						Path: provider.String(),
						Name: export.Name,
					},
				},
			},
		}
		err = cli.Cluster(consumer).Create(ctx, binding)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("waiting until the APIExportEndpointSlice in the provider workspace %q to have endpoints", provider))
		endpoints := &apisv1alpha1.APIExportEndpointSlice{}
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			err := cli.Cluster(provider).Get(ctx, client.ObjectKey{Name: "example.com"}, endpoints)
			if err != nil {
				return false, fmt.Sprintf("failed to get APIExportEndpointSlice in %s: %v", provider, err)
			}
			return len(endpoints.Status.APIExportEndpoints) > 0, toYAML(GinkgoT(), endpoints)
		}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to see endpoints in APIExportEndpointSlice in %s", provider)
		vwEndpoint = endpoints.Status.APIExportEndpoints[0].URL
	})

	Describe("with a multicluster provider and manager", func() {
		var (
			lock        sync.RWMutex
			engaged     = sets.NewString()
			g           *errgroup.Group
			cancelGroup context.CancelFunc
		)

		BeforeAll(func() {
			By("creating a multicluster provider for APIBindings against the apiexport virtual workspace")
			vwConfig := rest.CopyConfig(kcpConfig)
			vwConfig.Host = vwEndpoint
			p, err := virtualworkspace.New(vwConfig, &apisv1alpha1.APIBinding{}, virtualworkspace.Options{})
			Expect(err).NotTo(HaveOccurred())

			By("creating a manager against the provider workspace")
			rootConfig := rest.CopyConfig(kcpConfig)
			rootConfig.Host += provider.RequestPath()
			mgr, err = mcmanager.New(rootConfig, p, mcmanager.Options{})
			Expect(err).NotTo(HaveOccurred())

			By("creating a reconciler for the APIBinding")
			err = mcbuilder.ControllerManagedBy(mgr).
				Named("things").
				For(&apisv1alpha1.APIBinding{}).
				Complete(mcreconcile.Func(func(ctx context.Context, request mcreconcile.Request) (reconcile.Result, error) {
					By(fmt.Sprintf("reconciling APIBinding %s in cluster %q", request.Name, request.ClusterName))
					lock.Lock()
					defer lock.Unlock()
					engaged.Insert(request.ClusterName)
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

		It("sees the consumer workspace as a cluster", func() {
			By("watching the clusters the reconciler has seen")
			envtest.Eventually(GinkgoT(), func() (bool, string) {
				lock.RLock()
				defer lock.RUnlock()
				return engaged.Has(consumerWS.Spec.Cluster), fmt.Sprintf("failed to see the consumer workspace %q as a cluster: %v", consumerWS.Spec.Cluster, engaged.List())
			}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to see the consumer workspace %q as a cluster", consumer)
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

func toYAML(t require.TestingT, x any) string {
	y, err := yaml.Marshal(x)
	require.NoError(t, err)
	return string(y)
}
