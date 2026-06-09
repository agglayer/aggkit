# Example configurations

This directory contains ready-to-use config files (TOML) for known networks. Copy the one that matches your chain, then fill in the fields listed below before running the tool. (The tool also accepts JSON — the format is selected by the `.toml`/`.json` file extension.)

## Fields you must change

| Field | Why |
| ----- | --- |
| `l1RpcUrl` | Your L1 JSON-RPC endpoint. Required by Step E and Step I — without it the certificate will be incomplete. Use a **Sepolia** RPC for `zkevm-cardona.toml` and an **Ethereum mainnet** RPC for `zkevm-mainnet.toml`. |
| `exitAddress` | The address that will receive assets locked in smart contracts. **Required** — and it **must not be the zero address** (`0x00…00`); the tool errors otherwise. **You must hold the private key for this address** — funds can only be recovered by signing from it after the certificate settles. **A multisig (e.g. a Gnosis Safe) is strongly recommended** instead of a single EOA, to avoid relying on one private key. |
| `l1GlobalExitRootAddress` | Address of `PolygonZkEVMGlobalExitRootV2` on L1. Required by Step I to fetch `L1InfoTreeLeafCount`. Replace the `<L1_GLOBAL_EXIT_ROOT_ADDRESS>` placeholder. |
| `options.agglayerClient.GRPC.URL` | Agglayer gRPC endpoint. Required for Steps H (PreviousLocalExitRoot), SUBMIT, and WAIT. Replace `<AGGLAYER_GRPC_URL>` with the actual address, e.g. `"agglayer.example.com:50051"`. |
| `signerConfig` | Private key / KMS configuration used to sign the certificate in Step SIGN. |
| `options.agglayerAdminURL` / `agglayerAdminToken` | Agglayer admin RPC and (when behind Google Cloud IAP) its Bearer token. Required for Step F. Replace the `<AGGLAYER_ADMIN_URL>` / `<JWT>` placeholders, or remove them to skip Step F. |

Every field in the example files is annotated with an inline comment describing it, whether it is required, and its default. For a full description of every config field and all supported signer backends (local keystore, GCP KMS, AWS KMS, …) see the [main README](../README.md).
