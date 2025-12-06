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
	"strings"

	"github.com/spf13/pflag"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	kcpkubernetesclient "github.com/kcp-dev/client-go/kubernetes"
	"github.com/kcp-dev/logicalcluster/v3"
	apisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	kcpcore "github.com/kcp-dev/sdk/apis/core"
	corev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
	tenancyv1alpha1 "github.com/kcp-dev/sdk/apis/tenancy/v1alpha1"

	"github.com/kcp-dev/multicluster-provider/apiexport"
)

func init() {
	runtime.Must(corev1alpha1.AddToScheme(scheme.Scheme))
	runtime.Must(tenancyv1alpha1.AddToScheme(scheme.Scheme))
	runtime.Must(apisv1alpha1.AddToScheme(scheme.Scheme))
}

func main() {
	log.SetLogger(zap.New(zap.UseDevMode(true)))

	ctx := signals.SetupSignalHandler()
	entryLog := log.Log.WithName("entrypoint")

	var (
		endpointSlice string
		provider      *apiexport.Provider
	)

	pflag.StringVar(&endpointSlice, "endpointslice", "logicalcluster.workspaces.kcp.dev", "Set the APIExportEndpointSlice name to watch")
	pflag.Parse()

	cfg := ctrl.GetConfigOrDie()
	cfgVW := rest.CopyConfig(cfg)

	// We need a root config that can see all workspaces and we can use to create the kcp kubernetes client from.
	// This is a demo, so we just strip everything after /clusters/ in the current config's host.
	// In a real world scenario, you might want to use a dedicated kubeconfig with access to all workspaces instead.
	cfgRoot := rest.CopyConfig(cfg)
	cfgRoot.Host = strings.Split(cfgRoot.Host, "/clusters/")[0]

	// Assume that our restConfig we use for this example can access all the workspaces:
	// IMPORTANT: This should be shard-aware client, meaning it should go via front-proxy to be able to resolve
	// objects accross shards.
	kcpkubernetesClient, err := kcpkubernetesclient.NewForConfig(cfgRoot)
	if err != nil {
		entryLog.Error(err, "failed to create kcp kubernetes client")
		os.Exit(1)
	}

	// Setup a Manager, note that this not yet engages clusters, only makes them available.
	entryLog.Info("Setting up manager")
	opts := manager.Options{}

	provider, err = apiexport.New(cfg, endpointSlice, apiexport.Options{})
	if err != nil {
		entryLog.Error(err, "unable to construct cluster provider")
		os.Exit(1)
	}

	mgr, err := mcmanager.New(cfgVW, provider, opts)
	if err != nil {
		entryLog.Error(err, "unable to set up overall controller manager")
		os.Exit(1)
	}

	if err := mcbuilder.ControllerManagedBy(mgr).
		Named("kcp-logicalcluster-controller").
		For(&corev1alpha1.LogicalCluster{}).
		Complete(mcreconcile.Func(
			func(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
				log := log.FromContext(ctx).WithValues("cluster", req.ClusterName)

				cl, err := mgr.GetCluster(ctx, req.ClusterName)
				if err != nil {
					return reconcile.Result{}, fmt.Errorf("failed to get cluster: %w", err)
				}
				client := cl.GetClient()

				// Retrieve the LogicalCluster from the cluster.
				s := &corev1alpha1.LogicalCluster{}
				if err := client.Get(ctx, req.NamespacedName, s); err != nil {
					if apierrors.IsNotFound(err) {
						// LogicalCluster was deleted
						return reconcile.Result{}, nil
					}
					return reconcile.Result{}, fmt.Errorf("failed to get logicalcluster: %w", err)
				}

				log.Info("Reconciling LogicalCluster", "name", s.Name, "uuid", s.UID)

				// Let workspace path:
				pathString, ok := s.Annotations[kcpcore.LogicalClusterPathAnnotationKey]
				if !ok {
					return reconcile.Result{}, fmt.Errorf("logicalcluster %q is missing annotation %q", s.Name, kcpcore.LogicalClusterPathAnnotationKey)
				}

				// This is just to demonstrate how to parse and use the path.
				// This is where one would take the path and implement custom logic.
				path := logicalcluster.NewPath(pathString)
				if !path.IsValid() {
					err := fmt.Errorf("workspace path %q is not valid", pathString)
					log.Error(err, "Invalid workspace path")
					return reconcile.Result{}, nil
				}

				// Everything below is just demo of walking the workspace hierarchy and printing info by resolving each workspace
				// in the hierarchy to its default `namespace` UUID and name. This is one of the most basic things one can do
				// with the workspace path, but it is not something that is done often in practice, as most controllers
				// only care about the current workspace.
				type WorkspaceInfo struct {
					Path string
					Name string
					UUID string
				}

				var workspaces []WorkspaceInfo
				if parent, ok := path.Parent(); ok {
					// Collect workspace info from each workspace in the hierarchy
					for p := parent; !p.Empty(); p, _ = p.Parent() {
						ns, err := kcpkubernetesClient.Cluster(p).CoreV1().Namespaces().Get(ctx, "default", metav1.GetOptions{})
						if err != nil {
							log.Error(err, "failed to get namespace", "path", p.String())
							continue
						}
						workspaces = append(workspaces, WorkspaceInfo{
							Path: p.String(),
							Name: ns.Name,
							UUID: string(ns.UID),
						})
					}
				}

				// Reverse the slice to show root-to-leaf order (true tree structure)
				for i, j := 0, len(workspaces)-1; i < j; i, j = i+1, j-1 {
					workspaces[i], workspaces[j] = workspaces[j], workspaces[i]
				}

				// Print in tree format
				fmt.Printf("\n=== Workspace Hierarchy Tree for %s ===\n", s.Name)
				if len(workspaces) == 0 {
					fmt.Printf("└── (no parent workspaces)\n")
				} else {
					// Group by workspace path to show tree structure
					pathMap := make(map[string][]WorkspaceInfo)
					for _, ws := range workspaces {
						pathMap[ws.Path] = append(pathMap[ws.Path], ws)
					}

					// Print each workspace level with tree formatting
					for i, ws := range workspaces {
						var prefix string
						if i == len(workspaces)-1 {
							prefix = "└── "
						} else {
							prefix = "├── "
						}

						// Calculate indentation based on path depth
						pathParts := strings.Split(strings.Trim(ws.Path, ":"), ":")
						indent := strings.Repeat("    ", len(pathParts)-1)

						fmt.Printf("%s%s%s [%s]\n", indent, prefix, ws.Path, ws.Name)
						fmt.Printf("%s    └─ UUID: %s\n", indent, ws.UUID)
					}
				}
				fmt.Printf("Total workspaces in hierarchy: %d\n\n", len(workspaces))

				return reconcile.Result{}, nil
			},
		)); err != nil {
		entryLog.Error(err, "failed to build controller")
		os.Exit(1)
	}

	if provider != nil {
		entryLog.Info("Starting provider")
		go func() {
			if err := provider.Start(ctx, mgr); err != nil {
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
