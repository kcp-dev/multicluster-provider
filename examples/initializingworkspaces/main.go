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

	apisv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	"github.com/spf13/pflag"
	"go.uber.org/zap/zapcore"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	corev1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	"github.com/kcp-dev/kcp/sdk/apis/tenancy/initialization"
	tenancyv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/kcp-dev/multicluster-provider/initializingworkspaces"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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
		Watches(
			&corev1alpha1.LogicalCluster{},
			handleLogicalClusterEvent(),
		).Complete(mcreconcile.Func(
		func(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
			log := log.FromContext(ctx).WithValues("cluster", req.ClusterName)
			for name := range provider.Clusters {
				log.Info("Cluster in provider cache", "name", name)
			}
			cl, err := mgr.GetCluster(ctx, req.ClusterName)
			if err != nil {
				log.Info("Cluster not found, will retry", "cluster", req.ClusterName, "reason", err.Error())
				return reconcile.Result{Requeue: true, RequeueAfter: 2 * time.Second}, nil
			}
			log.Info("GetCluster success", "cluster", req.ClusterName)

			client := cl.GetClient()
			log.Info("Cluster client retrieved", "cluster", req.ClusterName)

			lc := &corev1alpha1.LogicalCluster{}
			if err := client.Get(ctx, req.NamespacedName, lc); err != nil {
				return reconcile.Result{}, err
			}

			log.Info("Reconciling LogicalCluster", "name", lc.Name, "LC", lc.Spec)
			// check if your initializer is still set on the logicalcluster
			if slices.Contains(lc.Status.Initializers, corev1alpha1.LogicalClusterInitializer(initializerName)) {
				log.Info("Starting to initialize cluster")

				workspaceName := fmt.Sprintf("initialized-workspace-%s", req.Name)
				ws := &tenancyv1alpha1.Workspace{}
				err = client.Get(ctx, ctrlclient.ObjectKey{Name: workspaceName}, ws)
				if err != nil {
					if !apierrors.IsNotFound(err) {
						log.Error(err, "Error checking for existing workspace")
						return reconcile.Result{}, err
					}

					log.Info("Creating child workspace", "name", workspaceName)
					ws = &tenancyv1alpha1.Workspace{
						ObjectMeta: ctrl.ObjectMeta{
							Name: workspaceName,
						},
					}

					if err := client.Create(ctx, ws); err != nil {
						log.Error(err, "Failed to create workspace")
						return reconcile.Result{Requeue: true}, err
					}
					log.Info("Workspace created successfully", "name", workspaceName)
				} else {
					log.Info("Found existing workspace", "name", workspaceName, "phase", ws.Status.Phase)
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
				log.Info("Removing initializer from LogicalCluster status", "name", lc.Name, "uuid", lc.UID)
				// Remove the initializer from the logical cluster status
				// so that it won't be processed again.
				initializerName := corev1alpha1.LogicalClusterInitializer(initializerName)
				if !slices.Contains(lc.Status.Initializers, initializerName) {
					log.Info("Initializer already absent, skipping patch")
					return reconcile.Result{}, nil
				}
				patch := ctrlclient.MergeFrom(lc.DeepCopy())
				lc.Status.Initializers = initialization.EnsureInitializerAbsent(initializerName, lc.Status.Initializers)
				if err := client.Status().Patch(ctx, lc, patch); err != nil {
					return reconcile.Result{}, err
				}
				log.Info("Removed initializer from LogicalCluster status", "name", lc.Name, "uuid", lc.UID)
			}
			return reconcile.Result{}, err
		},
	)); err != nil {
		entryLog.Error(err, "failed to build controller")
		os.Exit(1)
	}

	if provider != nil {
		entryLog.Info("Starting provider")
		go func() {
			if err := provider.Run(ctx, mgr); err != nil {
				entryLog.Error(err, "unable to run provider")
				os.Exit(1)
			}
		}()
	}

	entryLog.Info("Starting manager")
	if err := mgr.Start(ctx); err != nil {
		entryLog.Error(err, "unable to run manager")
		os.Exit(1)
	}
}

func handleLogicalClusterEvent() mchandler.TypedEventHandlerFunc[ctrlclient.Object, mcreconcile.Request] {
	return func(clusterName string, cl cluster.Cluster) handler.TypedEventHandler[ctrlclient.Object, mcreconcile.Request] {
		log.Log.Info("Setting up event handler for LogicalCluster", "cluster", clusterName)
		return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj ctrlclient.Object) []mcreconcile.Request {
			clusterID, ok := obj.GetAnnotations()["kcp.io/cluster"]
			if !ok {
				clusterID = clusterName
			}

			log.Log.Info("Event Handler reconcile request",
				"cluster", clusterName,
				"name", clusterID)

			return []mcreconcile.Request{
				{
					ClusterName: clusterID,
				},
			}
		})
	}
}
