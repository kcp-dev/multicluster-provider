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
	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	apisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"

	"github.com/kcp-dev/multicluster-provider/pkg/handlers"
	"github.com/kcp-dev/multicluster-provider/pkg/provider"
)

var _ multicluster.Provider = &Provider{}
var _ multicluster.ProviderRunnable = &Provider{}

// Provider is a [sigs.k8s.io/multicluster-runtime/pkg/multicluster.Provider] that represents each [logical cluster]
// (in the kcp sense) exposed via a APIExport virtual workspace as a cluster in the [sigs.k8s.io/multicluster-runtime] sense.
//
// [logical cluster]: https://docs.kcp.io/kcp/latest/concepts/terminology/#logical-cluster
type Provider struct {
	*provider.Provider
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
}

// New creates a new kcp virtual workspace provider.
func New(cfg *rest.Config, endpointSliceName string, options Options) (*Provider, error) {
	if options.ObjectToWatch == nil {
		options.ObjectToWatch = &apisv1alpha1.APIBinding{}

		if options.AddFilter == nil {
			options.AddFilter = provider.ConditionReadyFunc("Ready")
		}
		if options.UpdateFilter == nil {
			options.UpdateFilter = provider.ConditionReadyFunc("Ready")
		}
	}

	if options.Log == nil {
		options.Log = ptr.To(log.Log.WithName("kcp-apiexport-cluster-provider"))
	}

	p, err := provider.NewProvider(cfg, endpointSliceName, provider.Options{
		Scheme:                       options.Scheme,
		EndpointSliceObject:          &apisv1alpha1.APIExportEndpointSlice{},
		ExtractURLsFromEndpointSlice: provider.DefaultExtractURLsFromEndpointSlice,
		ObjectToWatch:                options.ObjectToWatch,
		Log:                          options.Log,
		AddFilter:                    options.AddFilter,
		UpdateFilter:                 options.UpdateFilter,
		Handlers:                     options.Handlers,
	})
	if err != nil {
		return nil, err
	}

	return &Provider{Provider: p}, nil
}
