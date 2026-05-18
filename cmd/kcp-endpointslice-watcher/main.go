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

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/go-logr/logr"
	"github.com/shibukawa/cdiff"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	mctrl "sigs.k8s.io/multicluster-runtime"

	"github.com/kcp-dev/multicluster-provider/pkg/handlers"
	"github.com/kcp-dev/multicluster-provider/pkg/provider"
)

func main() {
	flag.Parse()
	ctrl.SetLogger(logr.Discard())
	ctx := mctrl.SetupSignalHandler()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

var (
	fEndpointSliceGVK         = flag.String("es-gvk", "", "GVK of the EndpointSlice to watch")
	fEndpointSliceName        = flag.String("es-name", "", "Name of the EndpointSlice to watch")
	fEndpointSliceURLJsonPath = flag.String("es-url-path", "", "JSONPath expression to extract URLs from the EndpointSlice (e.g. '{.status.endpoints[*].url}')")
	fObjectToWatchGVK         = flag.String("vw-gvk", "", "GVK of the object in the VW to watch")
	fDiffObjects              = flag.Bool("diff", false, "Print a coloured diff of objects as they are added/changed/removed")
)

type logger struct{}

func (logger) OnAdd(obj client.Object) {
	fmt.Printf("add %q in namespace %q\n", obj.GetName(), obj.GetNamespace())
	if *fDiffObjects {
		objYAML, _ := yaml.Marshal(obj)
		result := cdiff.Diff("", string(objYAML), cdiff.WordByWord)
		fmt.Println(result.UnifiedWithGooKitColor("", "new", 3, cdiff.GooKitColorTheme))
	}
}

func (logger) OnUpdate(oldObj, newObj client.Object) {
	fmt.Printf("update %q in namespace %q\n", oldObj.GetName(), oldObj.GetNamespace())
	if *fDiffObjects {
		oldYAML, _ := yaml.Marshal(oldObj)
		newYAML, _ := yaml.Marshal(newObj)
		result := cdiff.Diff(string(oldYAML), string(newYAML), cdiff.WordByWord)
		fmt.Println(result.UnifiedWithGooKitColor("old", "new", 3, cdiff.GooKitColorTheme))
	}
}

func (logger) OnDelete(obj client.Object) {
	fmt.Printf("delete %q in namespace %q\n", obj.GetName(), obj.GetNamespace())
	if *fDiffObjects {
		objYAML, _ := yaml.Marshal(obj)
		result := cdiff.Diff(string(objYAML), "", cdiff.WordByWord)
		fmt.Println(result.UnifiedWithGooKitColor("old", "", 3, cdiff.GooKitColorTheme))
	}
}

func run(ctx context.Context) error {
	endpointSliceObj := &unstructured.Unstructured{}
	endpointSliceObj.SetGroupVersionKind(parseKind(*fEndpointSliceGVK))

	objectToWatch := &unstructured.Unstructured{}
	objectToWatch.SetGroupVersionKind(parseKind(*fObjectToWatchGVK))

	opts := provider.Options{
		EndpointSliceObject: endpointSliceObj,
		ObjectToWatch:       objectToWatch,
		Handlers:            handlers.Handlers{&logger{}},
	}

	if *fEndpointSliceURLJsonPath != "" {
		urlExtractor, err := provider.ExtractURLsByJSONPath(*fEndpointSliceURLJsonPath)
		if err != nil {
			return fmt.Errorf("error building url extractor: %w", err)
		}
		opts.ExtractURLsFromEndpointSlice = urlExtractor
	}

	p, err := provider.NewProvider(mctrl.GetConfigOrDie(), *fEndpointSliceName, opts)
	if err != nil {
		return err
	}

	return p.Start(ctx, nil)
}

// from https://github.com/platform-mesh/resource-broker/blob/fdf5e17850b4cf8d9d1468ac4620a2280d6ff789/pkg/broker/utils.go#L36-L49
func parseKind(kind string) schema.GroupVersionKind {
	if strings.HasSuffix(kind, ".core") {
		split := strings.SplitN(kind, ".", 3)
		return schema.GroupVersionKind{Group: "", Version: split[1], Kind: split[0]}
	}
	gvk, _ := schema.ParseKindArg(kind)
	if gvk == nil {
		return schema.GroupVersionKind{}
	}
	return *gvk
}
