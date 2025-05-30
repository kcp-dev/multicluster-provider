# kcp-dev/multicluster-provider

This repository contains an **experimental** provider implementation for [multicluster-runtime](https://github.com/multicluster-runtime/multicluster-runtime), a new [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) "addon" that allows writing uniform multi-cluster-aware Kubernetes controllers.

## Providers

This repository is expected to contain multiple [`Provider`](https://github.com/multicluster-runtime/multicluster-runtime/blob/223b19b990050e373880d57211c90ce86c53fd80/pkg/multicluster/multicluster.go#L52) implementations depending on how your controllers are supposed to interact with kcp.

Currently available are:

- [apiexport](./apiexport/): for interacting with the [`APIExport` virtual workspace](https://docs.kcp.io/kcp/latest/concepts/apis/exporting-apis/#build-your-controller) (or virtual workspaces with the same semantics).

## Examples

See [examples/apiexport](./examples/apiexport) for sample code.

## Contributing

Thanks for taking the time to start contributing!

### Before you start

* Please familiarize yourself with the [Code of Conduct](./CODE_OF_CONDUCT.md) before contributing.
* See [CONTRIBUTING.md](./CONTRIBUTING.md) for instructions on the developer certificate of origin that we require.

### Pull requests

* We welcome pull requests. Feel free to dig through existing [issues](https://github.com/kcp-dev/multicluster-provider/issues) and jump in.

## License

This project is licensed under [Apache-2.0](./LICENSE).
