/*
Copyright 2026 The kcp Authors.

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

package provider

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DefaultExtractURLsFromEndpointSlice extracts virtual workspace URLs from
// any object with a .status.endpoints[].url structure (e.g. APIExportEndpointSlice).
func DefaultExtractURLsFromEndpointSlice(obj client.Object) ([]string, error) {
	unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("unable to convert object to unstructured map: %w", err)
	}

	endpoints, found, err := unstructured.NestedSlice(unstructuredMap, "status", "endpoints")
	if err != nil {
		return nil, fmt.Errorf("error getting status.endpoints from unstructured map: %w", err)
	}
	if !found {
		return nil, nil
	}

	var urls []string
	for _, epRaw := range endpoints {
		ep, ok := epRaw.(map[string]any)
		if !ok {
			continue
		}
		url, _, err := unstructured.NestedString(ep, "url")
		if err != nil {
			return nil, fmt.Errorf("error getting url from endpoint %v: %w", ep, err)
		}
		if url != "" {
			urls = append(urls, url)
		}
	}
	return urls, nil
}

// ConditionReadyFunc returns a function that checks if the given object has a condition with the given type and status True.
func ConditionReadyFunc(conditionType string) func(client.Object) (bool, error) {
	return func(obj client.Object) (bool, error) {
		unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
		if err != nil {
			return false, fmt.Errorf("unable to convert object to unstructured map: %w", err)
		}

		conditions, found, err := unstructured.NestedSlice(unstructuredMap, "status", "conditions")
		if err != nil {
			return false, fmt.Errorf("error getting conditions from unstructured map: %w", err)
		}
		if !found {
			return false, nil
		}

		for _, condRaw := range conditions {
			cond, ok := condRaw.(map[string]any)
			if !ok {
				continue
			}

			condType, _, err := unstructured.NestedString(cond, "type")
			if err != nil {
				return false, fmt.Errorf("error getting type from unstructured condition %v: %w", cond, err)
			}

			condStatus, _, err := unstructured.NestedString(cond, "status")
			if err != nil {
				return false, fmt.Errorf("error getting status from unstructured condition %v: %w", cond, err)
			}

			if condType == conditionType && condStatus == string(corev1.ConditionTrue) {
				return true, nil
			}
		}

		return false, nil
	}
}
