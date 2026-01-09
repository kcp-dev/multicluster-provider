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
	"time"

	"github.com/spf13/pflag"
	"go.uber.org/zap/zapcore"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
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
	kcpcore "github.com/kcp-dev/sdk/apis/core"
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
		server            string
		workspaceTypeName string
		provider          *initializingworkspaces.Provider
		logLevel          string
	)

	pflag.StringVar(&server, "server", "", "Override for kubeconfig server URL")
	pflag.StringVar(&workspaceTypeName, "workspace-type", "example", "Name of the WorkspaceType to watch")
	pflag.StringVar(&logLevel, "log-level", zapcore.InfoLevel.String(), "Log verbosity level")
	pflag.Parse()

	logOpts := zap.Options{
		Development: true,
	}

	zapLevel, err := zapcore.ParseLevel(logLevel)
	if err == nil {
		logOpts.Level = zapLevel
	}

	log.SetLogger(zap.New(zap.UseFlagOptions(&logOpts)))
	entryLog := log.Log.WithName("entrypoint")

	if err != nil {
		entryLog.Error(err, "Invalid log level")
	}

	ctx := signals.SetupSignalHandler()
	cfg := ctrl.GetConfigOrDie()
	cfg = rest.CopyConfig(cfg)

	if server != "" {
		cfg.Host = server
	}

	entryLog.Info("Validating WorkspaceType", "name", workspaceTypeName)
	client, err := ctrlclient.New(cfg, ctrlclient.Options{})
	if err != nil {
		entryLog.Error(err, "Unable to construct cluster client")
		os.Exit(1)
	}

	wst := &tenancyv1alpha1.WorkspaceType{}
	if err := client.Get(ctx, types.NamespacedName{Name: workspaceTypeName}, wst); err != nil {
		entryLog.Error(err, "Failed to get WorkspaceType")
		os.Exit(1)
	}

	initializer := initialization.InitializerForType(wst)
	entryLog.Info("Using initializer", "initializer", initializer)

	entryLog.Info("Setting up manager")
	provider, err = initializingworkspaces.New(cfg, workspaceTypeName, initializingworkspaces.Options{})
	if err != nil {
		entryLog.Error(err, "Unable to construct cluster provider")
		os.Exit(1)
	}

	mgr, err := mcmanager.New(cfg, provider, manager.Options{})
	if err != nil {
		entryLog.Error(err, "Unable to set up overall controller manager")
		os.Exit(1)
	}

	if err := mcbuilder.ControllerManagedBy(mgr).
		Named("kcp-initializer-controller").
		For(&corev1alpha1.LogicalCluster{}).
		Complete(mcreconcile.Func(
			func(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
				logger := log.FromContext(ctx).WithValues("cluster", req.ClusterName)
				logger.Info("Reconciling logicalcluster")

				cl, err := mgr.GetCluster(ctx, req.ClusterName)
				if err != nil {
					return reconcile.Result{}, fmt.Errorf("failed to get cluster: %w", err)
				}
				client := cl.GetClient()
				lc := &corev1alpha1.LogicalCluster{}
				if err := client.Get(ctx, req.NamespacedName, lc); err != nil {
					return reconcile.Result{}, ctrlclient.IgnoreNotFound(err)
				}

				// wait for the tenancy API to be available
				if _, err := cl.GetRESTMapper().KindFor(tenancyv1alpha1.SchemeGroupVersion.WithResource("workspaces")); err != nil {
					logger.Info("kcp tenancy API is not available yet, requeuing")
					return reconcile.Result{RequeueAfter: 1 * time.Second}, nil
				}

				workspace := lc.Annotations[kcpcore.LogicalClusterPathAnnotationKey]
				if workspace == "" {
					workspace = "root"
				}

				logger = logger.WithValues("workspace", workspace)

				// check if your initializer is still set on the logicalcluster
				if !slices.Contains(lc.Status.Initializers, initializer) {
					logger.Info("Initializer already absent, skipping patch")
					return reconcile.Result{}, nil
				}

				logger.Info("Starting to initialize cluster")
				childWorkspace := fmt.Sprintf("initialized-workspace-%s", req.ClusterName)

				ws := &tenancyv1alpha1.Workspace{}
				err = client.Get(ctx, ctrlclient.ObjectKey{Name: childWorkspace}, ws)
				if err != nil {
					if !apierrors.IsNotFound(err) {
						logger.Error(err, "Error checking for existing workspace", "reason", apierrors.ReasonForError(err))
						return reconcile.Result{}, nil
					}

					logger.Info("Creating child workspace", "child", childWorkspace)
					ws = &tenancyv1alpha1.Workspace{
						ObjectMeta: ctrl.ObjectMeta{
							Name: childWorkspace,
						},
					}

					if err := client.Create(ctx, ws); err != nil {
						logger.Error(err, "Failed to create workspace")
						return reconcile.Result{}, nil
					}
					logger.Info("Child workspace created successfully", "child", childWorkspace)
				}

				if ws.Status.Phase != corev1alpha1.LogicalClusterPhaseReady {
					logger.Info("Child workspace not ready yet", "child", childWorkspace, "current-phase", ws.Status.Phase)
					return reconcile.Result{RequeueAfter: 1 * time.Second}, nil
				}

				logger.Info("Child workspace is ready, proceeding to create ConfigMap")

				s := &corev1.ConfigMap{
					ObjectMeta: ctrl.ObjectMeta{
						Name:      "kcp-initializer-cm",
						Namespace: "default",
					},
					Data: map[string]string{
						"test-data": "example-value",
					},
				}
				logger.Info("Reconciling ConfigMap", "configmap", s.Name)
				if err := client.Create(ctx, s); err != nil {
					return reconcile.Result{}, fmt.Errorf("failed to create ConfigMap: %w", err)
				}
				logger.Info("ConfigMap created successfully", "configmap", s.Name)

				// eventually remove the initializer from the cluster
				patch := ctrlclient.MergeFrom(lc.DeepCopy())
				lc.Status.Initializers = initialization.EnsureInitializerAbsent(initializer, lc.Status.Initializers)
				if err := client.Status().Patch(ctx, lc, patch); err != nil {
					return reconcile.Result{}, err
				}

				logger.Info("Removed initializer from LogicalCluster status")

				return reconcile.Result{}, nil
			},
		)); err != nil {
		entryLog.Error(err, "Failed to build controller")
		os.Exit(1)
	}

	entryLog.Info("Starting manager")
	if err := mgr.Start(ctx); err != nil {
		entryLog.Error(err, "Unable to run manager")
		os.Exit(1)
	}
}
