# exit_certificate_claimer scripts

Bash helpers that talk to the running [`exit_certificate_claimer`](../service) HTTP service
(API v2, `/claimer/v2`). They require `curl` and `jq`; `claim-asset.sh` additionally needs
[`cast`](https://book.getfoundry.sh/cast/) (foundry) to submit the transaction.

| Script | What it does |
| ------ | ------------ |
| [`list-bridges.sh`](list-bridges.sh) | Given an address, lists the bridge exits (deposits) associated with it via `GET /bridges`: the ones destined to it and the `Message` exits it is the beneficiary of. |
| [`claim-asset.sh`](claim-asset.sh) | Fetches the claim parameters for one deposit via `GET /claim-params` and submits the claim on L1 with the method the claimer returns in `claim_method`: `AgglayerBridge.claimAsset` (Transfer exit) or `AgglayerBridge.claimMessage` (Message exit). |
| [`claim-all.sh`](claim-all.sh) | Claims every pending deposit for all addresses of an exit run (the EOAs in `step-b-eoa-balances.json` plus the config's `exitAddress`), delegating each claim to `claim-asset.sh`. |

All scripts read the service base URL from `CLAIMER_URL` (default `http://localhost:8080`,
except `claim-all.sh` which defaults to `127.0.0.1:7080`).

## List the deposits of an address

```bash
./list-bridges.sh 0xAbC0000000000000000000000000000000000001
# against a remote service:
CLAIMER_URL=http://10.0.0.5:9090 ./list-bridges.sh 0xAbC...001
```

Each row shows the `deposit_count` you pass to `claim-asset.sh` and its `claim_method`. For a
`Message` exit decoded through a message route of the claimer, it also shows the beneficiary, the
decoded amount and the token it is paid in.

## Claim an asset

```bash
# 1. Preview the parameters and the exact cast command (no transaction):
DRY_RUN=1 ./claim-asset.sh 0xAbC...001 42

# 2. Submit the claim transaction (claimAsset or claimMessage, as the claimer says):
L1_RPC_URL=http://localhost:8545 \
BRIDGE_ADDRESS=0xYourAgglayerBridgeAddress \
PRIVATE_KEY=0xyourkey \
  ./claim-asset.sh 0xAbC...001 42
```

The address is the exit's destination address or, for a `Message` exit of a configured message
route, its beneficiary. `<deposit_count>` selects a single pending deposit (an address may have several). The script
prints the parameters and prompts for confirmation before sending; set `ASSUME_YES=1` to skip
the prompt.

| Env var | Required | Description |
| ------- | -------- | ----------- |
| `CLAIMER_URL` | no (default `http://localhost:8080`) | Claimer service base URL. |
| `L1_RPC_URL` | to submit | L1 RPC endpoint hosting `AgglayerBridge`. |
| `BRIDGE_ADDRESS` | to submit | `AgglayerBridge` contract address on L1. |
| `PRIVATE_KEY` | to submit | Signing key for the claim transaction. |
| `DRY_RUN` | no | `1` → only print params and the cast command. |
| `ASSUME_YES` | no | `1` → skip the confirmation prompt. |

> If the claimer returns `409 Conflict`, the certificate's local exit root is not yet settled on
> L1; wait for settlement and retry.

## Claim everything for an exit run

`claim-all.sh` reads an exit tool config file and claims every pending deposit for every
address it tracks: the EOAs in `<outputDir>/step-b-eoa-balances.json` plus the config's
`exitAddress` (where the smart-contract-locked funds land). `outputDir`, `l1RpcUrl`,
`l1BridgeAddress` and the local `signerConfig` keystore are all read from the config, so a
plain invocation usually needs no extra flags.

```bash
# Preview every claim without sending any transaction:
DRY_RUN=1 ./claim-all.sh

# Claim everything using the default config (tmp/exit_certificate-kurtosis.json):
./claim-all.sh

# A different config, against a remote claimer, without the up-front prompt:
CLAIMER_URL=http://10.0.0.5:7080 ASSUME_YES=1 ./claim-all.sh tmp/exit_certificate-cardona.json
```

| Env var | Required | Description |
| ------- | -------- | ----------- |
| `CLAIMER_URL` | no (default `127.0.0.1:7080`) | Claimer base URL; a missing scheme is assumed `http://`. |
| `L1_RPC_URL` | no (default config `.l1RpcUrl`) | L1 RPC endpoint hosting `AgglayerBridge`. |
| `BRIDGE_ADDRESS` | no (default config `.l1BridgeAddress`) | `AgglayerBridge` contract address on L1. |
| `PRIVATE_KEY` / `KEYSTORE` (+ `KEYSTORE_PASSWORD`) | no | Override the config's `signerConfig` signer. |
| `DRY_RUN` | no | `1` → only print params and the cast command for each claim. |
| `ASSUME_YES` | no | `1` → skip the single up-front confirmation prompt. |

`Message` exits are claimed with `claimMessage`. A message is listed both for its destination contract
and (with a message route) for its beneficiary; each deposit is claimed once per run, the first time
it shows up.

The script confirms once for the whole batch, then runs each `claim-asset.sh` non-interactively.
A failed claim (e.g. a `409` for an unsettled root) is logged as a warning and the run
continues; the script exits non-zero if any claim failed.
