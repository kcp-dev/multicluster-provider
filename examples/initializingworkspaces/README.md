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

Observe the controller reconciling every logical cluster and creating the child workspace `initialized-workspace` and the `kcp-initializer-cm` ConfigMap in each workspace and removing the initializer when done.

```sh
2025-07-24T10:26:58+02:00       INFO    Starting to initialize cluster  {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "0c69fc44-4886-42f8-b620-82a6fe96a165", "cluster": "1i6ttu8js47cs302"}
2025-07-24T10:26:58+02:00       INFO    Creating child workspace        {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "0c69fc44-4886-42f8-b620-82a6fe96a165", "cluster": "1i6ttu8js47cs302", "name": "initialized-workspace-1i6ttu8js47cs302"}
2025-07-24T10:26:58+02:00       INFO    kcp-initializing-workspaces-provider    disengaging non-initializing workspace  {"cluster": "1cxhyp0xy8lartoi"}
2025-07-24T10:26:58+02:00       INFO    Workspace created successfully  {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "0c69fc44-4886-42f8-b620-82a6fe96a165", "cluster": "1i6ttu8js47cs302", "name": "initialized-workspace-1i6ttu8js47cs302"}
2025-07-24T10:26:58+02:00       INFO    Reconciling ConfigMap   {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "0c69fc44-4886-42f8-b620-82a6fe96a165", "cluster": "1i6ttu8js47cs302", "name": "kcp-initializer-cm", "uuid": ""}
2025-07-24T10:26:58+02:00       INFO    ConfigMap created successfully  {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "0c69fc44-4886-42f8-b620-82a6fe96a165", "cluster": "1i6ttu8js47cs302", "name": "kcp-initializer-cm", "uuid": "9a8a8d5d-d606-4e08-bb69-679719d94867"}
2025-07-24T10:26:58+02:00       INFO    Removed initializer from LogicalCluster status  {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "0c69fc44-4886-42f8-b620-82a6fe96a165", "cluster": "1i6ttu8js47cs302", "name": "cluster", "uuid": "4c2fd3cf-512f-45f4-a9d3-6886c6542ccf"}
2025-07-24T10:26:58+02:00       INFO    Reconciling LogicalCluster      {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "99bd39f0-3ea8-4805-9770-60f95127b5ac", "cluster": "2hwz9858cyir31hl", "logical cluster": {"owner":{"apiVersion":"tenancy.kcp.io/v1alpha1","resource":"workspaces","name":"example1","cluster":"root","uid":"1d79b26f-cfb8-40d5-934d-b4a61eb20f12"},"initializers":["root:universal","root:examples-initializingworkspaces-multicluster","system:apibindings"]}}
2025-07-24T10:26:58+02:00       INFO    Starting to initialize cluster  {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "99bd39f0-3ea8-4805-9770-60f95127b5ac", "cluster": "2hwz9858cyir31hl"}
2025-07-24T10:26:58+02:00       INFO    Creating child workspace        {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "99bd39f0-3ea8-4805-9770-60f95127b5ac", "cluster": "2hwz9858cyir31hl", "name": "initialized-workspace-2hwz9858cyir31hl"}
2025-07-24T10:26:58+02:00       INFO    kcp-initializing-workspaces-provider    disengaging non-initializing workspace  {"cluster": "1i6ttu8js47cs302"}
2025-07-24T10:26:58+02:00       INFO    Workspace created successfully  {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "99bd39f0-3ea8-4805-9770-60f95127b5ac", "cluster": "2hwz9858cyir31hl", "name": "initialized-workspace-2hwz9858cyir31hl"}
2025-07-24T10:26:58+02:00       INFO    Reconciling ConfigMap   {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "99bd39f0-3ea8-4805-9770-60f95127b5ac", "cluster": "2hwz9858cyir31hl", "name": "kcp-initializer-cm", "uuid": ""}
2025-07-24T10:26:58+02:00       INFO    ConfigMap created successfully  {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "99bd39f0-3ea8-4805-9770-60f95127b5ac", "cluster": "2hwz9858cyir31hl", "name": "kcp-initializer-cm", "uuid": "87462d41-16b5-4617-9f7c-3894160576b7"}
2025-07-24T10:26:58+02:00       INFO    Removed initializer from LogicalCluster status  {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "99bd39f0-3ea8-4805-9770-60f95127b5ac", "cluster": "2hwz9858cyir31hl", "name": "cluster", "uuid": "cfeee05f-3cba-4766-b464-ba3ebe41a3fa"}
2025-07-24T10:26:58+02:00       INFO    Reconciling LogicalCluster      {"controller": "kcp-initializer-controller", "controllerGroup": "core.kcp.io", "controllerKind": "LogicalCluster", "reconcileID": "8ad43574-3862-4452-b1e0-e9daf1e67a54", "cluster": "2hwz9858cyir31hl", "logical cluster": {"owner":{"apiVersion":"tenancy.kcp.io/v1alpha1","resource":"workspaces","name":"example1","cluster":"root","uid":"1d79b26f-cfb8-40d5-934d-b4a61eb20f12"},"initializers":["root:universal","root:examples-initializingworkspaces-multicluster","system:apibindings"]}}
2025-07-24T10:26:59+02:00       INFO    kcp-initializing-workspaces-provider    disengaging non-initializing workspace  {"cluster": "2hwz9858cyir31hl"}
```
