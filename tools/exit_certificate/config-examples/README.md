# Example configurations

This directory contains ready-to-use config files (TOML) for known networks. Copy the one that matches your chain, then fill in the fields listed below before running the tool. (The tool also accepts JSON — the format is selected by the `.toml`/`.json` file extension.)

## Fields you must change

| Field | Why |
| ----- | --- |
| `l1RpcUrl` | Your L1 JSON-RPC endpoint. Required by Step E and Step I — without it the certificate will be incomplete. Use a **Sepolia** RPC for `zkevm-cardona.toml` and an **Ethereum mainnet** RPC for `zkevm-mainnet.toml`. |
| `exitAddress` | The address that will receive assets locked in smart contracts. **You must hold the private key for this address** — funds can only be recovered by signing from it after the certificate settles. |
| `options.agglayerClient.GRPC.URL` | Agglayer gRPC endpoint. Required for Steps H (PreviousLocalExitRoot), SUBMIT, and WAIT. Replace `<AGGLAYER_GRPC_URL>` with the actual address, e.g. `"agglayer.example.com:50051"`. |
| `signerConfig` | Private key / KMS configuration used to sign the certificate in Step SIGN. |

For a full description of every config field and all supported signer backends (local keystore, GCP KMS, AWS KMS, …) see the [main README](../README.md).
