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
	"reflect"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/jsonpath"
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

// ExtractURLsByJSONPath extracts virtual workspace URLs form any object
// given the passed JSON path, e.g. `{.status.endpoints[*].url}`
func ExtractURLsByJSONPath(path string) (URLExtractor, error) {
	jp := jsonpath.New("")
	if err := jp.Parse(path); err != nil {
		return nil, fmt.Errorf("invalid jsonpath expression %q: %w", path, err)
	}

	return func(obj client.Object) ([]string, error) {
		unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
		if err != nil {
			return nil, fmt.Errorf("unable to convert object to unstructured map: %w", err)
		}

		results, err := jp.FindResults(unstructuredMap)
		if err != nil {
			return nil, fmt.Errorf("jsonpath %q failed: %w", path, err)
		}

		var urls []string
		for _, result := range results {
			for _, v := range result {
				if v.Kind() == reflect.String {
					if s := v.String(); s != "" {
						urls = append(urls, s)
					}
				} else if v.Kind() == reflect.Interface {
					if s, ok := v.Interface().(string); ok && s != "" {
						urls = append(urls, s)
					}
				}
			}
		}
		return urls, nil
	}, nil
}
