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

package apiexport

import (
	"fmt"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"sigs.k8s.io/multicluster-runtime/pkg/clusters"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	apisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"

	mcpcache "github.com/kcp-dev/multicluster-provider/pkg/cache"
	"github.com/kcp-dev/multicluster-provider/pkg/handlers"
	"github.com/kcp-dev/multicluster-provider/pkg/provider"
)

var _ multicluster.Provider = &Provider{}
var _ multicluster.ProviderRunnable = &Provider{}

// ConditionReadyFunc returns a function that checks if the given object has a condition with the given type and status True.
func ConditionReadyFunc(conditionType string) func(client.Object) (bool, error) {
	return func(obj client.Object) (bool, error) {
		unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
		if err != nil {
			return false, fmt.Errorf("unable to convert object to unstructured map: %w", err)
		}

		conditions, found, err := unstructured.NestedSlice(unstructuredMap, "status", "conditions")
		if err != nil {
			return false, fmt.Errorf("error getting conditions from unstructured map: %w", err)
		}
		if !found {
			return false, nil
		}

		for _, condRaw := range conditions {
			cond, ok := condRaw.(map[string]any)
			if !ok {
				continue
			}

			condType, _, err := unstructured.NestedString(cond, "type")
			if err != nil {
				return false, fmt.Errorf("error getting type from unstructured condition %v: %w", cond, err)
			}

			condStatus, _, err := unstructured.NestedString(cond, "status")
			if err != nil {
				return false, fmt.Errorf("error getting status from unstructured condition %v: %w", cond, err)
			}

			if condType == conditionType && condStatus == string(corev1.ConditionTrue) {
				return true, nil
			}
		}

		return false, nil
	}
}

// Provider is a [sigs.k8s.io/multicluster-runtime/pkg/multicluster.Provider] that represents each [logical cluster]
// (in the kcp sense) exposed via a APIExport virtual workspace as a cluster in the [sigs.k8s.io/multicluster-runtime] sense.
//
// [logical cluster]: https://docs.kcp.io/kcp/latest/concepts/terminology/#logical-cluster
type Provider struct {
	provider.Factory
}

// Options are the options for creating a new instance of the apiexport provider.
type Options struct {
	// Scheme is the scheme to use for the provider. If this is nil, it defaults
	// to the client-go scheme.
	Scheme *runtime.Scheme

	// Log is the logger to use for the provider. If this is nil, it defaults
	// to the controller-runtime default logger.
	Log *logr.Logger

	// ObjectToWatch is the object type that the provider watches via a /clusters/*
	// wildcard endpoint to extract information about logical clusters joining and
	// leaving the "fleet" of (logical) clusters in kcp. If this is nil, it defaults
	// to [apisv1alpha1.APIBinding]. This might be useful when using this provider
	// against custom virtual workspaces that are not the APIExport one but share
	// the same endpoint semantics.
	ObjectToWatch client.Object

	// AddFilter is called to filter objects to engage.
	// If false is returned the object is not engaged.
	// If unset and ObjectToWatch is unset it defaults to ConditionReadyFunc("Ready")
	AddFilter func(obj client.Object) (bool, error)

	// UpdateFilter is called to filter objects to engage.
	// If false is returned the object is not engaged.
	// If unset and ObjectToWatch is unset it defaults to ConditionReadyFunc("Ready")
	UpdateFilter func(obj client.Object) (bool, error)

	// Handlers are lifecycle handlers, ran for each logical cluster in the provider represented
	// by apibinding object.
	Handlers handlers.Handlers

	// WildcardCache is the wildcard cache to use for the underlying
	// providers. If set this cache will be passed to the inner
	// providers created per
	// shard.
	//
	// NOTE: LOW LEVEL PRIMITIVE:
	// Only use a custom WildcardCache here if you know what you are doing.
	WildcardCache mcpcache.WildcardCache
}

// New creates a new kcp virtual workspace provider.
func New(cfg *rest.Config, endpointSliceName string, options Options) (*Provider, error) {
	// Do the defaulting controller-runtime would do for those fields we need.
	if options.Scheme == nil {
		options.Scheme = scheme.Scheme
	}

	if options.ObjectToWatch == nil {
		options.ObjectToWatch = &apisv1alpha1.APIBinding{}

		if options.AddFilter == nil {
			options.AddFilter = ConditionReadyFunc("Ready")
		}
		if options.UpdateFilter == nil {
			options.UpdateFilter = ConditionReadyFunc("Ready")
		}
	}

	if options.Log == nil {
		options.Log = ptr.To(log.Log.WithName("kcp-apiexport-cluster-provider"))
	}

	c, err := cache.New(cfg, cache.Options{
		Scheme: options.Scheme,
		ByObject: map[client.Object]cache.ByObject{
			&apisv1alpha1.APIExportEndpointSlice{}: {
				Field: fields.SelectorFromSet(fields.Set{"metadata.name": endpointSliceName}),
			},
		},
	})
	if err != nil {
		return nil, err
	}

	return &Provider{
		Factory: provider.Factory{
			Clusters:  ptr.To(clusters.New[cluster.Cluster]()),
			Providers: provider.NewProviders(),

			Log:      *options.Log,
			Handlers: options.Handlers,

			GetVWs: func(obj client.Object) ([]string, error) {
				ess := obj.(*apisv1alpha1.APIExportEndpointSlice)
				urls := make([]string, len(ess.Status.APIExportEndpoints))
				for i, endpoint := range ess.Status.APIExportEndpoints {
					urls[i] = endpoint.URL
				}
				return urls, nil
			},

			Config:        cfg,
			Scheme:        options.Scheme,
			Outer:         &apisv1alpha1.APIExportEndpointSlice{},
			Inner:         options.ObjectToWatch,
			AddFilter:     options.AddFilter,
			UpdateFilter:  options.UpdateFilter,
			Cache:         c,
			WildcardCache: options.WildcardCache,
		},
	}, nil
}
