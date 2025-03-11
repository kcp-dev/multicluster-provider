# `apiexport` CRD Example Controller

This folder contains an example controller for the `virtualworkspace` provider implementation. It reconciles CustomResourceDefinition `Application` objects across kcp workspaces.

Skeleton created by running [kubebuilder](https://book.kubebuilder.io/quick-start.html) with the following command:

```sh
$ kubebuilder init --domain contrib.kcp.io --repo github.com/kcp-dev/multicluster-provider/examples/crd
$ kubebuilder create api --group apis --version v1alpha1 --kind Application
```

After than content of `cmd/main.go` was updated with multicluster extension.

It can be tested by applying the necessary manifests from the respective folder while connected to the `root` workspace of a kcp instance:

```sh
$ kubectl ws create provider --enter
$ kubectl apply -f ./config/kcp/apiresourceschema-applications.apis.contrib.kcp.io.yaml
$ kubectl apply -f ./config/kcp/apiexport-apis.contrib.kcp.io.yaml

# Consumer 1
$ kubectl ws use :root
$ kubectl ws create examples-crd-multicluster-1 --enter
$ kubectl kcp bind apiexport root:provider:apis.contrib.kcp.io 
$ kubectl apply -f ./config/samples/apis_v1alpha1_application.yaml

# Consumer 2
$ kubectl ws use :root
$ kubectl ws create examples-crd-multicluster-2 --enter
$ kubectl kcp bind apiexport root:provider:apis.contrib.kcp.io 
$ kubectl apply -f ./config/samples/apis_v1alpha1_application.yaml
```

Then, start the example controller by passing the virtual workspace URL to it:


```sh
$ kubectl kcp use :root:provider
$ go run . --server=$(kubectl get apiexport apis.contrib.kcp.io -o jsonpath="{.status.virtualWorkspaces[0].url}")
```

Observe the controller reconciling the ` application-sample` Aplication in the workspaces:

```sh
2025-03-11T13:04:52+02:00       INFO    Reconciling Application {"controller": "kcp-applications-controller", "controllerGroup": "apis.contrib.kcp.io", "controllerKind": "Application", "reconcileID": "babfc696-50cc-4851-ab35-d1d956a6c120", "cluster": "1058d5hgzdd3ask6"}
```
