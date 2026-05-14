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
| `sovereignRollupAddr` | Yes* | Address of the `aggchainbase` contract on L1. Required by Step CHECK (network type and threshold verification). |
| `l1GlobalExitRootAddress` | Yes* | Address of `PolygonZkEVMGlobalExitRootV2` on L1. Required by Step I to fetch `L1InfoTreeLeafCount`. |
| `signerConfig` | No | Signer configuration object for Step SIGN. Same format as aggsender's `AggsenderPrivateKey`. Example: `{"Method": "local", "Path": "keystore.json", "Password": "pass"}`. |

> **\*Required for specific steps:** `sovereignRollupAddr` is required by Step CHECK; `l1GlobalExitRootAddress` is required by Step I. Without them those steps fail.

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
| `agglayerGrpcUrl` | `""` | Agglayer gRPC endpoint. Required for Steps H and SUBMIT. |
| `continueOnTraceError` | `false` | When `true`, Step A skips transactions whose `debug_traceTransaction` call fails instead of aborting. Failed tx hashes are saved to `step-a-failed-traces.json`. |
| `continueIfBalanceMismatch` | `false` | When `true`, Step F does not abort the pipeline on token balance mismatches. Instead it produces a capped certificate (`step-f-capped-certificate.json`) where each token's bridge exits are proportionally scaled down to `min(agglayer, lbt)`. See [Step F](#step-f--agglayer-token-balance-verification) for details. |

## Commands

### Run full pipeline

```bash
./exit-certificate --config parameters.json
```

Runs all steps sequentially: CHECK → 0 → A → B → C → D → E → F → G → H → I → SIGN (if `signerConfig` is set).

| Step | Name | What it does |
| :--: | ---- | ------------ |
| CHECK | Verify prerequisites | Checks Anvil, L1 RPC, network type (PP only), threshold = 1, no custom gas token. |
| 0 | Generate LBT | Scans `NewWrappedToken` events and fetches `totalSupply` per wrapped token at `targetBlock`. Skipped if `lbtFile` is set. |
| A | Collect addresses | Traces every L2 transaction via `debug_traceTransaction` and collects all addresses that touched state. |
| B | EOA balances | Classifies addresses as EOA vs contract; fetches ETH balance and every wrapped-token balance for each EOA at `targetBlock`. |
| C | SC-locked value | Computes value locked in contracts: `SC_locked = LBT_totalSupply − EOA_accumulated` per token. |
| D | Build certificate | Creates the `Certificate` with `BridgeExit` entries for every (EOA, token) pair and every token with SC-locked value. |
| E | Unclaimed deposits | Scans L1 for unclaimed `BridgeEvent` deposits targeting L2 and adds them as both `bridge_exits` and `imported_bridge_exits`. |
| F | Balance verification | Three-way comparison (LBT, agglayer, certificate) per token. Aborts on mismatch by default; with `continueIfBalanceMismatch=true` produces a proportionally capped certificate. |
| G | NewLocalExitRoot | Shadow-forks L2 at `targetBlock` via Anvil, replays all bridge exits, and reads the resulting `localExitRoot` from the forked bridge contract. |
| H | PreviousLocalExitRoot | Fetches `settled_ler` from the agglayer gRPC to obtain the previous LER and the next certificate height. |
| I | Assemble final cert | Applies `NewLocalExitRoot` (G), `PreviousLocalExitRoot` + height (H), bridge exit metadata, and `L1InfoTreeLeafCount` (from the latest `UpdateL1InfoTreeV2` event on L1). |
| SIGN | Sign certificate | Hashes the certificate and signs it with the configured keystore; wraps the signature in `AggchainDataMultisig`. |
| SUBMIT | Send to agglayer | Sends the signed certificate to the agglayer via gRPC. **Not part of the default pipeline.** |
| WAIT | Wait for settlement | Polls `GetCertificateHeader` every 5 s until the certificate is `Settled` or `InError`. **Not part of the default pipeline.** |

Steps SUBMIT and WAIT are **not** part of the default pipeline — they must be triggered explicitly.

### Run one or more steps

```bash
# Single step
./exit-certificate --config parameters.json --step h

# Multiple steps (comma-separated, run in the given order)
./exit-certificate --config parameters.json --step h,i,sign
./exit-certificate --config parameters.json --step "sign, submit"
```

Each step reads its dependencies from the output directory (files written by prior steps).
Spaces around commas are ignored. Execution stops at the first step that fails.

### CLI flags

| Flag | Short | Default | Description |
| :--: | :---: | :-----: | :---------: |
| `--config` | `-c` | `parameters.json` | Path to the config file. |
| `--step` | — | `all` | Step(s) to run: `all`, a single step name, or a comma-separated list (e.g. `h,i,sign`). Valid names: `check`, `0`, `a`–`i`, `sign`, `submit`, `wait`. |
| `--verbose` | — | `false` | Enable debug logging. Without this flag only `info`, `warn` and `error` messages are shown. |

## Pipeline steps

### Step CHECK — Verify prerequisites

Runs automatically as the first step of the full pipeline. Can also be run individually:

```bash
./exit-certificate --config parameters.json --step check
```

All checks run regardless of individual failures; a combined error lists every failed check.

1. **Anvil installed** — `anvil` must be in `$PATH` (required by Step G). Fails with a clear error pointing to [getfoundry.sh](https://getfoundry.sh) if missing.
2. **L1 RPC reachable** — dials `l1RpcUrl` and calls `eth_blockNumber`. Fails if not set or unreachable.
3. **L2 network ID matches bridge** — calls `NetworkID()` on the L2 bridge contract and verifies it matches `l2NetworkId` in config.
4. **`sovereignRollupAddr` is set** — required; fails if zero address.
5. **Network type is PP** — queries `AGGCHAINTYPE()` on the `aggchainbase` contract at `sovereignRollupAddr` on L1. FEP is not supported. Only runs if checks 2 and 4 passed.
6. **Threshold is 1** — queries the multisig threshold. Fails if > 1. Also verifies the bridge address on the contract matches config. Logs all committee signers and their URLs. Only runs if checks 2 and 4 passed.
7. **No custom gas token** — calls `gasTokenAddress()`/`gasTokenNetwork()` on the L2 bridge. Fails if a non-zero gas token is configured (not supported).

**Output:** `step-check-result.json`

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

Queries the agglayer admin API (`admin_getTokenBalance`) for the L2 network and performs a **three-way comparison** per token:

| Source | What it represents |
| ------ | ------------------ |
| **LBT** (Step 0) | `totalSupply` of the wrapped token at `targetBlock` — what the L2 contract holds |
| **Agglayer** | What the agglayer believes is locked for this L2 network |
| **Certificate** | Sum of all `BridgeExit` amounts for that token |

All three values must be equal. Each token is logged with ✅ or ❌:

```text
✅ (network=1 addr=0xabc...): lbt=1000  certificate=1000  agglayer=1000
❌ MISMATCH (network=1 addr=0xdef...): lbt=800  certificate=1000  agglayer=900
```

**If mismatches are found:**

- By default Step F **aborts the pipeline** with an error.
- Set `options.continueIfBalanceMismatch: true` to continue instead. In that case the step produces `step-f-capped-certificate.json`, where each mismatched token's bridge exits are proportionally scaled down to `min(agglayer, lbt)`. Subsequent steps in the pipeline (G, H, I) automatically use this capped certificate.

When running Step G individually it also prefers `step-f-capped-certificate.json` over `step-e-exit-certificate.json` if the capped file exists (logged with ⚠️).

LBT data comes from `step-0-lbt.json` (or `lbtFile`). If not available, the comparison falls back to two-way (certificate vs agglayer only).

Skipped automatically when `agglayerAdminURL` is not set in options.

**Reads:** `step-d-exit-certificate.json`, `step-0-lbt.json`

**Output:** `step-f-token-balances.json`, `step-f-checks.json`, `step-f-capped-certificate.json` *(only when mismatches exist and `continueIfBalanceMismatch=true`)*

### Step G — Compute NewLocalExitRoot (shadow-fork)

Computes the correct `new_local_exit_root` by replaying every `bridge_exit` from the certificate against a shadow-fork of the L2 chain via [Anvil](https://getfoundry.sh), then reading the resulting `localExitRoot` slot from the forked bridge contract.

**Anvil is a required external dependency** (`anvil` binary in `$PATH`). If missing, the step fails with a clear error. When the certificate has no bridge exits, Anvil is skipped and the canonical empty LER is used.

**Reads:** `step-f-capped-certificate.json` if it exists (produced by Step F when `continueIfBalanceMismatch=true`), otherwise `step-e-exit-certificate.json`.

**Output:** `step-g-new-local-exit-root.json`

### Step H — Fetch PreviousLocalExitRoot

Calls `interop_getNetworkInfo` on the agglayer JSON-RPC and reads the `settled_ler` for the L2 network. If no certificate has been settled yet, `PreviousLocalExitRoot` is zero.

Requires `agglayerGrpcUrl` in options.

**Output:** `step-h-previous-local-exit-root.json`

### Step I — Assemble final certificate

Reads the certificate from Step E and applies:

- `NewLocalExitRoot` from Step G
- `PreviousLocalExitRoot` and certificate height from Step H
- `L1InfoTreeLeafCount` — scans L1 backwards from the latest L1 block for the most recent `UpdateL1InfoTreeV2` event on the `l1GlobalExitRootAddress` contract. Requires `l1RpcUrl` and `l1GlobalExitRootAddress` in config.

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

### Step WAIT — Wait for certificate settlement

Polls the agglayer until the submitted certificate reaches a final state. **Not part of the default pipeline** — must be triggered with `--step wait`.

Requires `agglayerGrpcUrl` in options. Reads `step-submit-result.json` for the certificate hash.

Two phases:

1. If a different pending certificate is already in flight on the network, waits for it to settle (or enter error) before proceeding.
2. Polls `GetCertificateHeader` every 5 seconds until the submitted certificate is `Settled` or `InError`. Returns an error if `InError`.

**Reads:** `step-submit-result.json`

**Output:** `step-wait-result.json`

## Output

The final output is `exit-certificate-final.json` in the output directory. It is a standard agglayer `Certificate` JSON object with:

- `bridge_exits` — all value to be exited from the chain (EOA balances, SC-locked value, unclaimed L1→L2 deposits)
- `imported_bridge_exits` — unclaimed L1→L2 deposits represented as in-flight imports (from Step E, `claim_data` is `null`)

## Testing

From the repository root:

```bash
go test ./tools/exit_certificate/...
```
