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

/*
Package dyanmic provides a [sigs.k8s.io/multicluster-runtime] provider implementation for interacting
with any resource that has a list of endpoints at .status.endpoints, where each endpoint is a map with
at least the member "url" pointing at a virtual workspace.

This provider can be used for writing controllers that reconcile APIs exposed by an [kcp]-like *EndpointSlice object.

[kcp]: https://kcp.io
*/
package dynamic
