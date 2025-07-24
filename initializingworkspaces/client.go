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

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mcpcache "github.com/kcp-dev/multicluster-provider/internal/cache"
)

var _ client.Client = &Client{}

// Client routes LogicalCluster requests to wildcard cache and everything else to scoped client
type Client struct {
	client        client.Client
	wildcardCache mcpcache.WildcardCache
	scheme        *runtime.Scheme
}

// NewClient creates a new client
func newClient(client client.Client, wildcardCache mcpcache.WildcardCache, scheme *runtime.Scheme) *Client {
	return &Client{
		client:        client,
		wildcardCache: wildcardCache,
		scheme:        scheme,
	}
}

// isLogicalCluster checks if the object is a LogicalCluster
func (r *Client) isLogicalCluster(obj client.Object) bool {
	gvk := obj.GetObjectKind().GroupVersionKind()

	if gvk.Empty() {
		gvks, _, err := r.scheme.ObjectKinds(obj)
		if err != nil || len(gvks) == 0 {
			return false
		}
		gvk = gvks[0]
	}

	return gvk.Group == "core.kcp.io" &&
		gvk.Version == "v1alpha1" &&
		gvk.Kind == "LogicalCluster"
}

// isLogicalClusterList checks if the object is a LogicalClusterList
func (r *Client) isLogicalClusterList(obj client.ObjectList) bool {
	gvk := obj.GetObjectKind().GroupVersionKind()

	if gvk.Empty() {
		gvks, _, err := r.scheme.ObjectKinds(obj)
		if err != nil || len(gvks) == 0 {
			return false
		}
		gvk = gvks[0]
	}

	return gvk.Group == "core.kcp.io" &&
		gvk.Version == "v1alpha1" &&
		gvk.Kind == "LogicalClusterList"
}

// Get routes LogicalCluster to wildcard cache, everything else to scoped client
func (r *Client) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if r.isLogicalCluster(obj) {
		return r.wildcardCache.Get(ctx, key, obj, opts...)
	}
	return r.client.Get(ctx, key, obj, opts...)
}

// List routes LogicalCluster to wildcard cache, everything else to scoped client
func (r *Client) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if r.isLogicalClusterList(list) {
		return r.wildcardCache.List(ctx, list, opts...)
	}
	return r.client.List(ctx, list, opts...)
}

// Create always go to scoped client
func (r *Client) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	return r.client.Create(ctx, obj, opts...)
}

// Delete always goes to scoped client
func (r *Client) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return r.client.Delete(ctx, obj, opts...)
}

// Update always goes to scoped client
func (r *Client) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	return r.client.Update(ctx, obj, opts...)
}

// Patch always goes to scoped client
func (r *Client) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	return r.client.Patch(ctx, obj, patch, opts...)
}

// DeleteAllOf deletes all objects of the given type matching the given options.
func (r *Client) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	return r.client.DeleteAllOf(ctx, obj, opts...)
}

// Status returns the scoped client's status writer
func (r *Client) Status() client.StatusWriter {
	return r.client.Status()
}

// SubResource returns the scoped client's subresource client
func (r *Client) SubResource(subResource string) client.SubResourceClient {
	return r.client.SubResource(subResource)
}

// Scheme returns the scheme
func (r *Client) Scheme() *runtime.Scheme {
	return r.scheme
}

// RESTMapper returns the scoped client's REST mapper
func (r *Client) RESTMapper() meta.RESTMapper {
	return r.client.RESTMapper()
}

// GroupVersionKindFor returns GVK for the object
func (r *Client) GroupVersionKindFor(obj runtime.Object) (schema.GroupVersionKind, error) {
	return r.client.GroupVersionKindFor(obj)
}

// IsObjectNamespaced returns whether the object is namespaced
func (r *Client) IsObjectNamespaced(obj runtime.Object) (bool, error) {
	return r.client.IsObjectNamespaced(obj)
}
