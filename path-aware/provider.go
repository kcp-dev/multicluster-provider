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

package pathaware

import (
	"context"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"

	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	"github.com/kcp-dev/logicalcluster/v3"
	kcpcore "github.com/kcp-dev/sdk/apis/core"

	provider "github.com/kcp-dev/multicluster-provider/apiexport"
	"github.com/kcp-dev/multicluster-provider/pkg/handlers"
	"github.com/kcp-dev/multicluster-provider/pkg/paths"
)

var _ multicluster.Provider = &Provider{}
var _ multicluster.ProviderRunnable = &Provider{}
var _ handlers.Handler = &pathHandler{}

// Provider is a [sigs.k8s.io/multicluster-runtime/pkg/multicluster.Provider] that represents each [logical cluster]
// (in the kcp sense) exposed via a APIExport virtual workspace as a cluster in the [sigs.k8s.io/multicluster-runtime] sense.
//
// Core functionality is delegated to the apiexport.Provider, this wrapper just adds best effort path-awareness index to it via hooks.
//
// [logical cluster]: https://docs.kcp.io/kcp/latest/concepts/terminology/#logical-cluster
type Provider struct {
	*provider.Provider
	// pathStore maps logical cluster paths to cluster names.
	pathStore *paths.Store
}

// New creates a new kcp virtual workspace provider. The provided [rest.Config]
// must point to a virtual workspace apiserver base path, i.e. up to but without
// the '/clusters/*' suffix. This information can be extracted from an APIExportEndpointSlice status.
func New(cfg *rest.Config, endpointSliceName string, options provider.Options) (*Provider, error) {
	store := paths.New()

	h := &pathHandler{
		pathStore: store,
	}
	options.Handlers = append(options.Handlers, h)

	p, err := provider.New(cfg, endpointSliceName, options)
	if err != nil {
		return nil, err
	}

	return &Provider{
		Provider:  p,
		pathStore: store,
	}, nil
}

// Get returns the cluster with the given name as a cluster.Cluster.
func (p *Provider) Get(ctx context.Context, clusterName string) (cluster.Cluster, error) {
	if p.pathStore != nil {
		if lcName, exists := p.pathStore.Get(clusterName); exists {
			clusterName = lcName.String()
		}
	}
	return p.Provider.Get(ctx, clusterName)
}

// IndexField adds an indexer to the clusters managed by this provider.
func (p *Provider) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	return p.Provider.IndexField(ctx, obj, field, extractValue)
}

// Start starts the provider and blocks.
func (p *Provider) Start(ctx context.Context, aware multicluster.Aware) error {
	return p.Provider.Start(ctx, aware)
}

type pathHandler struct {
	pathStore *paths.Store
}

func (p *pathHandler) OnAdd(obj client.Object) {
	cluster := logicalcluster.From(obj)

	path := obj.GetAnnotations()[kcpcore.LogicalClusterPathAnnotationKey]
	if path == "" {
		return
	}

	p.pathStore.Add(path, cluster)
}

func (p *pathHandler) OnUpdate(oldObj, newObj client.Object) {
	// Not used.
}

func (p *pathHandler) OnDelete(obj client.Object) {
	path, ok := obj.GetAnnotations()[kcpcore.LogicalClusterPathAnnotationKey]
	if !ok {
		return
	}

	p.pathStore.Remove(path)
}
