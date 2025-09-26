# LogicalCluster to workspace hierarchy Example

This example demonstrates how to resolve a `LogicalCluster` to a `Workspace` using the KCP API and
walk up the workspace hierarchy tree, displaying parent workspace information. This might be useful
if one stores metadata in parent workspace and wants to access it.

But one needs to be careful when doing this, as walking up the hierarchy can be expensive if done
frequently, and workspaces are located on different shards, so each step up the hierarchy might
require a network call.

## Setup

### 1. Create a provider workspace

```bash
kubectl ws create provider --enter
```

### 2. Create an APIExport in the provider workspace

```yaml
kubectl apply -f - <<EOF
apiVersion: apis.kcp.io/v1alpha1
kind: APIExport
metadata:
  name: logicalcluster.workspaces.kcp.dev
spec:
  permissionClaims:
  - all: true
    group: core.kcp.io
    resource: logicalclusters
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
- Walks up the workspace hierarchy to collect parent workspace information
- Displays the hierarchy in a tree format showing workspace paths, namespace names, and UUIDs
