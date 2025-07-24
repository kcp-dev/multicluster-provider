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

package initializingworkspaces

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/cluster"

	"github.com/kcp-dev/logicalcluster/v3"

	mcpcache "github.com/kcp-dev/multicluster-provider/internal/cache"
)

var _ cluster.Cluster = &SpecificCluster{}

// SpecificCluster constructs a new cluster.Cluster that operates on a specific logical cluster.
type SpecificCluster struct {
	clusterName logicalcluster.Name

	scheme     *runtime.Scheme
	config     *rest.Config
	httpClient *http.Client
	client     client.Client
	mapper     meta.RESTMapper
	cache      cache.Cache
}

// NewSpecificCluster create a cluster with specific URL and uses routed client to access the logical clusters from the wildcard cache.
func NewSpecificCluster(cfg *rest.Config, clusterName logicalcluster.Name, wildcardCA mcpcache.WildcardCache, scheme *runtime.Scheme) (*SpecificCluster, error) {
	cfg = rest.CopyConfig(cfg)
	host := cfg.Host
	host = strings.TrimSuffix(host, "/clusters/*")
	cfg.Host = fmt.Sprintf("%s/clusters/%s", host, clusterName)
	host, err := url.JoinPath(cfg.Host, clusterName.Path().RequestPath())
	if err != nil {
		return nil, fmt.Errorf("failed to construct scoped cluster URL: %w", err)
	}
	cfg.Host = host

	// construct a scoped cache that uses the wildcard cache as base.
	ca := &mcpcache.ScopedCache{
		Base:        wildcardCA,
		ClusterName: clusterName,
	}

	client, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}

	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, err
	}

	mapper, err := apiutil.NewDynamicRESTMapper(cfg, httpClient)
	if err != nil {
		return nil, err
	}

	// Create routing client that routes LogicalCluster to wildcard cache
	routedClient := newClient(client, wildcardCA, scheme)

	return &SpecificCluster{
		clusterName: clusterName,
		config:      cfg,
		scheme:      scheme,
		client:      routedClient,
		httpClient:  httpClient,
		mapper:      mapper,
		cache:       ca,
	}, nil
}

// GetHTTPClient returns the HTTP client scoped to the cluster.
func (c *SpecificCluster) GetHTTPClient() *http.Client {
	return c.httpClient
}

// GetConfig returns the rest.Config scoped to the cluster.
func (c *SpecificCluster) GetConfig() *rest.Config {
	return c.config
}

// GetScheme returns the scheme scoped to the cluster.
func (c *SpecificCluster) GetScheme() *runtime.Scheme {
	return c.scheme
}

// GetFieldIndexer returns a FieldIndexer scoped to the cluster.
func (c *SpecificCluster) GetFieldIndexer() client.FieldIndexer {
	return c.cache
}

// GetRESTMapper returns a RESTMapper scoped to the cluster.
func (c *SpecificCluster) GetRESTMapper() meta.RESTMapper {
	return c.mapper
}

// GetCache returns a cache.Cache.
func (c *SpecificCluster) GetCache() cache.Cache {
	return c.cache
}

// GetClient returns a client scoped to the namespace.
func (c *SpecificCluster) GetClient() client.Client {
	return c.client
}

// GetEventRecorderFor returns a new EventRecorder for the provided name.
func (c *SpecificCluster) GetEventRecorderFor(name string) record.EventRecorder {
	panic("implement me")
}

// GetAPIReader returns a reader against the cluster.
func (c *SpecificCluster) GetAPIReader() client.Reader {
	return c.cache
}

// Start starts the cluster.
func (c *SpecificCluster) Start(ctx context.Context) error {
	return errors.New("specific cluster cannot be started")
}
