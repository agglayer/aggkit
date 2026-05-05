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

## Commands

### Run full pipeline

```bash
./exit-certificate --config parameters.json
```

Runs all steps sequentially: 0 → A1 → A2 → B → C → D → E → F.

### Run a single step

```bash
./exit-certificate --config parameters.json --step <0|a1|a2|b|c|d|e|f>
```

Each step reads its dependencies from the output directory (files written by prior steps).

```bash
# Collect tx hashes only (fast, no tracing)
./exit-certificate --config parameters.json --step a1

# Trace the collected hashes (slow, requires debug RPC)
./exit-certificate --config parameters.json --step a2
```

### CLI flags

| Flag | Short | Default | Description |
| :--: | :---: | :-----: | :---------: |
| `--config` | `-c` | `parameters.json` | Path to the config file. |
| `--step` | — | `all` | Run a specific step (`0`, `a1`, `a2`, `b`, `c`, `d`, `e`, `f`) or `all`. |

## Pipeline steps

### Step 0 — Generate LBT (Local Balance Tree)

Scans the L2 bridge contract for `NewWrappedToken` events and fetches the `totalSupply` of each wrapped token at `targetBlock`. Also computes the unlocked native token balance and checks for WETH.

This step replaces the need for the external [`getLBT`](https://github.com/agglayer/agglayer-contracts/tree/v12.2.3/tools/getLBT) tool and the `lbtFile` config parameter. If `lbtFile` is already set and the file exists, this step is skipped and the pre-generated file is used instead.

**Output:** `step-0-lbt.json`

### Step A1 — Collect tx hashes

Phases 1 and 2 of Step A: fetches block headers to find non-empty blocks, then retrieves the full tx list for each. No tracing is performed.

1. Quick scan — `eth_getBlockByNumber` (headers only) to find non-empty blocks
2. Detail fetch — `eth_getBlockByNumber` (full txs) → tx hashes

**Output:** `step-a1-tx-hashes.json`

### Step A2 — Trace transactions

Phase 3 of Step A: reads the tx hashes produced by A1 and traces each one with `debug_traceTransaction` (prestateTracer, diffMode) to collect all pre/post addresses.

**Reads:** `step-a1-tx-hashes.json`

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

**Output:** `step-e-unclaimed-bridges.json`, `exit-certificate-final.json`

### Step F — Agglayer token balance verification

Queries the agglayer admin API (`admin_getTokenBalance`) for the L2 network and compares each token's total balance reported by agglayer against the sum of the corresponding `BridgeExit` amounts in the certificate. Any mismatch is logged as a warning with per-exit detail.

Skipped automatically when `agglayerAdminURL` is not set in options.

**Output:** `step-f-verification.json`

## Output

The final output is `exit-certificate-final.json` in the output directory. It is a standard agglayer `Certificate` JSON object with:

- `bridge_exits` — all value to be exited from the chain (EOA balances, SC-locked value, unclaimed L1→L2 deposits)
- `imported_bridge_exits` — unclaimed L1→L2 deposits represented as in-flight imports (from Step E, `claim_data` is `null`)

## Testing

From the repository root:

```bash
go test ./tools/exit_certificate/...
```
