# LogicalCluster to workspace hierarchy Example

This example demonstrates how to resolve a `LogicalCluster` to a `Workspace` using the kcp API and
walk up the workspace hierarchy tree, displaying parent workspace information. This might be useful
if one stores metadata in a parent workspace and wants to access it.

But one needs to be careful when doing this, as walking up the hierarchy can be expensive if done
frequently, and workspaces are located on different shards, so each step up the hierarchy might
require a network call. In addition to fetching the workspace object (namespace in this example), `kcpkubernetesClient`
needs to point to the front-proxy so cross-shard calls can be made.

Consider the architecture, where a resolution controller, based on the example here, runs one instance per shard,
and resolves parent workspace data to get metadata. If this is done often and on each reconcile loop - this
will soon become a bottleneck for this reconciler. So while possible, this type of architecture needs to be implemented with caution.

## Setup

### 1. Create a provider workspace

```bash
kubectl ws create provider --enter
```

### 2. Create an APIExport in the provider workspace

```yaml
kubectl apply -f - <<EOF
apiVersion: apis.kcp.io/v1alpha2
kind: APIExport
metadata:
  name: logicalcluster.workspaces.kcp.dev
spec:
  permissionClaims:
  - group: core.kcp.io
    resource: logicalclusters
    verbs: ["*"]
EOF
```

### 3. Create consumer workspaces (3 nested workspaces)

```bash
kubectl ws create consumer1 --enter
kubectl kcp bind apiexport root:provider:logicalcluster.workspaces.kcp.dev --name logicalcluster --accept-permission-claim logicalclusters.core.kcp.io

kubectl ws create consumer2 --enter
kubectl kcp bind apiexport root:provider:logicalcluster.workspaces.kcp.dev --name logicalcluster --accept-permission-claim logicalclusters.core.kcp.io

kubectl ws create consumer3 --enter
kubectl kcp bind apiexport root:provider:logicalcluster.workspaces.kcp.dev --name logicalcluster --accept-permission-claim logicalclusters.core.kcp.io
```

## Running the Example

### 1. Switch to the provider workspace

```bash
kubectl ws use root:provider
```

### 2. Run the example

```bash
go run . --server=$(kubectl get apiexportendpointslice logicalcluster.workspaces.kcp.dev -o jsonpath="{.status.endpoints[0].url}")
```

## What it does

The example controller:
- Watches for `LogicalCluster` resources
- Extracts the workspace path from the LogicalCluster annotation
- Walks up the workspace hierarchy to collect parent workspace information. Assuming one can talk to the front proxy.
- Displays the hierarchy in a tree format showing workspace paths, namespace names, and UUIDs
