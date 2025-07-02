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
	"fmt"
	"net/http"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"

	"github.com/kcp-dev/logicalcluster/v3"
)

var _ cluster.Cluster = &specificCluster{}

type specificCluster struct {
	clusterName logicalcluster.Name

	scheme     *runtime.Scheme
	config     *rest.Config
	httpClient *http.Client
	client     client.Client
	mapper     meta.RESTMapper
	cache      cache.Cache
}

// New method to create a cluster with specific URL
func (p *Provider) createSpecificCluster(clusterName logicalcluster.Name, scheme *runtime.Scheme) (cluster.Cluster, error) {
	specificConfig := rest.CopyConfig(p.config)
	host := p.config.Host
	host = strings.TrimSuffix(host, "/clusters/*")
	specificConfig.Host = fmt.Sprintf("%s/clusters/%s", host, clusterName)

	cli, err := client.New(specificConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}

	httpClient, err := rest.HTTPClientFor(specificConfig)
	if err != nil {
		return nil, err
	}

	mapper, err := apiutil.NewDynamicRESTMapper(specificConfig, httpClient)
	if err != nil {
		return nil, err
	}

	specificCache, err := cache.New(specificConfig, cache.Options{
		Scheme: scheme,
		Mapper: mapper,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create cache: %w", err)
	}

	return &specificCluster{
		clusterName: clusterName,
		config:      specificConfig,
		scheme:      scheme,
		client:      cli,
		httpClient:  httpClient,
		mapper:      mapper,
		cache:       specificCache,
	}, nil
}

// GetHTTPClient returns the HTTP client scoped to the cluster.
func (c *specificCluster) GetHTTPClient() *http.Client {
	return c.httpClient
}

// GetConfig returns the rest.Config scoped to the cluster.
func (c *specificCluster) GetConfig() *rest.Config {
	return c.config
}

// GetScheme returns the scheme scoped to the cluster.
func (c *specificCluster) GetScheme() *runtime.Scheme {
	return c.scheme
}

// GetFieldIndexer returns a FieldIndexer scoped to the cluster.
func (c *specificCluster) GetFieldIndexer() client.FieldIndexer {
	return c.cache
}

// GetRESTMapper returns a RESTMapper scoped to the cluster.
func (c *specificCluster) GetRESTMapper() meta.RESTMapper {
	return c.mapper
}

// GetCache returns a cache.Cache.
func (c *specificCluster) GetCache() cache.Cache {
	return c.cache
}

// GetClient returns a client scoped to the namespace.
func (c *specificCluster) GetClient() client.Client {
	return c.client
}

// GetEventRecorderFor returns a new EventRecorder for the provided name.
func (c *specificCluster) GetEventRecorderFor(name string) record.EventRecorder {
	panic("implement me")
}

// GetAPIReader returns a reader against the cluster.
func (c *specificCluster) GetAPIReader() client.Reader {
	return c.cache
}

// Start starts the cluster.
func (c *specificCluster) Start(ctx context.Context) error {
	go func() {
		if err := c.cache.Start(ctx); err != nil {
			klog.Errorf("Failed to start cache for cluster %s: %v", c.clusterName, err)
		}
	}()

	// Wait for cache to sync
	syncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if !c.cache.WaitForCacheSync(syncCtx) {
		return fmt.Errorf("failed to sync cache for cluster %s", c.clusterName)
	}

	return nil
}
