# `path-aware` Example Controller

This folder contains an example controller for the `virtualworkspace` provider implementation. It reconciles `ConfigMap` objects across kcp workspaces.

This is almost identical to the `apiexport` example controller, with the only difference being that it uses the `path-aware` provider implementation. This means that the controller is able to resolve logical clusters based on their paths (e.g. `root:example1`) instead of just their names (e.g. `27uqz02z4wed6sjb`).

It can be tested by applying the necessary manifests from the respective folder while connected to the `root` workspace of a kcp instance:

```sh
$ kubectl apply -f ./manifests/bundle.yaml
apiexport.apis.kcp.io/examples-apiexport-multicluster created
workspacetype.tenancy.kcp.io/examples-apiexport-multicluster created
workspace.tenancy.kcp.io/example1 created
workspace.tenancy.kcp.io/example2 created
workspace.tenancy.kcp.io/example3 created
```

Then, start the example controller by passing it the name of the APIExportEndpointSlice that kcp automatically created for us:

```sh
$ go run . --endpointslice=examples-apiexport-multicluster
```

Observe the controller reconciling the `kube-root-ca.crt` ConfigMap created in each workspace:

```sh
...
2025-12-10T14:56:00+02:00       INFO    APIBinding logical cluster path {"controller": "kcp-configmap-controller", "controllerGroup": "", "controllerKind": "ConfigMap", "reconcileID": "377912b8-9086-4340-9bcc-5b091fe0a000", "cluster": "4dn4arxjmfwfiag4", "path": "root:example3"}
2025-12-10T14:56:00+02:00       INFO    Found ConfigMap via path lookup {"controller": "kcp-configmap-controller", "controllerGroup": "", "controllerKind": "ConfigMap", "reconcileID": "377912b8-9086-4340-9bcc-5b091fe0a000", "cluster": "4dn4arxjmfwfiag4", "name": "kube-root-ca.crt", "in cluster": "root:example3"}
```
