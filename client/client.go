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

package client

import (
	"fmt"
	"sync"

	"github.com/hashicorp/golang-lru/v2"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kcp-dev/logicalcluster/v3"
)

// ClusterClient is a cluster-aware client.
type ClusterClient interface {
	// Cluster returns the client for the given cluster.
	Cluster(cluster logicalcluster.Path) client.Client
}

// clusterClient is a multi-cluster-aware client.
type clusterClient struct {
	baseConfig *rest.Config
	opts       client.Options

	lock  sync.RWMutex
	cache *lru.Cache[logicalcluster.Path, client.Client]
}

// New creates a new cluster-aware client.
func New(cfg *rest.Config, options client.Options) (ClusterClient, error) {
	ca, err := lru.New[logicalcluster.Path, client.Client](100)
	if err != nil {
		return nil, err
	}
	return &clusterClient{
		opts:       options,
		baseConfig: cfg,
		cache:      ca,
	}, nil
}

func (c *clusterClient) Cluster(cluster logicalcluster.Path) client.Client {
	// quick path
	c.lock.RLock()
	cli, ok := c.cache.Get(cluster)
	c.lock.RUnlock()
	if ok {
		return cli
	}

	// slow path
	c.lock.Lock()
	defer c.lock.Unlock()
	if cli, ok := c.cache.Get(cluster); ok {
		return cli
	}

	// cache miss
	cfg := rest.CopyConfig(c.baseConfig)
	cfg.Host += cluster.RequestPath()
	cli, err := client.New(cfg, c.opts)
	if err != nil {
		panic(fmt.Errorf("failed to create client for cluster %s: %w", cluster, err))
	}
	c.cache.Add(cluster, cli)
	return cli
}
