# exit_certificate_claimer scripts

Bash helpers that talk to the running [`exit_certificate_claimer`](../service) HTTP service
(`/claimer/v1`). They require `curl` and `jq`; `claim-asset.sh` additionally needs
[`cast`](https://book.getfoundry.sh/cast/) (foundry) to submit the transaction.

| Script | What it does |
| ------ | ------------ |
| [`list-bridges.sh`](list-bridges.sh) | Given a destination address, lists the bridge exits (deposits) associated with it via `GET /bridges`. |
| [`claim-asset.sh`](claim-asset.sh) | Fetches the `claimAsset` parameters for one deposit via `GET /claim-params` and submits `AgglayerBridge.claimAsset` on L1. |

All scripts read the service base URL from `CLAIMER_URL` (default `http://localhost:8080`).

## List the deposits of an address

```bash
./list-bridges.sh 0xAbC0000000000000000000000000000000000001
# against a remote service:
CLAIMER_URL=http://10.0.0.5:9090 ./list-bridges.sh 0xAbC...001
```

Each row shows the `deposit_count` you pass to `claim-asset.sh`.

## Claim an asset

```bash
# 1. Preview the parameters and the exact cast command (no transaction):
DRY_RUN=1 ./claim-asset.sh 0xAbC...001 42

# 2. Submit the claimAsset transaction:
L1_RPC_URL=http://localhost:8545 \
BRIDGE_ADDRESS=0xYourAgglayerBridgeAddress \
PRIVATE_KEY=0xyourkey \
  ./claim-asset.sh 0xAbC...001 42
```

`<deposit_count>` selects a single pending deposit (an address may have several). The script
prints the parameters and prompts for confirmation before sending; set `ASSUME_YES=1` to skip
the prompt.

| Env var | Required | Description |
| ------- | -------- | ----------- |
| `CLAIMER_URL` | no (default `http://localhost:8080`) | Claimer service base URL. |
| `L1_RPC_URL` | to submit | L1 RPC endpoint hosting `AgglayerBridge`. |
| `BRIDGE_ADDRESS` | to submit | `AgglayerBridge` contract address on L1. |
| `PRIVATE_KEY` | to submit | Signing key for the `claimAsset` transaction. |
| `DRY_RUN` | no | `1` → only print params and the cast command. |
| `ASSUME_YES` | no | `1` → skip the confirmation prompt. |

> If the claimer returns `409 Conflict`, the certificate's local exit root is not yet settled on
> L1; wait for settlement and retry.
