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

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/pflag"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	apisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	apisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	kcpcore "github.com/kcp-dev/sdk/apis/core"
	corev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
	tenancyv1alpha1 "github.com/kcp-dev/sdk/apis/tenancy/v1alpha1"

	"github.com/kcp-dev/multicluster-provider/apiexport"
	pathaware "github.com/kcp-dev/multicluster-provider/path-aware"
)

func init() {
	runtime.Must(corev1alpha1.AddToScheme(scheme.Scheme))
	runtime.Must(tenancyv1alpha1.AddToScheme(scheme.Scheme))
	runtime.Must(apisv1alpha1.AddToScheme(scheme.Scheme))
	runtime.Must(apisv1alpha2.AddToScheme(scheme.Scheme))
}

func main() {
	log.SetLogger(zap.New(zap.UseDevMode(true)))

	ctx := signals.SetupSignalHandler()
	entryLog := log.Log.WithName("entrypoint")

	var (
		endpointSlice string
		provider      *pathaware.Provider
	)

	pflag.StringVar(&endpointSlice, "endpointslice", "examples-path-aware-apiexport-multicluster", "Set the APIExportEndpointSlice name to watch")
	pflag.Parse()

	cfg := ctrl.GetConfigOrDie()

	// Setup a Manager, note that this not yet engages clusters, only makes them available.
	entryLog.Info("Setting up manager")
	opts := manager.Options{}

	var err error
	provider, err = pathaware.New(cfg, endpointSlice, apiexport.Options{})
	if err != nil {
		entryLog.Error(err, "unable to construct cluster provider")
		os.Exit(1)
	}

	mgr, err := mcmanager.New(cfg, provider, opts)
	if err != nil {
		entryLog.Error(err, "unable to set up overall controller manager")
		os.Exit(1)
	}

	if err := mcbuilder.ControllerManagedBy(mgr).
		Named("kcp-configmap-controller").
		For(&corev1.ConfigMap{}).
		Complete(mcreconcile.Func(
			func(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
				log := log.FromContext(ctx).WithValues("cluster", req.ClusterName)

				// This is main part of the the example:
				// 1. Get the cluster by name from the request
				// 2. Use the client from the cluster to get a resource (ConfigMap here)
				// 3. Additionally, demonstrate that we can also get the cluster by logical cluster path

				cl, err := mgr.GetCluster(ctx, req.ClusterName)
				if err != nil {
					return reconcile.Result{}, fmt.Errorf("failed to get cluster: %w", err)
				}
				client := cl.GetClient()

				// Retrieve the ConfigMap from the cluster.
				s := &corev1.ConfigMap{}
				if err := client.Get(ctx, req.NamespacedName, s); err != nil {
					if apierrors.IsNotFound(err) {
						// ConfigMap was deleted.
						return reconcile.Result{}, nil
					}
					return reconcile.Result{}, fmt.Errorf("failed to get configmap: %w", err)
				}

				log.Info("Reconciling ConfigMap", "name", s.Name, "uuid", s.UID)
				recorder := cl.GetEventRecorder("kcp-configmap-controller")
				recorder.Eventf(s, nil, corev1.EventTypeNormal, "ConfigMapChanged", "ConfigMapReconciling", "reconciling ConfigMap %s", s.Name)

				// This is optional part. Please delete it if you want to keep the example minimal.
				// Here we demonstrate that you can query cluster object by using canonical logical cluster path
				// stored in APIBinding annotation or acquired by other means in the system. To access the cluster this
				// way, cluster MUST be engaged in the manager already (to have apiBinding to the same virtual workspace).

				// We should be able to access APIBinding in the same cluster. APIBindings are easiest way to get
				// canonical logical cluster path.
				bindings := &apisv1alpha2.APIBindingList{}
				if err := client.List(ctx, bindings); err != nil {
					return reconcile.Result{}, fmt.Errorf("failed to list apibindings: %w", err)
				}
				log.Info("Found APIBindings in the cluster", "count", len(bindings.Items))

				// As we operate on single cluster - we should have only 1 APIBinding per logical cluster path.
				for _, binding := range bindings.Items {
					log.Info("Found APIBinding", "name", binding.Name, "status", binding.Status.Phase)
					canonicalPath, ok := binding.Annotations[kcpcore.LogicalClusterPathAnnotationKey]
					if !ok {
						log.Error(fmt.Errorf("missing annotation"), "APIBinding is missing logical cluster path annotation", "name", binding.Name)
						continue
					}
					log.Info("APIBinding logical cluster path", "path", canonicalPath)

					// We should be able to resolve cluster object via path as well
					clPath, err := mgr.GetCluster(ctx, canonicalPath)
					if err != nil {
						log.Error(err, "failed to get cluster by path", "path", canonicalPath)
						continue
					}
					client2 := clPath.GetClient()
					// Retrieve the ConfigMap from the cluster.
					s := &corev1.ConfigMap{}
					if err := client2.Get(ctx, req.NamespacedName, s); err != nil {
						if apierrors.IsNotFound(err) {
							// ConfigMap was deleted.
							return reconcile.Result{}, nil
						}
						return reconcile.Result{}, fmt.Errorf("failed to get configmap: %w", err)
					}
					log.Info("Found ConfigMap via path lookup", "name", s.Name, "in cluster", canonicalPath)
				}

				recorder.Eventf(s, nil, corev1.EventTypeNormal, "ConfigMapChanged", "ConfigMapReconciled", "reconciled ConfigMap %s", s.Name)
				return reconcile.Result{}, nil
			},
		)); err != nil {
		entryLog.Error(err, "failed to build controller")
		os.Exit(1)
	}

	entryLog.Info("Starting manager")
	if err := mgr.Start(ctx); err != nil {
		entryLog.Error(err, "unable to run manager")
		os.Exit(1)
	}
}
