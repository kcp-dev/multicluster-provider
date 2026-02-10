/*
Copyright 2026 The KCP Authors.

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

	"github.com/davecgh/go-spew/spew"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/diff"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mctrl "sigs.k8s.io/multicluster-runtime"

	"github.com/kcp-dev/multicluster-provider/dynamic"
	"github.com/kcp-dev/multicluster-provider/pkg/handlers"
)

func main() {
	ctx := mctrl.SetupSignalHandler()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

var (
	fEndpointSliceGVK  = flag.String("gvk", "", "GVK of the EndpointSlice to watch")
	fEndpointSliceName = flag.String("name", "", "Name of the EndpointSlice to watch")
	fObjectToWatchGVK  = flag.String("vwgvk", "", "GVK of the object in the VW to watch")
	fDumpObjects       = flag.Bool("dump", false, "Dump objects")
)

type logger struct{}

func (logger) OnAdd(obj client.Object) {
	log.Printf("add %q in namespace %q", obj.GetName(), obj.GetNamespace())
	if *fDumpObjects {
		spew.Dump(obj)
	}
}

func (logger) OnUpdate(oldObj, newObj client.Object) {
	log.Printf("update %q in namespace %q", oldObj.GetName(), oldObj.GetNamespace())
	if *fDumpObjects {
		spew.Dump("old", oldObj)
		spew.Dump("new", newObj)
		fmt.Println(diff.Diff(oldObj, newObj))
	}
}

func (logger) OnDelete(obj client.Object) {
	log.Printf("delete %q in namespace %q", obj.GetName(), obj.GetNamespace())
	if *fDumpObjects {
		spew.Dump(obj)
	}
}

func run(ctx context.Context) error {
	flag.Parse()

	p, err := dynamic.New(mctrl.GetConfigOrDie(), dynamic.Options{
		EndpointSliceGVK:  parseKind(*fEndpointSliceGVK),
		EndpointSliceName: *fEndpointSliceName,
		ObjectToWatchGVK:  parseKind(*fObjectToWatchGVK),
		Handlers:          handlers.Handlers{&logger{}},
	})
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
