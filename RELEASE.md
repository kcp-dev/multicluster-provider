# Release Process

This document describes how releases are created for multicluster-provider.

## Versioning

The project follows [Semantic Versioning](https://semver.org/). All release tags use the `v` prefix (e.g., `v0.5.1`).

This repository contains two Go modules that are tagged independently:

| Module | Tag format | Example |
|--------|-----------|---------|
| `github.com/kcp-dev/multicluster-provider` (root) | `v<version>` | `v0.6.0` |
| `github.com/kcp-dev/multicluster-provider/client` | `client/v<version>` | `client/v0.6.0` |

Both modules must be tagged for each release.

## Release Branches

Release branches are maintained for each minor version:

- `release-0.2`, `release-0.3`, `release-0.4`, `release-0.5`, etc.

Patch releases are tagged from the corresponding release branch. New features target `main` and go into the next minor release.

## Creating a Release

### Minor Release (e.g., v0.6.0)

1. Create a release branch from `main`:
   ```sh
   git checkout main
   git pull upstream main
   git checkout -b release-0.6
   git push upstream release-0.6
   ```

2. Tag both modules (signed):
   ```sh
   git tag -s v0.6.0 -m "v0.6.0"
   git tag -s client/v0.6.0 -m "client/v0.6.0"
   git push upstream v0.6.0 client/v0.6.0
   ```

3. The GoReleaser workflow builds binaries for Linux and macOS (amd64/arm64) and creates a **draft** GitHub release.

4. Review the draft release on GitHub, edit release notes as needed, and publish it.

### Patch Release (e.g., v0.6.1)

1. Cherry-pick fixes into the release branch:
   ```sh
   git checkout release-0.6
   git cherry-pick <commit-sha>
   git push upstream release-0.6
   ```

2. Tag both modules and push (signed):
   ```sh
   git tag -s v0.6.1 -m "v0.6.1"
   git tag -s client/v0.6.1 -m "client/v0.6.1"
   git push upstream v0.6.1 client/v0.6.1
   ```

3. Review and publish the draft GitHub release.
