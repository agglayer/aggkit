# exit-certificate

Generate exit certificates for a chain migration — scans L2 state, computes balances, and builds a certificate that bridges all value back to L1.

## Overview

**What it does:** The `exit-certificate` CLI scans an L2 chain from genesis to a target block, discovers all addresses with value, and produces an agglayer `Certificate` containing `BridgeExit` entries that transfer every balance (ETH + wrapped tokens) to the destination network. The certificate uses the native agglayer types directly — no conversion step is needed before submission.

**When to use it:** Use when an aggchain needs to exit the Agglayer ecosystem. The tool ensures all value on the L2 is accounted for and packaged into a single certificate.

## Known limitations

- **FEP (Finality by Execution Proof) is not supported.** The tool only handles Pessimistic Proof (PP) certificates. Chains running FEP mode cannot use this tool as-is.
- **`SetClaim` and `UpdatedUnsetGlobalIndexHashChain` events are not supported.** Transactions that emit these events on the bridge contract ([see contracts](https://github.com/agglayer/agglayer-contracts/tree/v12.2.3)) are not detected or accounted for. Value associated with these flows may be missing from the generated certificate.

## Quick start

```bash
cd tools/exit_certificate

# Build
go build -o exit-certificate ./cmd

# Create your config from the example
cp parameters.json.example parameters.json

# Edit parameters.json with your RPC URLs, bridge address, etc.
# Then run the tool
./exit-certificate --config parameters.json
```

## Building

From `tools/exit_certificate/`:

```bash
go build -o exit-certificate ./cmd
```

## Config file

The tool uses a standalone JSON config file. Copy the example and fill in your values:

```bash
cp parameters.json.example parameters.json
```

> **Note:** `parameters.json` and the `output/` directory are git-ignored — they are not committed to the repository.

### Config fields

| Field | Required | Description |
| :---: | :------: | :---------: |
| `l2RpcUrl` | Yes | L2 JSON-RPC endpoint. Must support `debug_traceTransaction` for Step A. |
| `l1RpcUrl` | No | L1 JSON-RPC endpoint. Required only for Step E (unclaimed bridge detection). |
| `l2BridgeAddress` | Yes | L2 bridge contract address. |
| `l1BridgeAddress` | No | L1 bridge contract address. Defaults to `l2BridgeAddress`. |
| `l2NetworkId` | No | L2 network ID. Defaults to `1`. |
| `targetBlock` | Yes | Target block number or `"latest"`. All state is captured at this block. |
| `exitAddress` | No | Address that receives SC-locked value exits. Defaults to zero address. |
| `lbtFile` | No | Path to a pre-generated LBT JSON file. If omitted, the tool generates it automatically via Step 0. Can also be generated externally with the [`getLBT`](https://github.com/agglayer/agglayer-contracts/tree/v12.2.3/tools/getLBT) tool from `agglayer-contracts`. |
| `destinationNetwork` | No | Destination network for bridge exits. Defaults to `0` (L1). |
| `signerConfig` | No | Signer configuration object for Step SIGN. Same format as aggsender's `AggsenderPrivateKey`. Example: `{"Method": "local", "Path": "keystore.json", "Password": "pass"}`. |

### Options

| Field | Default | Description |
| :---: | :-----: | :---------: |
| `blockRange` | `5000` | Block range per `eth_getLogs` query. |
| `concurrencyLimit` | `20` | Max concurrent RPC requests. |
| `rpcBatchSize` | `200` | Max calls per JSON-RPC batch request. |
| `rpcDelayMs` | `0` | Delay between RPC batches (rate limiting). |
| `outputDir` | `./output` | Directory for intermediate and final output files. Relative paths resolve from the config file directory. |
| `l1StartBlock` | `0` | L1 block to start scanning from (Step E). |
| `l2StartBlock` | `0` | L2 block to start scanning from (Step A). Useful when genesis activity can be skipped. |
| `agglayerAdminURL` | `""` | Agglayer admin RPC endpoint. Required for Step F. If omitted, Step F is skipped. |
| `agglayerRpcUrl` | `""` | Agglayer JSON-RPC endpoint. Required for Step H (`interop_getNetworkInfo`). |
| `agglayerGrpcUrl` | `""` | Agglayer gRPC endpoint. Required for Step SUBMIT. |
| `continueOnTraceError` | `false` | When `true`, Step A skips transactions whose `debug_traceTransaction` call fails instead of aborting. Failed tx hashes are saved to `step-a-failed-traces.json`. |

## Commands

### Run full pipeline

```bash
./exit-certificate --config parameters.json
```

Runs all steps sequentially: 0 → A → B → C → D → E → F → G → H → I → SIGN (if `signerConfig` is set).

Step SUBMIT is **not** part of the default pipeline — it must be triggered explicitly.

### Run a single step

```bash
./exit-certificate --config parameters.json --step <0|a|b|c|d|e|f|g|h|i|sign|submit>
```

Each step reads its dependencies from the output directory (files written by prior steps).

### CLI flags

| Flag | Short | Default | Description |
| :--: | :---: | :-----: | :---------: |
| `--config` | `-c` | `parameters.json` | Path to the config file. |
| `--step` | — | `all` | Run a specific step (`0`, `a`, `b`, `c`, `d`, `e`, `f`, `g`, `h`, `i`, `sign`, `submit`) or `all`. |

## Pipeline steps

### Step 0 — Generate LBT (Local Balance Tree)

Scans the L2 bridge contract for `NewWrappedToken` events and fetches the `totalSupply` of each wrapped token at `targetBlock`. Also computes the unlocked native token balance and checks for WETH.

This step replaces the need for the external [`getLBT`](https://github.com/agglayer/agglayer-contracts/tree/v12.2.3/tools/getLBT) tool and the `lbtFile` config parameter. If `lbtFile` is already set and the file exists, this step is skipped and the pre-generated file is used instead.

**Output:** `step-0-lbt.json`

### Step A — Collect addresses

Scans all blocks from `l2StartBlock` to `targetBlock` and collects every address that participated in any transaction, using `debug_traceTransaction` (prestateTracer, diffMode).

1. Scan — `eth_getBlockByNumber` (headers only, `false`) across all blocks → tx hashes are included directly in the response
2. Trace — `debug_traceTransaction` (prestateTracer, diffMode) per hash to extract pre/post state addresses

**Output:** `step-a-addresses.json`

### Step B — EOA balance checking

Classifies addresses as EOA vs contract, then queries ETH balance and every wrapped-token balance at `targetBlock` for all EOAs. The wrapped token list comes from the LBT data (Step 0 or `lbtFile`).

**Phases:**

1. `eth_getCode` to classify EOA vs contract
2. `eth_getBalance` for all EOAs
3. `balanceOf` calls per token across all EOAs (token list from LBT)

**Output:** `step-b-eoa-balances.json`, `step-b-accumulated.json`, `step-b-contract-addresses.json`

### Step C — SC-locked value extraction

Computes value locked in smart contracts using: `SC_locked = LBT_totalSupply - accumulated_EOA_balances`. Uses the LBT data (Step 0 or `lbtFile`) for total supply per token.

**Output:** `step-c-sc-locked-values.json`

### Step D — Build exit certificate

Creates the agglayer `Certificate` with `BridgeExit` entries for:

1. Every (EOA, token) pair with a non-zero balance → exits to the same address on the destination network
2. Every token with SC-locked value → exits to `exitAddress` on the destination network

**Output:** `step-d-exit-certificate.json`

### Step E — Unclaimed L1→L2 bridge deposits

Scans L1 for `BridgeEvent` events targeting the L2 and checks each deposit against `isClaimed` on the L2 bridge. Unclaimed deposits are added to the certificate in two ways:

- **`bridge_exits`** — the deposit value that must be exited from L2
- **`imported_bridge_exits`** — the in-flight L1→L2 claim, with `GlobalIndex{mainnet_flag: true, leaf_index: depositCount}` and `claim_data: null` (Merkle proofs are not available via plain RPC)

Requires `l1RpcUrl`.

**Output:** `step-e-unclaimed-bridges.json`, `step-e-exit-certificate.json`

### Step F — Agglayer token balance verification

Queries the agglayer admin API (`admin_getTokenBalance`) for the L2 network and compares each token's total balance reported by agglayer against the sum of the corresponding `BridgeExit` amounts in the certificate. Any mismatch is logged as a warning with per-exit detail.

Skipped automatically when `agglayerAdminURL` is not set in options.

**Reads:** `step-d-exit-certificate.json`

**Output:** `step-f-token-balances.json`, `step-f-checks.json`

### Step G — Compute NewLocalExitRoot (shadow-fork)

Computes the correct `new_local_exit_root` by replaying every `bridge_exit` from the certificate against a shadow-fork of the L2 chain via [Anvil](https://getfoundry.sh), then reading the resulting `localExitRoot` slot from the forked bridge contract.

**Anvil is a required external dependency** (`anvil` binary in `$PATH`). If missing, the step fails with a clear error. When the certificate has no bridge exits, Anvil is skipped and the canonical empty LER is used.

**Reads:** `step-e-exit-certificate.json`

**Output:** `step-g-new-local-exit-root.json`

### Step H — Fetch PreviousLocalExitRoot

Calls `interop_getNetworkInfo` on the agglayer JSON-RPC and reads the `settled_ler` for the L2 network. If no certificate has been settled yet, `PreviousLocalExitRoot` is zero.

Requires `agglayerRpcUrl` in options.

**Output:** `step-h-previous-local-exit-root.json`

### Step I — Assemble final certificate

Reads the certificate from Step E and applies `NewLocalExitRoot` (from Step G) and `PreviousLocalExitRoot` (from Step H).

**Reads:** `step-e-exit-certificate.json`, `step-g-new-local-exit-root.json`, `step-h-previous-local-exit-root.json`

**Output:** `exit-certificate-final.json`

### Step SIGN — Sign the certificate

Signs `exit-certificate-final.json` with the configured keystore and writes `exit-certificate-signed.json`. The signature is embedded in `AggchainData` as an `AggchainDataMultisig` ECDSA entry.

Requires `signerConfig` in config (same format as aggsender's `AggsenderPrivateKey`). Skipped automatically in `all` mode when `signerConfig` is not set.

**Reads:** `exit-certificate-final.json`

**Output:** `exit-certificate-signed.json`

### Step SUBMIT — Send certificate to agglayer

Sends `exit-certificate-signed.json` to the agglayer via gRPC and returns the certificate hash. **Not part of the default pipeline** — must be triggered with `--step submit`.

Requires `agglayerGrpcUrl` in options.

**Reads:** `exit-certificate-signed.json`

**Output:** `step-submit-result.json`

## Output

The final output is `exit-certificate-final.json` in the output directory. It is a standard agglayer `Certificate` JSON object with:

- `bridge_exits` — all value to be exited from the chain (EOA balances, SC-locked value, unclaimed L1→L2 deposits)
- `imported_bridge_exits` — unclaimed L1→L2 deposits represented as in-flight imports (from Step E, `claim_data` is `null`)

## Testing

From the repository root:

```bash
go test ./tools/exit_certificate/...
```
