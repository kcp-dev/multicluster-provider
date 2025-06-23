# `initializingworkspaces` Example Controller

This folder contains an example controller for the `initializingworkspaces` provider implementation. It reconciles `ConfigMap` objects across kcp workspaces.

It can be tested by applying the necessary manifests from the respective folder while connected to the `root` workspace of a kcp instance:

```sh
$ kubectl apply -f ./manifests/bundle.yaml
workspacetype.tenancy.kcp.io/examples-initializingworkspaces-multicluster created
workspace.tenancy.kcp.io/example1 created
workspace.tenancy.kcp.io/example2 created
workspace.tenancy.kcp.io/example3 created
```

Then, start the example controller by passing the virtual workspace URL to it:

```sh
$ go run . --server=$(kubectl get workspacetype examples-initializingworkspaces-multicluster -o jsonpath="{.status.virtualWorkspaces[0].url}") --initializer=root:examples-initializingworkspaces-multicluster
```

Observe the controller reconciling every logical cluster and creating the `kcp-initializer-cm` ConfigMap in each workspace and removing the initializer when done.

```sh
2025-06-23T16:33:24+02:00       INFO    Starting to initialize cluster  {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "f4149561-a2a0-40bf-bcdc-b124a3042d6f", "cluster": "10gwnu7yxzk58m2e"}
2025-06-23T16:33:24+02:00       INFO    Reconciling ConfigMap   {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "f4149561-a2a0-40bf-bcdc-b124a3042d6f", "cluster": "10gwnu7yxzk58m2e", "name": "kcp-initializer-cm", "uuid": "e1da25d0-fb6a-4486-953b-d5a3772c4241"}
2025-06-23T16:33:25+02:00       INFO    Removed initializer from LogicalCluster status  {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "f4149561-a2a0-40bf-bcdc-b124a3042d6f", "cluster": "10gwnu7yxzk58m2e", "name": "cluster", "uuid": "64b04afc-d04d-4842-8447-892491aec467"}
2025-06-23T16:33:25+02:00       INFO    Starting to initialize cluster  {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "f55046e7-36c6-40ee-a043-81e8891b5f37", "cluster": "2vjb557hetgz77w0"}
2025-06-23T16:33:25+02:00       INFO    kcp-initializing-workspaces-provider    disengaging initializing workspace      {"cluster": "10gwnu7yxzk58m2e"}
2025-06-23T16:33:25+02:00       INFO    Reconciling ConfigMap   {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "f55046e7-36c6-40ee-a043-81e8891b5f37", "cluster": "2vjb557hetgz77w0", "name": "kcp-initializer-cm", "uuid": "0a3d3f60-1c3e-4a45-bee5-29db902a062b"}
2025-06-23T16:33:25+02:00       INFO    Removed initializer from LogicalCluster status  {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "f55046e7-36c6-40ee-a043-81e8891b5f37", "cluster": "2vjb557hetgz77w0", "name": "cluster", "uuid": "994fba1b-7c79-4866-9408-c4a73cfa7351"}
2025-06-23T16:33:25+02:00       INFO    Starting to initialize cluster  {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "4371aedd-9405-45c6-b8c5-43399154c457", "cluster": "7zb1jyl4f5y4h65p"}
2025-06-23T16:33:25+02:00       INFO    kcp-initializing-workspaces-provider    disengaging initializing workspace      {"cluster": "2vjb557hetgz77w0"}
2025-06-23T16:33:25+02:00       INFO    Reconciling ConfigMap   {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "4371aedd-9405-45c6-b8c5-43399154c457", "cluster": "7zb1jyl4f5y4h65p", "name": "kcp-initializer-cm", "uuid": "8a922d1a-04a3-4848-b1e5-e90d2f5405d7"}
2025-06-23T16:33:25+02:00       INFO    Removed initializer from LogicalCluster status  {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "4371aedd-9405-45c6-b8c5-43399154c457", "cluster": "7zb1jyl4f5y4h65p", "name": "cluster", "uuid": "3f160828-89af-4ee7-ba5f-f65bd21d90da"}
2025-06-23T16:33:25+02:00       INFO    kcp-initializing-workspaces-provider    disengaging initializing workspace      {"cluster": "7zb1jyl4f5y4h65p"}
```
