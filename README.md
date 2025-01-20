<div id="top"></div>
<!-- PROJECT LOGO -->
<br />
<div align="center">

<img src="./.github/assets/aggkit-logo.svg#gh-light-mode-only" alt="Logo" width="100">
<img src="./.github/assets/aggkit-logo.svg#gh-dark-mode-only" alt="Logo" width="100">

## AggKit

**AggKit** is a modular framework that developers can use to connect networks to the AggLayer

</div>

<br />

## Getting Started

### Pre-requisites

Setup Kurtosis following these instructions: [Kurtosis CDK Getting Started](https://github.com/0xPolygon/kurtosis-cdk?tab=readme-ov-file#getting-started)

### Local Testing

- You can run locally against kurtosis-cdk environment using: [docs/local_debug.md](docs/local_debug.md)

### Build locally

You can locally build a production release of AggKit CLI + AggKit with:

```
make build
```

### Run locally

You can build and run a debug release locally using:

```
cargo run
```

It will build and run both binaries.
### Running with Kurtosis

1. Run your kurtosis environment
2. build `cdk-erigon` and make it available in your system's PATH
3. Run `scripts/local_config`
4. cargo run -- --config ./tmp/aggkit/local_config/test.kurtosis.toml --chain ./tmp/aggkit/local_config/genesis.json erigon

## Contributing

Contributions are very welcomed, the guidelines are currently not available (WIP)

## Support

Feel free to [open an issue](https://github.com/agglayer/aggkit/issues/new) if you have any feature request or bug report.<br />


## License

Copyright (c) 2024 PT Services DMCC

Licensed under either of

* Apache License, Version 2.0, ([LICENSE-APACHE](LICENSE-APACHE) or http://www.apache.org/licenses/LICENSE-2.0)
* MIT license ([LICENSE-MIT](LICENSE-MIT) or http://opensource.org/licenses/MIT)

at your option. 

The SPDX license identifier for this project is `MIT OR Apache-2.0`.
