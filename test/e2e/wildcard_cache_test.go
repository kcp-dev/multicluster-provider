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
	"context"
	"fmt"
	"time"

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

	"github.com/kcp-dev/logicalcluster/v3"
	apisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	apisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	"github.com/kcp-dev/sdk/apis/core"

	clusterclient "github.com/kcp-dev/multicluster-provider/client"
	"github.com/kcp-dev/multicluster-provider/envtest"
	mcpcache "github.com/kcp-dev/multicluster-provider/pkg/cache"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("WildcardCache", Ordered, func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc

		cli                       clusterclient.ClusterClient
		provider, consumer, other logicalcluster.Path
		vwEndpoint                string
		wc                        mcpcache.WildcardCache
	)

	BeforeAll(func() {
		ctx, cancel = context.WithCancel(context.Background())

		var err error
		cli, err = clusterclient.New(kcpConfig, client.Options{})
		Expect(err).NotTo(HaveOccurred())

		_, provider = envtest.NewWorkspaceFixture(GinkgoT(), cli, core.RootCluster.Path(), envtest.WithNamePrefix("wc-provider"))
		_, consumer = envtest.NewWorkspaceFixture(GinkgoT(), cli, core.RootCluster.Path(), envtest.WithNamePrefix("wc-consumer"), envtest.WithRootShard())
		_, other = envtest.NewWorkspaceFixture(GinkgoT(), cli, core.RootCluster.Path(), envtest.WithNamePrefix("wc-other"), envtest.WithRootShard())

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
					Served:  true,
				}},
			},
		}
		err = cli.Cluster(provider).Create(ctx, schema)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("creating an APIExport in the provider workspace %q", provider))
		export := &apisv1alpha2.APIExport{
			ObjectMeta: metav1.ObjectMeta{
				Name: "example.com",
			},
			Spec: apisv1alpha2.APIExportSpec{
				Resources: []apisv1alpha2.ResourceSchema{
					{Name: "things", Group: "example.com", Schema: schema.Name, Storage: apisv1alpha2.ResourceSchemaStorage{CRD: &apisv1alpha2.ResourceSchemaStorageCRD{}}},
				},
			},
		}
		err = cli.Cluster(provider).Create(ctx, export)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("creating an APIBinding in the consumer workspace %q", consumer))
		binding := &apisv1alpha2.APIBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: "example.com",
			},
			Spec: apisv1alpha2.APIBindingSpec{
				Reference: apisv1alpha2.BindingReference{
					Export: &apisv1alpha2.ExportBindingReference{
						Path: provider.String(),
						Name: export.Name,
					},
				},
			},
		}
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			err = cli.Cluster(consumer).Create(ctx, binding)
			if err != nil {
				return false, fmt.Sprintf("failed to create APIBinding: %v", err)
			}
			return true, ""
		}, wait.ForeverTestTimeout, time.Millisecond*500, "failed to create APIBinding in consumer workspace")

		By(fmt.Sprintf("creating an APIBinding in the other workspace %q", other))
		binding = &apisv1alpha2.APIBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: "example.com",
			},
			Spec: apisv1alpha2.APIBindingSpec{
				Reference: apisv1alpha2.BindingReference{
					Export: &apisv1alpha2.ExportBindingReference{
						Path: provider.String(),
						Name: export.Name,
					},
				},
			},
		}
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			err = cli.Cluster(other).Create(ctx, binding)
			if err != nil {
				return false, fmt.Sprintf("failed to create APIBinding: %v", err)
			}
			return true, ""
		}, wait.ForeverTestTimeout, time.Millisecond*500, "failed to create APIBinding in other workspace")

		By("waiting for the APIExportEndpointSlice to have endpoints")
		endpoints := &apisv1alpha1.APIExportEndpointSlice{}
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			err := cli.Cluster(provider).Get(ctx, client.ObjectKey{Name: "example.com"}, endpoints)
			if err != nil {
				return false, fmt.Sprintf("failed to get APIExportEndpointSlice in %q: %v", provider, err)
			}
			return len(endpoints.Status.APIExportEndpoints) > 0, toYAML(GinkgoT(), endpoints)
		}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to see endpoints in APIExportEndpointSlice in %q", provider)
		vwEndpoint = endpoints.Status.APIExportEndpoints[0].URL

		By(fmt.Sprintf("waiting for APIBinding in consumer %q to be ready", consumer))
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			current := &apisv1alpha2.APIBinding{}
			err := cli.Cluster(consumer).Get(ctx, client.ObjectKey{Name: "example.com"}, current)
			if err != nil {
				return false, fmt.Sprintf("failed to get APIBinding: %v", err)
			}
			return current.Status.Phase == apisv1alpha2.APIBindingPhaseBound, fmt.Sprintf("binding not bound:\n%s", toYAML(GinkgoT(), current))
		}, wait.ForeverTestTimeout, time.Millisecond*100)

		By(fmt.Sprintf("waiting for APIBinding in other %q to be ready", other))
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			current := &apisv1alpha2.APIBinding{}
			err := cli.Cluster(other).Get(ctx, client.ObjectKey{Name: "example.com"}, current)
			if err != nil {
				return false, fmt.Sprintf("failed to get APIBinding: %v", err)
			}
			return current.Status.Phase == apisv1alpha2.APIBindingPhaseBound, fmt.Sprintf("binding not bound:\n%s", toYAML(GinkgoT(), current))
		}, wait.ForeverTestTimeout, time.Millisecond*100)

		By("creating a stone in the consumer workspace")
		thing := &unstructured.Unstructured{}
		thing.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Thing"})
		thing.SetName("stone")
		thing.SetLabels(map[string]string{"color": "gray"})
		err = cli.Cluster(consumer).Create(ctx, thing)
		Expect(err).NotTo(HaveOccurred())

		By("creating a box in the other workspace")
		thing = &unstructured.Unstructured{}
		thing.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Thing"})
		thing.SetName("box")
		thing.SetLabels(map[string]string{"color": "white"})
		err = cli.Cluster(other).Create(ctx, thing)
		Expect(err).NotTo(HaveOccurred())

		By("creating a wildcard cache against the virtual workspace endpoint")
		vwConfig := rest.CopyConfig(kcpConfig)
		vwConfig.Host = vwEndpoint
		wc, err = mcpcache.NewWildcardCache(vwConfig, cache.Options{
			Scheme: scheme.Scheme,
		})
		Expect(err).NotTo(HaveOccurred())

		By("adding a field index on label 'color' to the wildcard cache")
		indexThing := &unstructured.Unstructured{}
		indexThing.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Thing"})
		err = wc.IndexField(ctx, indexThing, "color", func(obj client.Object) []string {
			u := obj.(*unstructured.Unstructured)
			return []string{u.GetLabels()["color"]}
		})
		Expect(err).NotTo(HaveOccurred())

		By("starting the wildcard cache")
		go func() {
			defer GinkgoRecover()
			err := wc.Start(ctx)
			Expect(err).NotTo(HaveOccurred())
		}()
		Expect(wc.WaitForCacheSync(ctx)).To(BeTrue())
	})

	It("lists things across all clusters", func() {
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			l := &unstructured.UnstructuredList{}
			l.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "ThingList"})
			err := wc.List(ctx, l)
			if err != nil {
				return false, fmt.Sprintf("failed to list things: %v", err)
			}
			if len(l.Items) < 2 {
				return false, fmt.Sprintf("expected at least 2 items (stone+box), got %d\n\n%s", len(l.Items), toYAML(GinkgoT(), l.Object))
			}
			names := sets.NewString()
			for _, item := range l.Items {
				names.Insert(item.GetName())
			}
			if !names.HasAll("stone", "box") {
				return false, fmt.Sprintf("expected to find 'stone' and 'box', got %v", names.List())
			}
			return true, ""
		}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to list things across all clusters")
	})

	It("lists things by field index across all clusters", func() {
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			l := &unstructured.UnstructuredList{}
			l.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "ThingList"})
			err := wc.List(ctx, l, client.MatchingFields{"color": "gray"})
			if err != nil {
				return false, fmt.Sprintf("failed to list things by color: %v", err)
			}
			if len(l.Items) != 1 {
				return false, fmt.Sprintf("expected 1 gray item, got %d\n\n%s", len(l.Items), toYAML(GinkgoT(), l.Object))
			}
			if l.Items[0].GetName() != "stone" {
				return false, fmt.Sprintf("expected 'stone', got %q", l.Items[0].GetName())
			}
			return true, ""
		}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to list things by field index across clusters")
	})

	It("lists things by field index for a different color across clusters", func() {
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			l := &unstructured.UnstructuredList{}
			l.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "ThingList"})
			err := wc.List(ctx, l, client.MatchingFields{"color": "white"})
			if err != nil {
				return false, fmt.Sprintf("failed to list things by color: %v", err)
			}
			if len(l.Items) != 1 {
				return false, fmt.Sprintf("expected 1 white item, got %d\n\n%s", len(l.Items), toYAML(GinkgoT(), l.Object))
			}
			if l.Items[0].GetName() != "box" {
				return false, fmt.Sprintf("expected 'box', got %q", l.Items[0].GetName())
			}
			return true, ""
		}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to list things by field index for 'white' across clusters")
	})

	AfterAll(func() {
		cancel()
	})
})
