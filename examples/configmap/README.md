# `apiexport` Example Controller

This folder contains an example controller for the `virtualworkspace` provider implementation. It reconciles `ConfigMap` objects across kcp workspaces.

It can be tested by applying the necessary manifests from the respective folder while connected to the `root` workspace of a kcp instance:

```sh
$ kubectl apply -f ./manifests/
apiexport.apis.kcp.io/examples-apiexport-multicluster created
workspacetype.tenancy.kcp.io/examples-apiexport-multicluster created
workspace.tenancy.kcp.io/example1 created
workspace.tenancy.kcp.io/example2 created
workspace.tenancy.kcp.io/example3 created
```

Then, start the example controller by passing the virtual workspace URL to it:

```sh
$ go run . --server=$(kubectl get apiexport examples-apiexport-multicluster -o jsonpath="{.status.virtualWorkspaces[0].url}")
```

Observe the controller reconciling the `kube-root-ca.crt` ConfigMap created in each workspace:

```sh
2025-03-06T20:16:18+01:00	INFO	Reconciling configmap	{"controller": "kcp-configmap-controller", "controllerGroup": "", "controllerKind": "ConfigMap", "reconcileID": "674a4e78-fec6-4e38-a6c2-0a8855259905", "cluster": "27uqz02z4wed6sjb", "name": "kube-root-ca.crt", "uuid": "4fb98e39-23f2-41b7-84a5-60163ca55148"}
2025-03-06T20:16:18+01:00	INFO	Reconciling configmap	{"controller": "kcp-configmap-controller", "controllerGroup": "", "controllerKind": "ConfigMap", "reconcileID": "913bccc0-bc3b-44b6-8508-8f84f6df5340", "cluster": "36ise8guls0p4mb9", "name": "kube-root-ca.crt", "uuid": "0dd90698-ff41-4216-84c3-64125b8dc32d"}
2025-03-06T20:16:18+01:00	INFO	Reconciling configmap	{"controller": "kcp-configmap-controller", "controllerGroup": "", "controllerKind": "ConfigMap", "reconcileID": "0aebc78a-a531-49e8-86c5-17a7407c57e2", "cluster": "1u97oi9csiunqu76", "name": "kube-root-ca.crt", "uuid": "c2094526-68f7-4743-bb6b-1492b2630419"}
```
