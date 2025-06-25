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

package shared

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/cluster"

	"github.com/kcp-dev/logicalcluster/v3"
)

func NewScopedCluster(cfg *rest.Config, clusterName logicalcluster.Name, wildcardCA WildcardCache, scheme *runtime.Scheme) (*scopedCluster, error) {
	cfg = rest.CopyConfig(cfg)
	host, err := url.JoinPath(cfg.Host, clusterName.Path().RequestPath())
	if err != nil {
		return nil, fmt.Errorf("failed to construct scoped cluster URL: %w", err)
	}
	cfg.Host = host

	// construct a scoped cache that uses the wildcard cache as base.
	ca := &scopedCache{
		base:        wildcardCA,
		clusterName: clusterName,
	}

	cli, err := client.New(cfg, client.Options{Cache: &client.CacheOptions{Reader: ca}, Scheme: scheme})
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

	return &scopedCluster{
		clusterName: clusterName,
		config:      cfg,
		scheme:      scheme,
		client:      cli,
		httpClient:  httpClient,
		mapper:      mapper,
		cache:       ca,
	}, nil
}

var _ cluster.Cluster = &scopedCluster{}

// scopedCluster is a cluster that operates on a specific namespace.
type scopedCluster struct {
	clusterName logicalcluster.Name

	scheme     *runtime.Scheme
	config     *rest.Config
	httpClient *http.Client
	client     client.Client
	mapper     meta.RESTMapper
	cache      cache.Cache
}

func (c *scopedCluster) GetHTTPClient() *http.Client {
	return c.httpClient
}

func (c *scopedCluster) GetConfig() *rest.Config {
	return c.config
}

func (c *scopedCluster) GetScheme() *runtime.Scheme {
	return c.scheme
}

func (c *scopedCluster) GetFieldIndexer() client.FieldIndexer {
	return c.cache
}

func (c *scopedCluster) GetRESTMapper() meta.RESTMapper {
	return c.mapper
}

// GetCache returns a cache.Cache.
func (c *scopedCluster) GetCache() cache.Cache {
	return c.cache
}

// GetClient returns a client scoped to the namespace.
func (c *scopedCluster) GetClient() client.Client {
	return c.client
}

// GetEventRecorderFor returns a new EventRecorder for the provided name.
func (c *scopedCluster) GetEventRecorderFor(name string) record.EventRecorder {
	panic("implement me")
}

// GetAPIReader returns a reader against the cluster.
func (c *scopedCluster) GetAPIReader() client.Reader {
	return c.cache
}

// Start starts the cluster.
func (c *scopedCluster) Start(ctx context.Context) error {
	return errors.New("scoped cluster cannot be started")
}
