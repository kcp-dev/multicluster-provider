/*
Copyright 2026 The kcp Authors.

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

	"golang.org/x/sync/errgroup"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	runtimeschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/kcp-dev/logicalcluster/v3"
	apisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	apisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	"github.com/kcp-dev/sdk/apis/core"
	tenancyv1alpha1 "github.com/kcp-dev/sdk/apis/tenancy/v1alpha1"

	"github.com/kcp-dev/multicluster-provider/apiexport"
	clusterclient "github.com/kcp-dev/multicluster-provider/client"
	"github.com/kcp-dev/multicluster-provider/envtest"
	mcpcache "github.com/kcp-dev/multicluster-provider/pkg/cache"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TODO: Once envtest supports multiple shards, spread consumer workspaces across shards to verify true cross-shard aggregation.
var _ = Describe("AggregateCache", Ordered, func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc

		cli                          clusterclient.ClusterClient
		providerPath                 logicalcluster.Path
		consumer1WS, consumer2WS     *tenancyv1alpha1.Workspace
		consumer1Path, consumer2Path logicalcluster.Path
		mgr                          mcmanager.Manager
		vwEndpoint                   string
	)

	BeforeAll(func() {
		ctx, cancel = context.WithCancel(context.Background())

		var err error
		cli, err = clusterclient.New(kcpConfig, client.Options{})
		Expect(err).NotTo(HaveOccurred())

		_, providerPath = envtest.NewWorkspaceFixture(GinkgoT(), cli, core.RootCluster.Path(), envtest.WithNamePrefix("ac-provider"))

		By(fmt.Sprintf("creating a schema in the provider workspace %q", providerPath))
		schema := &apisv1alpha1.APIResourceSchema{
			ObjectMeta: metav1.ObjectMeta{
				Name: "v20250317.widgets.example.com",
			},
			Spec: apisv1alpha1.APIResourceSchemaSpec{
				Group: "example.com",
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Kind:     "Widget",
					ListKind: "WidgetList",
					Plural:   "widgets",
					Singular: "widget",
				},
				Scope: apiextensionsv1.ClusterScoped,
				Versions: []apisv1alpha1.APIResourceVersion{{
					Name: "v1",
					Schema: runtime.RawExtension{
						Raw: []byte(`{"type":"object","properties":{"spec":{"type":"object","properties":{"color":{"type":"string"}}}}}`),
					},
					Storage: true,
					Served:  true,
				}},
			},
		}
		err = cli.Cluster(providerPath).Create(ctx, schema)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("creating an APIExport in the provider workspace %q", providerPath))
		export := &apisv1alpha2.APIExport{
			ObjectMeta: metav1.ObjectMeta{
				Name: "example.com",
			},
			Spec: apisv1alpha2.APIExportSpec{
				Resources: []apisv1alpha2.ResourceSchema{
					{Name: "widgets", Group: "example.com", Schema: schema.Name, Storage: apisv1alpha2.ResourceSchemaStorage{CRD: &apisv1alpha2.ResourceSchemaStorageCRD{}}},
				},
			},
		}
		err = cli.Cluster(providerPath).Create(ctx, export)
		Expect(err).NotTo(HaveOccurred())

		// TODO: multi-shard: spread across shards
		By("creating consumer workspaces")
		consumer1WS, consumer1Path = envtest.NewWorkspaceFixture(GinkgoT(), cli, core.RootCluster.Path(), envtest.WithNamePrefix("ac-consumer1"))
		consumer2WS, consumer2Path = envtest.NewWorkspaceFixture(GinkgoT(), cli, core.RootCluster.Path(), envtest.WithNamePrefix("ac-consumer2"))

		By(fmt.Sprintf("creating an APIBinding in consumer1 workspace %q", consumer1Path))
		binding := &apisv1alpha2.APIBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: "example.com",
			},
			Spec: apisv1alpha2.APIBindingSpec{
				Reference: apisv1alpha2.BindingReference{
					Export: &apisv1alpha2.ExportBindingReference{
						Path: providerPath.String(),
						Name: export.Name,
					},
				},
			},
		}
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			err = cli.Cluster(consumer1Path).Create(ctx, binding)
			if err != nil {
				return false, fmt.Sprintf("failed to create APIBinding: %v", err)
			}
			return true, ""
		}, wait.ForeverTestTimeout, time.Millisecond*500, "failed to create APIBinding in consumer1 workspace")

		By(fmt.Sprintf("creating an APIBinding in consumer2 workspace %q", consumer2Path))
		binding = &apisv1alpha2.APIBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: "example.com",
			},
			Spec: apisv1alpha2.APIBindingSpec{
				Reference: apisv1alpha2.BindingReference{
					Export: &apisv1alpha2.ExportBindingReference{
						Path: providerPath.String(),
						Name: export.Name,
					},
				},
			},
		}
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			err = cli.Cluster(consumer2Path).Create(ctx, binding)
			if err != nil {
				return false, fmt.Sprintf("failed to create APIBinding: %v", err)
			}
			return true, ""
		}, wait.ForeverTestTimeout, time.Millisecond*500, "failed to create APIBinding in consumer2 workspace")

		By("waiting for the APIExportEndpointSlice to have endpoints")
		endpoints := &apisv1alpha1.APIExportEndpointSlice{}
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			err := cli.Cluster(providerPath).Get(ctx, client.ObjectKey{Name: "example.com"}, endpoints)
			if err != nil {
				return false, fmt.Sprintf("failed to get APIExportEndpointSlice in %q: %v", providerPath, err)
			}
			// TODO: multi-shard: expect one endpoint per shard
			return len(endpoints.Status.APIExportEndpoints) >= 1, fmt.Sprintf(
				"expected at least 1 endpoint, got %d:\n%s", len(endpoints.Status.APIExportEndpoints), toYAML(GinkgoT(), endpoints))
		}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to see endpoints in APIExportEndpointSlice")
		vwEndpoint = endpoints.Status.APIExportEndpoints[0].URL

		By(fmt.Sprintf("waiting for APIBinding in consumer1 %q to be ready", consumer1Path))
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			current := &apisv1alpha2.APIBinding{}
			err := cli.Cluster(consumer1Path).Get(ctx, client.ObjectKey{Name: "example.com"}, current)
			if err != nil {
				return false, fmt.Sprintf("failed to get APIBinding: %v", err)
			}
			return current.Status.Phase == apisv1alpha2.APIBindingPhaseBound, fmt.Sprintf("binding not bound:\n%s", toYAML(GinkgoT(), current))
		}, wait.ForeverTestTimeout, time.Millisecond*100)

		By(fmt.Sprintf("waiting for APIBinding in consumer2 %q to be ready", consumer2Path))
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			current := &apisv1alpha2.APIBinding{}
			err := cli.Cluster(consumer2Path).Get(ctx, client.ObjectKey{Name: "example.com"}, current)
			if err != nil {
				return false, fmt.Sprintf("failed to get APIBinding: %v", err)
			}
			return current.Status.Phase == apisv1alpha2.APIBindingPhaseBound, fmt.Sprintf("binding not bound:\n%s", toYAML(GinkgoT(), current))
		}, wait.ForeverTestTimeout, time.Millisecond*100)

		By("creating a widget in consumer1")
		widget := &unstructured.Unstructured{}
		widget.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"})
		widget.SetName("red-widget")
		widget.SetLabels(map[string]string{"color": "red"})
		err = cli.Cluster(consumer1Path).Create(ctx, widget)
		Expect(err).NotTo(HaveOccurred())

		By("creating a widget in consumer2")
		widget = &unstructured.Unstructured{}
		widget.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"})
		widget.SetName("blue-widget")
		widget.SetLabels(map[string]string{"color": "blue"})
		err = cli.Cluster(consumer2Path).Create(ctx, widget)
		Expect(err).NotTo(HaveOccurred())
	})

	Describe("aggregate cache lists across clusters", func() {
		var (
			lock    sync.RWMutex
			engaged = sets.NewString()

			p              *apiexport.Provider
			aggregateCache *mcpcache.AggregateCache
			g              *errgroup.Group
			cancelGroup    context.CancelFunc
		)

		BeforeAll(func() {
			By("creating a multicluster provider")
			providerConfig := rest.CopyConfig(kcpConfig)
			providerConfig.Host += providerPath.RequestPath()
			var err error
			p, err = apiexport.New(providerConfig, "example.com", apiexport.Options{
				Scheme: scheme.Scheme,
			})
			Expect(err).NotTo(HaveOccurred())

			By("creating a manager with the provider")
			mgr, err = mcmanager.New(providerConfig, p, mcmanager.Options{})
			Expect(err).NotTo(HaveOccurred())

			By("adding an index on label 'color'")
			widget := &unstructured.Unstructured{}
			widget.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"})
			err = mgr.GetFieldIndexer().IndexField(ctx, widget, "color", func(obj client.Object) []string {
				u := obj.(*unstructured.Unstructured)
				return []string{u.GetLabels()["color"]}
			})
			Expect(err).NotTo(HaveOccurred())

			By("creating a reconciler that tracks engaged clusters")
			err = mcbuilder.ControllerManagedBy(mgr).
				Named("aggregate-cache-test").
				For(&apisv1alpha2.APIBinding{}).
				Complete(mcreconcile.Func(func(ctx context.Context, request mcreconcile.Request) (reconcile.Result, error) {
					lock.Lock()
					defer lock.Unlock()
					engaged.Insert(string(request.ClusterName))
					return reconcile.Result{}, nil
				}))
			Expect(err).NotTo(HaveOccurred())

			By("creating an AggregateCache backed by the virtual workspace wildcard cache")
			aggregateCache = mcpcache.NewAggregateCache()
			vwConfig := rest.CopyConfig(kcpConfig)
			vwConfig.Host = vwEndpoint
			wc, err := mcpcache.NewWildcardCache(vwConfig, cache.Options{
				Scheme: scheme.Scheme,
			})
			Expect(err).NotTo(HaveOccurred())

			widgetForIndex := &unstructured.Unstructured{}
			widgetForIndex.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"})
			err = wc.IndexField(ctx, widgetForIndex, "color", func(obj client.Object) []string {
				u := obj.(*unstructured.Unstructured)
				return []string{u.GetLabels()["color"]}
			})
			Expect(err).NotTo(HaveOccurred())

			// TODO: multi-shard: Add one wildcard cache per shard
			aggregateCache.AddCache("shard-root", wc)

			By("starting the manager and wildcard cache")
			var groupContext context.Context
			groupContext, cancelGroup = context.WithCancel(ctx)
			g, groupContext = errgroup.WithContext(groupContext)
			g.Go(func() error {
				return mgr.Start(groupContext)
			})
			g.Go(func() error {
				return wc.Start(groupContext)
			})

			By("waiting for wildcard cache sync")
			Expect(wc.WaitForCacheSync(groupContext)).To(BeTrue())
		})

		It("sees consumer clusters", func() {
			// TODO: multi-shard: assert that the clusters are from different shardS
			envtest.Eventually(GinkgoT(), func() (bool, string) {
				lock.RLock()
				defer lock.RUnlock()
				hasCons1 := engaged.Has(consumer1WS.Spec.Cluster)
				hasCons2 := engaged.Has(consumer2WS.Spec.Cluster)
				if hasCons1 && hasCons2 {
					return true, ""
				}
				return false, fmt.Sprintf("engaged clusters: %v, want %q and %q", engaged.List(), consumer1WS.Spec.Cluster, consumer2WS.Spec.Cluster)
			}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to see both consumer clusters")
		})

		It("can list objects from individual clusters via scoped caches", func() {
			By("listing widgets from consumer1's scoped cluster")
			envtest.Eventually(GinkgoT(), func() (bool, string) {
				consumer1Cl, err := mgr.GetCluster(ctx, multicluster.ClusterName(consumer1WS.Spec.Cluster))
				if err != nil {
					return false, fmt.Sprintf("consumer1 cluster not ready: %v", err)
				}
				l := &unstructured.UnstructuredList{}
				l.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "WidgetList"})
				err = consumer1Cl.GetCache().List(ctx, l)
				if err != nil {
					return false, fmt.Sprintf("failed to list: %v", err)
				}
				if len(l.Items) != 1 {
					return false, fmt.Sprintf("expected 1 widget in consumer1, got %d", len(l.Items))
				}
				return l.Items[0].GetName() == "red-widget", fmt.Sprintf("expected red-widget, got %s", l.Items[0].GetName())
			}, wait.ForeverTestTimeout, time.Millisecond*100)

			By("listing widgets from consumer2's scoped cluster")
			envtest.Eventually(GinkgoT(), func() (bool, string) {
				consumer2Cl, err := mgr.GetCluster(ctx, multicluster.ClusterName(consumer2WS.Spec.Cluster))
				if err != nil {
					return false, fmt.Sprintf("consumer2 cluster not ready: %v", err)
				}
				l := &unstructured.UnstructuredList{}
				l.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "WidgetList"})
				err = consumer2Cl.GetCache().List(ctx, l)
				if err != nil {
					return false, fmt.Sprintf("failed to list: %v", err)
				}
				if len(l.Items) != 1 {
					return false, fmt.Sprintf("expected 1 widget in consumer2, got %d", len(l.Items))
				}
				return l.Items[0].GetName() == "blue-widget", fmt.Sprintf("expected blue-widget, got %s", l.Items[0].GetName())
			}, wait.ForeverTestTimeout, time.Millisecond*100)
		})

		It("can list all objects across clusters via the aggregate cache", func() {
			envtest.Eventually(GinkgoT(), func() (bool, string) {
				l := &unstructured.UnstructuredList{}
				l.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "WidgetList"})
				err := aggregateCache.List(ctx, l)
				if err != nil {
					return false, fmt.Sprintf("failed to list via aggregate cache: %v", err)
				}
				if len(l.Items) < 2 {
					return false, fmt.Sprintf("expected at least 2 widgets, got %d", len(l.Items))
				}
				names := sets.NewString()
				for _, item := range l.Items {
					names.Insert(item.GetName())
				}
				if !names.Has("red-widget") || !names.Has("blue-widget") {
					return false, fmt.Sprintf("expected red-widget and blue-widget, got %v", names.List())
				}
				return true, ""
			}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to list widgets from both clusters via aggregate cache")
		})

		It("can list by indexed field across clusters via the aggregate cache", func() {
			By("listing only red widgets")
			envtest.Eventually(GinkgoT(), func() (bool, string) {
				l := &unstructured.UnstructuredList{}
				l.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "WidgetList"})
				err := aggregateCache.List(ctx, l, client.MatchingFields{"color": "red"})
				if err != nil {
					return false, fmt.Sprintf("failed to list via aggregate cache with field selector: %v", err)
				}
				if len(l.Items) != 1 {
					return false, fmt.Sprintf("expected 1 red widget, got %d", len(l.Items))
				}
				return l.Items[0].GetName() == "red-widget", fmt.Sprintf("expected red-widget, got %s", l.Items[0].GetName())
			}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to list red widgets via aggregate cache with field index")

			By("listing only blue widgets")
			envtest.Eventually(GinkgoT(), func() (bool, string) {
				l := &unstructured.UnstructuredList{}
				l.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "WidgetList"})
				err := aggregateCache.List(ctx, l, client.MatchingFields{"color": "blue"})
				if err != nil {
					return false, fmt.Sprintf("failed to list via aggregate cache with field selector: %v", err)
				}
				if len(l.Items) != 1 {
					return false, fmt.Sprintf("expected 1 blue widget, got %d", len(l.Items))
				}
				return l.Items[0].GetName() == "blue-widget", fmt.Sprintf("expected blue-widget, got %s", l.Items[0].GetName())
			}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to list blue widgets via aggregate cache with field index")
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
