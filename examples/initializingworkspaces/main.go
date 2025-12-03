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
	"slices"

	"github.com/spf13/pflag"
	"go.uber.org/zap/zapcore"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	apisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	corev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
	"github.com/kcp-dev/sdk/apis/tenancy/initialization"
	tenancyv1alpha1 "github.com/kcp-dev/sdk/apis/tenancy/v1alpha1"

	"github.com/kcp-dev/multicluster-provider/initializingworkspaces"
)

func init() {
	runtime.Must(corev1alpha1.AddToScheme(scheme.Scheme))
	runtime.Must(tenancyv1alpha1.AddToScheme(scheme.Scheme))
	runtime.Must(apisv1alpha1.AddToScheme(scheme.Scheme))
}

func main() {
	var (
		server          string
		initializerName string
		provider        *initializingworkspaces.Provider
		verbosity       int
	)

	pflag.StringVar(&server, "server", "", "Override for kubeconfig server URL")
	pflag.StringVar(&initializerName, "initializer", "initializer:example", "Name of the initializer to use")
	pflag.IntVar(&verbosity, "v", 1, "Log verbosity level")
	pflag.Parse()

	logOpts := zap.Options{
		Development: true,
		Level:       zapcore.Level(-verbosity),
	}
	log.SetLogger(zap.New(zap.UseFlagOptions(&logOpts)))

	ctx := signals.SetupSignalHandler()
	entryLog := log.Log.WithName("entrypoint")
	cfg := ctrl.GetConfigOrDie()
	cfg = rest.CopyConfig(cfg)

	if server != "" {
		cfg.Host = server
	}

	entryLog.Info("Setting up manager")
	opts := manager.Options{}

	var err error
	provider, err = initializingworkspaces.New(cfg, initializingworkspaces.Options{InitializerName: initializerName})
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
		Named("kcp-initializer-controller").
		For(&corev1alpha1.LogicalCluster{}).
		Complete(mcreconcile.Func(
			func(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
				log := log.FromContext(ctx).WithValues("cluster", req.ClusterName)
				cl, err := mgr.GetCluster(ctx, req.ClusterName)
				if err != nil {
					return reconcile.Result{}, fmt.Errorf("failed to get cluster: %w", err)
				}
				client := cl.GetClient()
				lc := &corev1alpha1.LogicalCluster{}
				if err := client.Get(ctx, req.NamespacedName, lc); err != nil {
					return reconcile.Result{}, fmt.Errorf("failed to get logical cluster: %w", err)
				}

				log.Info("Reconciling LogicalCluster", "logical cluster", lc.Spec)
				initializer := corev1alpha1.LogicalClusterInitializer(initializerName)
				// check if your initializer is still set on the logicalcluster
				if slices.Contains(lc.Status.Initializers, initializer) {
					log.Info("Starting to initialize cluster")
					workspaceName := fmt.Sprintf("initialized-workspace-%s", req.ClusterName)
					ws := &tenancyv1alpha1.Workspace{}
					err = client.Get(ctx, ctrlclient.ObjectKey{Name: workspaceName}, ws)
					if err != nil {
						if !apierrors.IsNotFound(err) {
							log.Error(err, "Error checking for existing workspace")
							return reconcile.Result{}, nil
						}

						log.Info("Creating child workspace", "name", workspaceName)
						ws = &tenancyv1alpha1.Workspace{
							ObjectMeta: ctrl.ObjectMeta{
								Name: workspaceName,
							},
						}

						if err := client.Create(ctx, ws); err != nil {
							log.Error(err, "Failed to create workspace")
							return reconcile.Result{}, nil
						}
						log.Info("Workspace created successfully", "name", workspaceName)
					}

					if ws.Status.Phase != corev1alpha1.LogicalClusterPhaseReady {
						log.Info("Workspace not ready yet", "current-phase", ws.Status.Phase)
						return reconcile.Result{Requeue: true}, nil
					}
					log.Info("Workspace is ready, proceeding to create ConfigMap")

					s := &corev1.ConfigMap{
						ObjectMeta: ctrl.ObjectMeta{
							Name:      "kcp-initializer-cm",
							Namespace: "default",
						},
						Data: map[string]string{
							"test-data": "example-value",
						},
					}
					log.Info("Reconciling ConfigMap", "name", s.Name, "uuid", s.UID)
					if err := client.Create(ctx, s); err != nil {
						return reconcile.Result{}, fmt.Errorf("failed to create configmap: %w", err)
					}
					log.Info("ConfigMap created successfully", "name", s.Name, "uuid", s.UID)
				}
				// Remove the initializer from the logical cluster status
				// so that it won't be processed again.
				if !slices.Contains(lc.Status.Initializers, initializer) {
					log.Info("Initializer already absent, skipping patch")
					return reconcile.Result{}, nil
				}

				patch := ctrlclient.MergeFrom(lc.DeepCopy())
				lc.Status.Initializers = initialization.EnsureInitializerAbsent(initializer, lc.Status.Initializers)
				if err := client.Status().Patch(ctx, lc, patch); err != nil {
					return reconcile.Result{}, err
				}
				log.Info("Removed initializer from LogicalCluster status", "name", lc.Name, "uuid", lc.UID)
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
