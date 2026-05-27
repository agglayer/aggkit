# exit_certificate tool — spec for AI assistants

## Purpose

Standalone CLI tool that generates an agglayer `Certificate` for an L2 chain exiting the Agglayer
ecosystem. It scans L2 state from genesis to a target block, computes all balances, and produces
a certificate containing `BridgeExit` entries that transfer every balance (ETH + wrapped tokens)
to the destination network.

## Package layout

```text
tools/exit_certificate/
├── cmd/main.go          — CLI entry point (urfave/cli/v2)
├── run.go               — pipeline orchestration (Run, runAll, runSingleStep)
├── config.go            — Config, Options, LoadConfig, LBT file parsing
├── types.go             — all domain types (StepXResult, LBTEntry, EOABalance, …)
├── rpc.go               — raw JSON-RPC helpers (singleRPC, concurrentBatchRPC, …)
├── worker.go            — generic worker pool (runWorkerPool)
├── hex.go               — hex/uint64 conversion utilities
├── step_0.go            — LBT generation
├── step_a.go            — address collection via debug_traceTransaction
├── step_b.go            — EOA classification + balance fetching
├── step_c.go            — SC-locked value computation
├── step_d.go            — build agglayer Certificate
├── step_e.go            — unclaimed L1→L2 deposits
├── step_f.go            — agglayer token balance verification
├── step_g.go            — NewLocalExitRoot computation
├── step_h.go            — fetch PreviousLocalExitRoot from agglayer
├── step_i.go            — assemble final certificate (LER, prev LER, L1InfoTreeLeafCount)
├── step_check.go        — prerequisite checks (Anvil, L1 RPC, network type, threshold, gas token)
├── step_sign.go         — ECDSA certificate signing
├── step_submit.go       — send certificate to agglayer via gRPC
├── step_wait.go         — poll agglayer until certificate is settled or in error
└── parameters.json.example
```

## Pipeline

Full pipeline order (`runAll`): **CHECK → 0 → A → B → C → D → E → F → G → H → I → SIGN**

Post-submission steps (explicit only, not part of `runAll`): **SUBMIT → WAIT**

Each step reads its inputs from disk (output dir) and writes its outputs to disk. The
`runAll` path passes data in memory directly; `runSingleStep` always loads from disk.

### Step CHECK — Verify prerequisites

Runs automatically as the first step of the full pipeline, and can also be triggered individually with `--step check`.

All checks run regardless of individual failures. A combined error lists every failed check.

1. **Anvil installed** — `anvil` must be in `$PATH` (required by Step G). Fails with a clear error pointing to [getfoundry.sh](https://getfoundry.sh) if missing.
2. **L1 RPC reachable** — dials `l1RpcUrl` and calls `eth_blockNumber`. Fails if not set or unreachable.
3. **L2 network ID matches bridge** — calls `NetworkID()` on the L2 bridge contract and verifies it matches `l2NetworkId` in config.
4. **`sovereignRollupAddr` is set** — required; fails if zero address.
5. **Network type is PP** — queries `AGGCHAINTYPE()` on the `aggchainbase` contract at `sovereignRollupAddr` on L1. Fails if FEP. Only runs if checks 2 and 4 passed.
6. **Threshold is 1** — queries `Threshold()` and `GetAggchainSignerInfos()`. Fails if threshold > 1. Also verifies the bridge address on the contract matches config. Only runs if checks 2 and 4 passed.
7. **No custom gas token** — calls `gasTokenAddress()`/`gasTokenNetwork()` on the L2 bridge. Fails if a non-zero gas token is set (not supported).

- **Output:** `step-check-result.json` (`StepCheckResult`)

### Step 0 — Generate LBT

- **Trigger:** always runs as part of the full pipeline.
- **Does:** first resolves `targetBlock` (finality keyword, optional offset, or concrete number) to a `uint64` via an RPC call when needed; then scans L2 bridge `NewWrappedToken` events, fetches `totalSupply` per token at the resolved block, computes unlocked native balance.
- **Output:** `step-0-l2_target_block.json` (resolved block number as `uint64`), `step-0-lbt.json` (`[]LBTEntry`)

### Step A — Collect addresses

- **RPC:** `eth_getBlockByNumber` (headers, `false`) → tx hashes; then `debug_traceTransaction` with `prestateTracer`+`diffMode` per hash.
- **Output:** `step-a-addresses.json` (`[]common.Address`), `step-a-failed-traces.json` (`[]common.Hash`)
- **Option:** `continueOnTraceError=true` skips failed traces instead of aborting.

### Step B — EOA balance checking

Three phases:

1. `eth_getCode` → classify each address as EOA or contract
2. `eth_getBalance` for all EOAs at `targetBlock`
3. `balanceOf(address)` per wrapped token × per EOA (token list from LBT)

- **Output:** `step-b-eoa-balances.json` (`[]EOABalance`), `step-b-accumulated.json` (`[]AccumulatedBalance`), `step-b-contract-addresses.json` (`[]common.Address`)

### Step C — SC-locked value

- **Formula:** `SC_locked = LBT_totalSupply − accumulated_EOA_balances` per token.
- **Output:** `step-c-sc-locked-values.json` (`[]SCLockedValue`)

### Step D — Build certificate

Creates the `*agglayertypes.Certificate` with `BridgeExit` entries:

- One per (EOA, token) pair with non-zero balance → destination is the EOA address on `destinationNetwork`.
- One per token with SC-locked value > 0 → destination is `exitAddress` on `destinationNetwork`.

- **Output:** `step-d-exit-certificate.json`

### Step E — Unclaimed L1→L2 deposits

- **Requires:** `l1RpcUrl` (skipped otherwise).
- Scans L1 `BridgeEvent` events targeting L2 network, checks each deposit against `isClaimed` on L2 bridge.
- Splits unclaimed deposits by leaf type: **assets** (`leaf_type=0`) are added to the certificate as `bridge_exits` + `imported_bridge_exits` (with `claim_data: null`); **messages** (`leaf_type=1`) are excluded from the certificate and saved separately.
- **Bridge service cross-check:** when `options.bridgeServiceURL` is set, compares the detected unclaimed asset set against the bridge service's pending-bridges and errors on any discrepancy. Controlled by `options.bridgeServiceType` (`"aggkit"` → `GET /bridge/v1/bridges`; `"zkevm"` → `GET /pending-bridges`).
- **Output:** `step-e-unclaimed-bridges.json` (`[]L1Deposit`), `step-e-unclaimed-messages.json` (`[]L1Deposit`, always written), `step-e-exit-certificate.json`

### Step F — Agglayer balance verification

- **Requires:** `agglayerAdminURL` in options (skipped otherwise).
- Calls `admin_getTokenBalance` on the agglayer admin RPC and performs a **three-way comparison** per token: `LBT (Step 0) == agglayer == certificate sum`. Each token is logged with ✅ or ❌.
- **LBT data:** loaded from `step-0-lbt.json`. If unavailable, falls back to two-way comparison (certificate vs agglayer).
- **On mismatch:** aborts the pipeline with an error by default.
- **`continueIfBalanceMismatch=true`:** suppresses the error and produces `step-f-capped-certificate.json`, where each mismatched token's bridge exits are proportionally scaled down to `min(agglayer, lbt)`. The pipeline (and `runSingleG`) automatically uses this capped certificate for subsequent steps.
- `buildCapMap` / `capBridgeExits` are the internal helpers for computing and applying the caps. Proportional scaling preserves the exact capped total by adding any integer-division remainder to the last exit of each group.
- **Output:** `step-f-token-balances.json`, `step-f-checks.json` (`[]TokenBalanceCheck`), `step-f-capped-certificate.json` *(only when `continueIfBalanceMismatch=true` and mismatches exist)*

### Step G — Compute NewLocalExitRoot (shadow-fork)

> **Input priority (single-step mode):** uses `step-f-capped-certificate.json` if it exists (logged with ⚠️), otherwise falls back to `step-e-exit-certificate.json`. In `runAll` the in-memory certificate already reflects any capping done by Step F.

Computes the correct `NewLocalExitRoot` by replaying every `bridge_exit` from the certificate
against a shadow-fork of the L2 chain, then reading the resulting `localExitRoot` storage slot
directly from the forked bridge contract.

**Why shadow-fork instead of local Merkle math:**
The `AgglayerBridge` contract maintains its own Local Exit Tree internally. Recomputing it
off-chain requires matching the exact leaf encoding and tree implementation. Driving the actual
contract on a fork eliminates that divergence risk.

**Approach:**

1. **Fork L2 at `targetBlock`** — spin up an Anvil instance (`anvil --fork-url <l2RpcUrl>
   --fork-block-number <targetBlock>`). Anvil is a required external dependency for this step.
2. **Impersonate a funded sender** — use `anvil_impersonateAccount` + `anvil_setBalance` so
   `bridgeAsset` calls can be sent without a real private key.
3. **Replay bridge exits** — for each `BridgeExit` in the certificate (`bridge_exits` list),
   send an `eth_sendTransaction` calling
   [`bridgeAsset`](https://github.com/agglayer/agglayer-contracts/blob/v12.2.3/contracts/AgglayerBridge.sol)
   on the L2 bridge contract with the same parameters:
   - `destinationNetwork` — from the `BridgeExit`
   - `destinationAddress` — from the `BridgeExit`
   - `amount` — from the `BridgeExit`
   - `token` — derived from `TokenInfo.OriginTokenAddress` / `OriginNetwork`
   - `forceUpdateGlobalExitRoot = false`
   - `permitData = ""`
4. **Read `localExitRoot`** — after all calls, call the `localExitRootManager().localExitRoot()`
   view function (or read the storage slot directly) on the bridge contract.
5. **Return result** — assign the result to `Certificate.NewLocalExitRoot` and return it to the
   caller. Saving `step-g-new-local-exit-root.json` is the orchestrator's responsibility, not Step G's.

**Anvil dependency:** the tool shells out to `anvil` (from the Foundry toolchain). If `anvil`
is not in `$PATH`, Step G must fail with a clear error message pointing to
`https://getfoundry.sh`.

**Empty bridge exits:** if the certificate has no `bridge_exits`, skip the fork entirely and
use the canonical `bridgesynctypes.EmptyLER` value (no Anvil needed).

- **Output:** `step-g-new-local-exit-root.json` (`StepGResult`)

### Step H — Fetch PreviousLocalExitRoot

- **Requires:** `options.agglayerGrpcUrl` — uses `agglayer.NewAgglayerClient` (gRPC), same as step SUBMIT.
- Calls `interop_getNetworkInfo` with `l2NetworkId` on the agglayer JSON-RPC and reads `settled_ler`.
- If no certificate has been settled yet (`settled_ler` is null), `PreviousLocalExitRoot` is zero.
- **Output:** `step-h-previous-local-exit-root.json` (`StepHResult`)

### Step I — Assemble final certificate

- Reads `step-e-exit-certificate.json` (base from E), `step-g-new-local-exit-root.json`, and
  `step-h-previous-local-exit-root.json` (optional).
- Sets `Certificate.NewLocalExitRoot` from G and `Certificate.PrevLocalExitRoot` from H.
- **Fetches `L1InfoTreeLeafCount`** — scans L1 backwards from the latest L1 block for the most
  recent `UpdateL1InfoTreeV2` event emitted by `l1GlobalExitRootAddress` and sets
  `Certificate.L1InfoTreeLeafCount`. Requires `l1RpcUrl` and `l1GlobalExitRootAddress` in config.
- **Output:** `exit-certificate-final.json` (updated with both roots and leaf count)

### Step SIGN — Sign certificate

- **Requires:** `signerConfig.Method` (skipped in `all` mode when not set; error in single-step mode).
- Uses the same `signertypes.SignerConfig` as aggsender's `AggsenderPrivateKey`. JSON format: `{"Method": "local", "Path": "keystore.json", "Password": "pass"}` (flat, mirrors the TOML inline table).
- Fetches `eth_chainId`, loads keystore via `go_signer`, hashes the certificate with `validator.HashCertificateToSign`, signs, and wraps in `AggchainDataMultisig`.
- **Output:** `exit-certificate-signed.json`

### Step SUBMIT — Send certificate to agglayer

- **Not part of `runAll`** — must be triggered explicitly with `--step submit`.
- **Requires:** `options.agglayerGrpcUrl` — the agglayer gRPC endpoint.
- Loads `exit-certificate-signed.json`, creates an agglayer gRPC client, and calls `SendCertificate`.
- **Output:** `step-submit-result.json` (`StepSubmitResult` with `certificateHash`)

### Step WAIT — Wait for certificate settlement

- **Not part of `runAll`** — must be triggered explicitly with `--step wait`.
- **Requires:** `options.agglayerGrpcUrl`.
- Reads `step-submit-result.json` for the certificate hash.
- **Phase 1:** checks for any pre-existing pending certificate on the network (different hash). If found, polls until it reaches a final state before proceeding.
- **Phase 2:** polls `GetCertificateHeader` every 5 seconds until the submitted certificate is `Settled` (success) or `InError` (returns an error).
- Logs the settlement tx hash on success.
- **Output:** `step-wait-result.json` (`StepWaitResult`)

## Key types (`types.go`)

| Type | Description |
| --- | --- |
| `LBTEntry` | LBT row: wrapped token address, origin network/token, total supply |
| `WrappedToken` | Like `LBTEntry` but without the balance field |
| `EOABalance` | Per-address: ETH balance + slice of `EOATokenBalance` |
| `AccumulatedBalance` | Sum across all EOAs for a single token |
| `SCLockedValue` | LBT total − EOA accumulated, per token |
| `L1Deposit` | Parsed `BridgeEvent` log from L1 |
| `TokenBalanceCheck` | Step F three-way comparison: `LBTAmount` (Step 0), `CertificateAmount` (sum of exits), `AgglayerAmount`. `LBTAmount` is empty when LBT data was unavailable (two-way fallback). |
| `StepGResult` | `NewLocalExitRoot` hash + bridge exit count |
| `StepHResult` | `PreviousLocalExitRoot` + next certificate height from agglayer |
| `StepSubmitResult` | `certificateHash` returned by the agglayer after submission |
| `StepWaitResult` | `certificateHash`, `finalStatus`, optional `settlementTxHash`, `elapsedSeconds`, optional `pendingCertWaited` |

## Config fields (`config.go`)

Required: `l2RpcUrl`, `l2BridgeAddress`, `targetBlock`.

`targetBlock` accepts: a finality keyword (`LatestBlock`, `FinalizedBlock`, `SafeBlock`, `PendingBlock`), an optional negative offset appended with `/` (e.g. `LatestBlock/-10`), a decimal block number (`"21000000"`), or a hex block number (`"0x1406f40"`). An empty string defaults to `LatestBlock`. The keyword is resolved to a concrete `uint64` at the start of Step 0 and written to `step-0-l2_target_block.json`; all subsequent steps (A, B, G) read that fixed number. The old lowercase aliases (`latest`, `finalized`, `safe`, `pending`) are **not** accepted — use the PascalCase keywords.

Notable optional fields:

- `sovereignRollupAddr` — address of the `aggchainbase` contract on L1. Required by Step CHECK (checks 4–6). Without it Step CHECK fails.
- `l1GlobalExitRootAddress` — address of `PolygonZkEVMGlobalExitRootV2` on L1. Required by Step I to fetch `L1InfoTreeLeafCount`. Without it Step I fails.
- `options.bridgeServiceURL` — base URL of the bridge service REST API. When set, Step E cross-checks unclaimed deposits against the bridge service and errors on discrepancies.
- `options.bridgeServiceType` — `"aggkit"` (default) or `"zkevm"`. Selects the API flavour used for the cross-check.

Defaults applied by `LoadConfig`:

- `l1BridgeAddress` defaults to `l2BridgeAddress`
- `l2NetworkId` defaults to `1`
- `options.blockRange` = 5000, `concurrencyLimit` = 20, `rpcBatchSize` = 200
- `options.abortOnGenesisBalance` = `true` — abort if any address has a non-zero ETH balance at block 0 (genesis preload guard). Set `false` only for Kurtosis/test environments.
- `options.continueIfBalanceMismatch` = `false` — when `true`, Step F does not abort on token balance mismatches and instead produces a capped certificate.
- Relative paths in `options.outputDir` and `signerConfig.Path` resolve from the directory containing the config file.

`signerConfig` uses `signertypes.SignerConfig` (same type as aggsender's `AggsenderPrivateKey`). The JSON format is flat — `Method`, `Path`, `Password` are top-level keys (matching the TOML inline table style). Parsed by `parseSignerConfig` which splits `Method` out and puts the rest into `Config map[string]any`.

## RPC layer (`rpc.go`, `worker.go`)

- All RPC is plain JSON-RPC over HTTP — no go-ethereum client.
- `concurrentBatchRPC` sends calls in `rpcBatchSize`-sized batches, dispatches batches with a semaphore of size `concurrencyLimit`.
- `runWorkerPool` is a generic fan-out + fan-in over a slice of inputs with a configurable worker count.
- Retry logic uses `defaultRetries` (3) for `singleRPC`.
- `rpcDelayMs` inserts a sleep between batches for rate-limiting.

## Invariants and gotchas

- **Output dir:** All intermediate files land in `options.outputDir` (default `./output` relative to the config file). The dir is created automatically.
- **`parameters.json` and `output/` are git-ignored** — never commit them.
- **File chain:** Step D → `step-d-exit-certificate.json`; Step E → `step-e-exit-certificate.json` (adds unclaimed deposits); Step I → `exit-certificate-final.json` (sets `NewLocalExitRoot` from G and `PrevLocalExitRoot` from H). Always submit `exit-certificate-final.json` (or the signed variant).
- **LBT resolution:** `resolveOrGenerateLBT` always runs Step 0 and saves `step-0-lbt.json`.
- **Step F reads from `step-d-exit-certificate.json`** for the balance check (not the final certificate), so the comparison reflects pure L2 exits before Step E additions. When capping is triggered, the caps are also applied to the final (Step E) certificate's `BridgeExits` in `runAll`, and saved as `step-f-capped-certificate.json`.
- **File chain with capping:** when `continueIfBalanceMismatch=true` produces a capped cert, the effective chain becomes: Step D → Step E → **Step F (capped)** → Step G → … Always check whether `step-f-capped-certificate.json` exists when investigating balance issues.
- **`--verbose` flag:** the logger defaults to `info` level; pass `--verbose` to enable `debug` output.
- **SC-locked value can be negative** when genesis state was pre-loaded or the LBT is stale — `abortOnGenesisBalance=true` catches this early.
- **`debug_traceTransaction` must be available** on the L2 RPC (Step A). Archive node required.
- **Step G requires Anvil** (`anvil` binary in `$PATH`, from the Foundry toolchain). The step fails fast with a clear error if it is missing.
- **FEP chains are not supported.** Only Pessimistic Proof certificates are generated.
- **`SetClaim` and `UpdatedUnsetGlobalIndexHashChain` events are not handled** — value from those flows may be missing.

## Testing

Run from the repo root:

```bash
go test ./tools/exit_certificate/...
```

Or a single test:

```bash
go test -v -run TestName ./tools/exit_certificate/
```

Test files: `*_test.go` beside each step file. Use `require` (not `assert`). No mocks for the RPC layer — tests that hit network are integration tests in `integration_test.go` and require a live node.

## Build

```bash
cd tools/exit_certificate
go build -o exit-certificate ./cmd
```

## Coding rules

- **Contract binding**: Use the library "github.com/0xPolygon/cdk-contracts-tooling/contracts/". Here you can find all the contract, for instance, for bridge you can use: "github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
