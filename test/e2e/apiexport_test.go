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

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	runtimeschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/kcp-dev/logicalcluster/v3"
	apisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	apisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	"github.com/kcp-dev/sdk/apis/core"
	tenancyv1alpha1 "github.com/kcp-dev/sdk/apis/tenancy/v1alpha1"

	"github.com/kcp-dev/multicluster-provider/apiexport"
	clusterclient "github.com/kcp-dev/multicluster-provider/client"
	"github.com/kcp-dev/multicluster-provider/envtest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("APIExport Provider", Ordered, func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc

		cli                       clusterclient.ClusterClient
		provider, consumer, other logicalcluster.Path
		consumerWS                *tenancyv1alpha1.Workspace
		mgr                       mcmanager.Manager
		vwEndpoint                string
	)

	BeforeAll(func() {
		ctx, cancel = context.WithCancel(context.Background())

		var err error
		cli, err = clusterclient.New(kcpConfig, client.Options{})
		Expect(err).NotTo(HaveOccurred())

		_, provider = envtest.NewWorkspaceFixture(GinkgoT(), cli, core.RootCluster.Path(), envtest.WithNamePrefix("provider"))
		consumerWS, consumer = envtest.NewWorkspaceFixture(GinkgoT(), cli, core.RootCluster.Path(), envtest.WithNamePrefix("consumer"))
		_, other = envtest.NewWorkspaceFixture(GinkgoT(), cli, core.RootCluster.Path(), envtest.WithNamePrefix("other"))

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
				PermissionClaims: []apisv1alpha2.PermissionClaim{
					{
						GroupResource: apisv1alpha2.GroupResource{
							Group:    "events.k8s.io",
							Resource: "events",
						},
						Verbs: []string{"*"},
					},
					{
						GroupResource: apisv1alpha2.GroupResource{
							Group:    "",
							Resource: "events",
						},
						Verbs: []string{"*"},
					},
				},
			},
		}
		err = cli.Cluster(provider).Create(ctx, export)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("creating an APIBinding in the other workspace %q", other))
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
				PermissionClaims: []apisv1alpha2.AcceptablePermissionClaim{
					{
						ScopedPermissionClaim: apisv1alpha2.ScopedPermissionClaim{
							PermissionClaim: apisv1alpha2.PermissionClaim{
								GroupResource: apisv1alpha2.GroupResource{
									Group:    "events.k8s.io",
									Resource: "events",
								},
								Verbs: []string{"*"},
							},
							Selector: apisv1alpha2.PermissionClaimSelector{
								MatchAll: true,
							},
						},
						State: apisv1alpha2.ClaimAccepted,
					},
					{
						ScopedPermissionClaim: apisv1alpha2.ScopedPermissionClaim{
							PermissionClaim: apisv1alpha2.PermissionClaim{
								GroupResource: apisv1alpha2.GroupResource{
									Group:    "",
									Resource: "events",
								},
								Verbs: []string{"*"},
							},
							Selector: apisv1alpha2.PermissionClaimSelector{
								MatchAll: true,
							},
						},
						State: apisv1alpha2.ClaimAccepted,
					},
				},
			},
		}
		err = cli.Cluster(other).Create(ctx, binding)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("creating an APIBinding in the consumer workspace %q", consumer))
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
				PermissionClaims: []apisv1alpha2.AcceptablePermissionClaim{
					{
						ScopedPermissionClaim: apisv1alpha2.ScopedPermissionClaim{
							PermissionClaim: apisv1alpha2.PermissionClaim{
								GroupResource: apisv1alpha2.GroupResource{
									Group:    "events.k8s.io",
									Resource: "events",
								},
								Verbs: []string{"*"},
							},
							Selector: apisv1alpha2.PermissionClaimSelector{
								MatchAll: true,
							},
						},
						State: apisv1alpha2.ClaimAccepted,
					},
					{
						ScopedPermissionClaim: apisv1alpha2.ScopedPermissionClaim{
							PermissionClaim: apisv1alpha2.PermissionClaim{
								GroupResource: apisv1alpha2.GroupResource{
									Group:    "",
									Resource: "events",
								},
								Verbs: []string{"*"},
							},
							Selector: apisv1alpha2.PermissionClaimSelector{
								MatchAll: true,
							},
						},
						State: apisv1alpha2.ClaimAccepted,
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
				return false, fmt.Sprintf("failed to get APIExportEndpointSlice in %q: %v", provider, err)
			}
			return len(endpoints.Status.APIExportEndpoints) > 0, toYAML(GinkgoT(), endpoints)
		}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to see endpoints in APIExportEndpointSlice in %q", provider)
		vwEndpoint = endpoints.Status.APIExportEndpoints[0].URL

		By(fmt.Sprintf("waiting until the APIBinding in the consumer workspace %q to be ready", consumer))
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			current := &apisv1alpha2.APIBinding{}
			err := cli.Cluster(consumer).Get(ctx, client.ObjectKey{Name: "example.com"}, current)
			if err != nil {
				return false, fmt.Sprintf("failed to get APIBinding in %q: %v", consumer, err)
			}
			if current.Status.Phase != apisv1alpha2.APIBindingPhaseBound {
				return false, fmt.Sprintf("binding not bound:\n\n%s", toYAML(GinkgoT(), current))
			}
			return true, ""
		}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to wait for APIBinding in consumer workspace to be ready %q", consumer)

		By("waiting until things can be listed in the consumer workspace")
		envtest.Eventually(GinkgoT(), func() (bool, string) {
			u := &unstructured.UnstructuredList{}
			u.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "ThingList"})
			err = cli.Cluster(consumer).List(ctx, u)
			if err != nil {
				return false, fmt.Sprintf("failed to list things in %s: %v", consumer, err)
			}
			return true, ""
		}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to wait for things to be listable in consumer workspace %q", consumer)
	})

	Describe("with a multicluster provider and manager", func() {
		var (
			lock           sync.RWMutex
			engaged        = sets.NewString()
			eventLock      sync.RWMutex
			eventRecorders = make(map[string]events.EventRecorder)
			p              *apiexport.Provider
			g              *errgroup.Group
			cancelGroup    context.CancelFunc
		)

		BeforeAll(func() {
			By("creating a stone in the consumer workspace", func() {
				thing := &unstructured.Unstructured{}
				thing.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Thing"})
				thing.SetName("stone")
				thing.SetLabels(map[string]string{"color": "gray"})
				err := cli.Cluster(consumer).Create(ctx, thing)
				Expect(err).NotTo(HaveOccurred())
			})

			By("creating a box in the other workspace", func() {
				thing := &unstructured.Unstructured{}
				thing.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Thing"})
				thing.SetName("box")
				thing.SetLabels(map[string]string{"color": "white"})
				err := cli.Cluster(other).Create(ctx, thing)
				Expect(err).NotTo(HaveOccurred())
			})

			By("creating a multicluster provider for APIBindings against the apiexport virtual workspace")
			vwConfig := rest.CopyConfig(kcpConfig)
			vwConfig.Host = vwEndpoint
			providerConfig := rest.CopyConfig(kcpConfig)
			providerConfig.Host += provider.RequestPath()
			var err error
			p, err = apiexport.New(providerConfig, "example.com", apiexport.Options{
				Scheme: scheme.Scheme,
			})
			Expect(err).NotTo(HaveOccurred())

			By("waiting for discovery of the virtual workspace to show 'example.com'")
			wildcardConfig := rest.CopyConfig(vwConfig)
			wildcardConfig.Host += logicalcluster.Wildcard.RequestPath()
			disc, err := discovery.NewDiscoveryClientForConfig(wildcardConfig)
			Expect(err).NotTo(HaveOccurred())
			envtest.Eventually(GinkgoT(), func() (bool, string) {
				ret, err := disc.ServerGroups()
				Expect(err).NotTo(HaveOccurred())
				for _, g := range ret.Groups {
					if g.Name == "example.com" {
						return true, ""
					}
				}
				return false, fmt.Sprintf("failed to find group example.com in:\n%s", toYAML(GinkgoT(), ret))
			}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to find group example.com in the virtual workspace")

			By("creating a manager against the provider workspace")
			rootConfig := rest.CopyConfig(kcpConfig)
			rootConfig.Host += provider.RequestPath()
			mgr, err = mcmanager.New(rootConfig, p, mcmanager.Options{})
			Expect(err).NotTo(HaveOccurred())

			By("adding an index on label 'color'")
			thing := &unstructured.Unstructured{}
			thing.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Thing"})
			err = mgr.GetFieldIndexer().IndexField(ctx, thing, "color", func(obj client.Object) []string {
				u := obj.(*unstructured.Unstructured)
				return []string{u.GetLabels()["color"]}
			})
			Expect(err).NotTo(HaveOccurred())

			By("creating a reconciler for APIBindings")
			err = mcbuilder.ControllerManagedBy(mgr).
				Named("apibindings").
				For(&apisv1alpha2.APIBinding{}).
				Complete(mcreconcile.Func(func(ctx context.Context, request mcreconcile.Request) (reconcile.Result, error) {
					By(fmt.Sprintf("reconciling APIBinding %s in cluster %q", request.Name, request.ClusterName))
					lock.Lock()
					defer lock.Unlock()
					engaged.Insert(request.ClusterName)

					cluster, err := mgr.GetCluster(ctx, request.ClusterName)
					if err != nil {
						return reconcile.Result{}, err
					}

					var binding apisv1alpha2.APIBinding
					err = cluster.GetClient().Get(ctx, request.NamespacedName, &binding)
					if err != nil {
						return reconcile.Result{}, err
					}

					eventLock.RLock()
					recorder, ok := eventRecorders[request.ClusterName]
					if !ok {
						eventLock.RUnlock()
						eventLock.Lock()
						recorder = cluster.GetEventRecorder(request.ClusterName)
						eventRecorders[request.ClusterName] = recorder
						eventLock.Unlock()
					}

					recorder.Eventf(&binding, nil, corev1.EventTypeNormal, "reason", "message", "note")

					return reconcile.Result{}, nil
				}))
			Expect(err).NotTo(HaveOccurred())

			By("starting the provider and manager")
			var groupContext context.Context
			groupContext, cancelGroup = context.WithCancel(ctx)
			g, groupContext = errgroup.WithContext(groupContext)
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

		It("sees only the stone in the consumer clusters", func() {
			envtest.Eventually(GinkgoT(), func() (success bool, reason string) {
				l := &unstructured.UnstructuredList{}
				l.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "ThingList"})
				consumerCl, err := mgr.GetCluster(ctx, consumerWS.Spec.Cluster)
				Expect(err).NotTo(HaveOccurred())

				err = consumerCl.GetCache().List(ctx, l)
				if err != nil {
					return false, fmt.Sprintf("failed to list things in the consumer cluster cache: %v", err)
				}
				if len(l.Items) != 1 {
					return false, fmt.Sprintf("expected 1 item, got %d\n\n%s", len(l.Items), toYAML(GinkgoT(), l.Object))
				} else if name := l.Items[0].GetName(); name != "stone" {
					return false, fmt.Sprintf("expected item name to be stone, got %q\n\n%s", name, toYAML(GinkgoT(), l.Items[0]))
				}
				return true, ""
			}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to see the stone in the consumer cluster")
		})

		It("sees only the stone as grey thing in the consumer clusters", func() {
			consumerCl, err := mgr.GetCluster(ctx, consumerWS.Spec.Cluster)
			Expect(err).NotTo(HaveOccurred())
			envtest.Eventually(GinkgoT(), func() (success bool, reason string) {
				l := &unstructured.UnstructuredList{}
				l.SetGroupVersionKind(runtimeschema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "ThingList"})
				err = consumerCl.GetCache().List(ctx, l, client.MatchingFields{"color": "gray"})
				if err != nil {
					return false, fmt.Sprintf("failed to list things in the consumer cluster cache: %v", err)
				}
				if len(l.Items) != 1 {
					return false, fmt.Sprintf("expected 1 item, got %d\n\n%s", len(l.Items), toYAML(GinkgoT(), l.Object))
				} else if name := l.Items[0].GetName(); name != "stone" {
					return false, fmt.Sprintf("expected item name to be stone, got %q\n\n%s", name, toYAML(GinkgoT(), l.Items[0]))
				}
				return true, ""
			}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to see the stone as only thing of color 'grey' in the consumer cluster")
		})

		It("creates events on APIBindings in several workspaces", func() {
			envtest.Eventually(GinkgoT(), func() (success bool, reason string) {
				var events eventsv1.EventList
				err := cli.Cluster(consumer).List(ctx, &events)
				if err != nil {
					return false, err.Error()
				}

				if len(events.Items) != 1 {
					return false, fmt.Sprintf("expected to find a single event, found %d", len(events.Items))
				}

				return true, ""
			}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to see event on APIBinding in %s", consumer.String())

			envtest.Eventually(GinkgoT(), func() (success bool, reason string) {
				var events eventsv1.EventList
				err := cli.Cluster(other).List(ctx, &events)
				if err != nil {
					return false, err.Error()
				}

				if len(events.Items) != 1 {
					return false, fmt.Sprintf("expected to find a single event, found %d", len(events.Items))
				}

				return true, ""
			}, wait.ForeverTestTimeout, time.Millisecond*100, "failed to see event on APIBinding in %s", other.String())
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
