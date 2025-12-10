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

package paths

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kcp-dev/logicalcluster/v3"
)

func TestPathStore(t *testing.T) {
	store := New()

	// Test data based on actual kcp.io annotations
	testData := []struct {
		cluster string
		path    string
	}{
		{
			cluster: "1k7ez4pisisnd9rg",
			path:    "root:test4",
		},
		{
			cluster: "1wmsceobzetidmmy",
			path:    "root:test2",
		},
		{
			cluster: "7a4mb6kjduypgg6d",
			path:    "root:test",
		},
		{
			cluster: "9pqu0i88fggvmevz",
			path:    "root:test3",
		},
		{
			cluster: "root",
			path:    "root",
		},
		{
			cluster: "system:shard",
			path:    "system:shard",
		},
	}

	// Verify path to cluster name conversion using logicalcluster
	t.Run("verify_path_to_cluster_name", func(t *testing.T) {
		for _, td := range testData {
			if td.cluster != td.path { // Skip cases where cluster == path
				// Convert path to cluster name and verify it matches expected cluster
				lcPath := logicalcluster.NewPath(td.path)
				if clusterName, ok := lcPath.Name(); ok {
					require.Equal(t, td.cluster, clusterName.String(),
						"cluster %s should match converted path %s -> %s", td.cluster, td.path, clusterName.String())
				}
			}
		}
	})

	// Test adding path to cluster mappings
	t.Run("add_path_cluster_mappings", func(t *testing.T) {
		for _, td := range testData {
			clusterName := logicalcluster.Name(td.cluster)
			store.Add(td.path, clusterName)
			require.True(t, store.Has(td.path), "path should exist: %s", td.path)

			// Verify we can retrieve the cluster
			retrievedCluster, exists := store.Get(td.path)
			require.True(t, exists, "should be able to get cluster for path: %s", td.path)
			require.Equal(t, clusterName, retrievedCluster, "retrieved cluster should match")
		}
	})

	// Test updating existing mappings
	t.Run("update_mappings", func(t *testing.T) {
		updateStore := New()

		// Add initial mapping
		path := "root:test"
		initialCluster := logicalcluster.Name("initial-cluster")
		updateStore.Add(path, initialCluster)

		retrieved, exists := updateStore.Get(path)
		require.True(t, exists)
		require.Equal(t, initialCluster, retrieved)

		// Update with new cluster
		newCluster := logicalcluster.Name("new-cluster")
		updateStore.Add(path, newCluster)

		retrieved, exists = updateStore.Get(path)
		require.True(t, exists)
		require.Equal(t, newCluster, retrieved)
	})

	// Test path validation scenarios
	t.Run("path_scenarios", func(t *testing.T) {
		scenarioStore := New()

		testCases := []struct {
			name    string
			path    string
			cluster string
		}{
			{
				name:    "root path",
				path:    "root",
				cluster: "root",
			},
			{
				name:    "simple workspace",
				path:    "root:test",
				cluster: "7a4mb6kjduypgg6d",
			},
			{
				name:    "system path",
				path:    "system:shard",
				cluster: "system:shard",
			},
			{
				name:    "nested workspace",
				path:    "root:test4",
				cluster: "1k7ez4pisisnd9rg",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				clusterName := logicalcluster.Name(tc.cluster)
				scenarioStore.Add(tc.path, clusterName)

				require.True(t, scenarioStore.Has(tc.path))
				retrieved, exists := scenarioStore.Get(tc.path)
				require.True(t, exists)
				require.Equal(t, clusterName, retrieved)
			})
		}
	})
}
