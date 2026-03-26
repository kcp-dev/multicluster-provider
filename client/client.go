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

package client

import (
	"fmt"
	"sync"

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

	lock    sync.RWMutex
	clients map[logicalcluster.Path]client.Client
}

// New creates a new cluster-aware client.
func New(cfg *rest.Config, options client.Options) (ClusterClient, error) {
	return &clusterClient{
		opts:       options,
		baseConfig: cfg,
		clients:    make(map[logicalcluster.Path]client.Client),
	}, nil
}

func (c *clusterClient) Cluster(cluster logicalcluster.Path) client.Client {
	// quick path
	c.lock.RLock()
	cli, ok := c.clients[cluster]
	c.lock.RUnlock()
	if ok {
		return cli
	}

	// slow path
	c.lock.Lock()
	defer c.lock.Unlock()
	if cli, ok := c.clients[cluster]; ok {
		return cli
	}

	cfg := rest.CopyConfig(c.baseConfig)
	cfg.Host += cluster.RequestPath()
	cli, err := client.New(cfg, c.opts)
	if err != nil {
		panic(fmt.Errorf("failed to create client for cluster %s: %w", cluster, err))
	}
	c.clients[cluster] = cli
	return cli
}
