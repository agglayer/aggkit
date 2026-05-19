# Example configurations

This directory contains ready-to-use config files for known networks. Copy the one that matches your chain, then fill in the fields listed below before running the tool.

## Fields you must change

| Field | Why |
| ----- | --- |
| `l1RpcUrl` | Your L1 JSON-RPC endpoint. Required by Step E and Step I — without it the certificate will be incomplete. |
| `exitAddress` | The address that will receive assets locked in smart contracts. **You must hold the private key for this address** — funds can only be recovered by signing from it after the certificate settles. |
| `signerConfig` | Private key / KMS configuration used to sign the certificate in Step SIGN. |

For a full description of every config field and all supported signer backends (local keystore, GCP KMS, AWS KMS, …) see the [main README](../README.md).
