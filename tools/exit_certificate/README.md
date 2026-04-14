# exit-certificate

Generate exit certificates for a chain migration — scans L2 state, computes balances, and builds a certificate that bridges all value back to L1.

## Overview

**What it does:** The `exit-certificate` CLI scans an L2 chain from genesis to a target block, discovers all addresses with value, and produces an agglayer `Certificate` containing `BridgeExit` entries that transfer every balance (ETH + wrapped tokens) to the destination network. The certificate uses the native agglayer types directly — no conversion step is needed before submission.

**When to use it:** Use when an aggchain needs to exit the Agglayer ecosystem. The tool ensures all value on the L2 is accounted for and packaged into a single certificate.

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

## Commands

### Run full pipeline

```bash
./exit-certificate --config parameters.json
```

Runs all steps sequentially: 0 → A → B → C → D → E.

### Run a single step

```bash
./exit-certificate --config parameters.json --step <0|a|b|c|d|e>
```

Each step reads its dependencies from the output directory (files written by prior steps).

### CLI flags

| Flag | Short | Default | Description |
| :--: | :---: | :-----: | :---------: |
| `--config` | `-c` | `parameters.json` | Path to the config file. |
| `--step` | — | `all` | Run a specific step (`0`, `a`, `b`, `c`, `d`, `e`) or `all`. |

## Pipeline steps

### Step 0 — Generate LBT (Local Balance Tree)

Scans the L2 bridge contract for `NewWrappedToken` events and fetches the `totalSupply` of each wrapped token at `targetBlock`. Also computes the unlocked native token balance and checks for WETH.

This step replaces the need for the external [`getLBT`](https://github.com/agglayer/agglayer-contracts/tree/v12.2.3/tools/getLBT) tool and the `lbtFile` config parameter. If `lbtFile` is already set and the file exists, this step is skipped and the pre-generated file is used instead.

**Output:** `step-0-lbt.json`

### Step A — Collect touched addresses

Scans all blocks from genesis to `targetBlock` and traces every transaction with `debug_traceTransaction` (prestateTracer, diffMode) to discover all addresses that were read or written.

**Phases:**
1. Quick scan — fetch block headers to find non-empty blocks
2. Detail fetch — get full tx objects for non-empty blocks → tx hashes
3. Trace — `debug_traceTransaction` → pre/post addresses

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

Scans L1 for `BridgeEvent` events targeting the L2, compares with L2 `ClaimEvent` data, and adds unclaimed deposits as additional bridge exits in the certificate. Requires `l1RpcUrl`.

**Output:** `step-e-unclaimed-bridges.json`, `exit-certificate-final.json`

## Output

The final output is `exit-certificate-final.json` in the output directory. It is a standard agglayer `Certificate` JSON object with `bridge_exits` containing all the value to be exited from the chain.

## Testing

From the repository root:

```bash
go test ./tools/exit_certificate/...
```
