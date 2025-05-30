/*
Copyright 2022 The KCP Authors.

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

	"k8s.io/apimachinery/pkg/api/meta"

	"github.com/kcp-dev/logicalcluster/v3"
)

// ClusterIndexFunc indexes by cluster name.
func ClusterIndexFunc(obj any) ([]string, error) {
	meta, err := meta.Accessor(obj)
	if err != nil {
		return []string{}, fmt.Errorf("object has no meta: %w", err)
	}
	return []string{ClusterIndexKey(logicalcluster.From(meta))}, nil
}

// ClusterIndexKey formats the index key for a cluster name.
func ClusterIndexKey(clusterName logicalcluster.Name) string {
	return clusterName.String()
}

// ClusterAndNamespaceIndexFunc indexes by cluster and namespace name.
func ClusterAndNamespaceIndexFunc(obj any) ([]string, error) {
	meta, err := meta.Accessor(obj)
	if err != nil {
		return []string{}, fmt.Errorf("object has no meta: %w", err)
	}
	return []string{ClusterAndNamespaceIndexKey(logicalcluster.From(meta), meta.GetNamespace())}, nil
}

// ClusterAndNamespaceIndexKey formats the index key for a cluster name and namespace.
func ClusterAndNamespaceIndexKey(clusterName logicalcluster.Name, namespace string) string {
	return clusterName.String() + "/" + namespace
}
