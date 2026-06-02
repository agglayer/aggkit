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

### Step B — EOA balance checking + ERC-20 detection

Three sub-steps: B1, B2, B3. Running `--step b` executes all three.

#### Step B1 — EOA classification and balance fetching

1. `eth_getCode` → classify each address as EOA or contract
2. `eth_getBalance` for all EOAs at `targetBlock`
3. `balanceOf(address)` per wrapped token × per EOA (token list from LBT)

- **Output:** `step-b-eoa-balances.json` (`[]EOABalance`), `step-b-accumulated.json` (`[]AccumulatedBalance`), `step-b-contract-addresses.json` (`[]common.Address`)

#### Step B2 — ERC-20 detection in contracts

Probes each contract address with `totalSupply()` / `balanceOf(address(0))` to confirm the ERC-20 interface. For each detected ERC-20, calls `balanceOf(contractAddr)` on every tracked wrapped token and `eth_getBalance` to find which tracked tokens it holds.

- Holds ≥ 1 tracked token → `DetectedERC20` (relevant)
- Holds none → `DiscardedERC20` (irrelevant)

- **Output:** `step-b2-detected-erc20s.json` (`[]DetectedERC20`), `step-b2-discarded-erc20s.json` (`[]DiscardedERC20`)

#### Step B3 — Extra ERC-20 holder decomposition

Iterates over `options.extraErc20Contracts`. For each address:

- If Step B2 already populated `Holders` for it, copies those holders and marks `AlreadyFromB2=true` — no RPC call.
- Otherwise, calls `fetchTokenBalances` (one RPC batch of `balanceOf` for every EOA from Step A).

Skipped automatically when `options.extraErc20Contracts` is empty.

- **Output:** `step-b3-erc20-holders.json` (`[]ERC20HolderBreakdown`)

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
   --fork-block-number <targetBlock> --block-time <anvilBlockTimeSeconds> --disable-block-gas-limit
   --auto-impersonate --no-rate-limit`). Anvil is a required external dependency for this step.
   **Interval mining** (`--block-time`) is used instead of auto-mine: with auto-mine each `bridgeAsset`
   would produce its own block, so a mainnet replay (hundreds of thousands of exits) accumulates that
   many blocks and Anvil degrades until receipt polling times out. Anvil instead mines a block every
   interval, batching all pending txs into it; `--disable-block-gas-limit` lets one block hold every
   pending tx. `--auto-impersonate` drops the per-tx `anvil_impersonateAccount` calls (balance is set
   once per sender). `--no-rate-limit` disables Anvil's internal ~330 CUPS throttle to the fork
   backend, which otherwise caps cold-state fetches to a few exits/s regardless of concurrency.
   Correctness is unchanged: each worker still waits for its tx's receipt before the next exit, so the
   per-exit balance patching stays correctly ordered.

   > **Fork backend is the bottleneck.** Replaying against a *remote* `l2RpcUrl` means every cold
   > storage slot is a network round-trip; throughput is bound by the upstream RPC's latency and rate
   > limits. The send/collect pipeline (step 3) keeps that from being made worse by per-tx receipt
   > waits, but it cannot remove the fetch cost itself. Transient fork errors are retried
   > (`isTransientForkError`, `--retries`/`--fork-retry-backoff`) so a dropped connection doesn't abort
   > the run. For a large replay, fork against a **local archive node** to remove the network cost
   > entirely.
2. **Fund the senders** — Anvil runs with `--auto-impersonate`, so any account can send txs; each
   sender's ETH balance is set once with `anvil_setBalance`. For ERC-20 exits, the sender's token
   balance is patched to `MaxUint256` via storage and a single `approve(bridge, MaxUint256)` is sent
   per (sender, token) — so a sender can bridge a token any number of times without
   underflowing balance/allowance.
3. **Replay bridge exits via a send/collect pipeline** — for each `BridgeExit` in the certificate
   (`bridge_exits` list), send an `eth_sendTransaction` calling
   [`bridgeAsset`](https://github.com/agglayer/agglayer-contracts/blob/v12.2.3/contracts/AgglayerBridge.sol)
   on the L2 bridge contract with the same parameters:
   - `destinationNetwork` — from the `BridgeExit`
   - `destinationAddress` — from the `BridgeExit`
   - `amount` — from the `BridgeExit`
   - `token` — derived from `TokenInfo.OriginTokenAddress` / `OriginNetwork`
   - `forceUpdateGlobalExitRoot = false`
   - `permitData = ""`

   `replayBridgeExits` does **not** wait for each tx's receipt before sending the next — with
   interval mining that would cap throughput at ~concurrency/`--block-time`. Instead it runs a
   **send/collect pipeline**: sender workers (one per sender group, `concurrency = options.concurrencyLimit`)
   fire all of a sender's txs without waiting and push each onto a bounded channel
   (`replayInFlightWindow`), while collector workers pull those and fetch receipts + `BridgeEvent`
   metadata in parallel. The channel capacity bounds the unconfirmed mempool, so block size and
   memory stay bounded. **Exits are grouped by sender (`DestinationAddress`)**: same-sender txs are
   sent sequentially so Anvil assigns nonces in order (approve before bridge); different senders are
   independent. Before any tx is sent, the fork head + 1 is recorded as `ShadowForkFirstBlock`.
4. **Read `localExitRoot`** — after all calls, call `getRoot()` on the bridge contract. The LER is
   independent of replay order (each deposit lands at its own tree index), so parallelism is safe.
5. **Recover deposit order and reorder the certificate** — the parallel replay assigns
   `depositCount`s non-deterministically, so the certificate's `bridge_exits` order must be aligned
   with the actual exit-tree leaf order or it would not match the computed `NewLocalExitRoot`
   (agglayer rebuilds the LER by inserting `bridge_exits` in order). Two interchangeable mechanisms
   recover the canonical order from the shadow-fork, selected via `options.depositOrderSource`
   (dispatched by `recoverShadowForkDepositOrder` in `step_g_order.go`):
   - **`"events"`** (default, `readShadowForkBridges` in `step_g_events.go`): reads `BridgeEvent`
     logs directly from the fork via `eth_getLogs`, **only from `ShadowForkFirstBlock`** onward (it
     does not sync the full L2 history). Lightweight and the recommended path.
   - **`"bridgesync"`** (`syncShadowForkBridges` in `step_g_bridgesync.go`): spins up an L2
     `bridgesync` syncer against the Anvil fork (reusing the production component), syncs **all** L2
     bridges from genesis, then filters those at `BlockNum >= ShadowForkFirstBlock` (the replayed
     ones).

   Both produce `[]shadowForkBridge` ordered by `DepositCount`. `reorderCertificateExits`
   (`step_g_order.go`) matches each replayed bridge back to a certificate exit by leaf content
   `(originNetwork, originAddress, destinationNetwork, destinationAddress, amount)` and reorders
   `Certificate.BridgeExits` (and the metadata slice) in place to deposit order.
6. **Return result** — assign the LER to `Certificate.NewLocalExitRoot` and return it. Saving
   `step-g-new-local-exit-root.json` is the orchestrator's responsibility, not Step G's.

**Anvil dependency:** the tool shells out to `anvil` (from the Foundry toolchain). If `anvil`
is not in `$PATH`, Step G must fail with a clear error message pointing to
`https://getfoundry.sh`.

**Empty bridge exits:** if the certificate has no `bridge_exits`, skip the fork entirely and
use the canonical `bridgesynctypes.EmptyLER` value (no Anvil needed).

**Reordered certificate output:** because Step G reorders `bridge_exits`, the orchestrator saves the
reordered certificate as `step-g-reordered-certificate.json`. In `runAll` the in-memory certificate
(already reordered) flows to Step I; in single-step mode Step I prefers
`step-g-reordered-certificate.json` over the capped/Step-E certificates so the final certificate
matches the computed LER. `StepGResult.ShadowForkFirstBlock` records the first replayed block.

**Abort on replay failure:** the parallel replay is fail-fast — the first `approveERC20`/`bridgeAsset`
failure cancels the shared context (so the other workers stop), aborts Step G with the real error
(not `context.Canceled`), and the `defer cleanup()` kills Anvil. The offending exit is persisted to
`step-g-failed-exit.json` (`FailedBridgeExit`) for inspection.

- **Output:** `step-g-new-local-exit-root.json` (`StepGResult`), `step-g-reordered-certificate.json`, `step-g-failed-exit.json` *(only on replay failure)*

### Step H — Fetch PreviousLocalExitRoot

- **Requires:** `options.agglayerGrpcUrl` — uses `agglayer.NewAgglayerClient` (gRPC), same as step SUBMIT.
- Calls `interop_getNetworkInfo` with `l2NetworkId` on the agglayer JSON-RPC and reads `settled_ler`.
- If no certificate has been settled yet (`settled_ler` is null), `PreviousLocalExitRoot` is zero.
- **Output:** `step-h-previous-local-exit-root.json` (`StepHResult`)

### Step I — Assemble final certificate

- Reads the base certificate (single-step priority: `step-g-reordered-certificate.json` >
  `step-f-capped-certificate.json` > `step-e-exit-certificate.json`), `step-g-new-local-exit-root.json`,
  and `step-h-previous-local-exit-root.json` (optional).
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
| `StepGResult` | `NewLocalExitRoot` hash + bridge exit count + `BridgeExitMetadata` + `ShadowForkFirstBlock` (first replayed shadow-fork block, used to recover deposit order) |
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
- `options.depositOrderSource` — `"events"` (default) or `"bridgesync"`. Selects how Step G recovers the canonical bridge deposit order from the shadow-fork after the parallel replay. `"events"` reads `BridgeEvent` logs directly from the fork (only the replayed blocks); `"bridgesync"` reuses the bridgesync component (syncs all L2 bridges from genesis). `LoadConfig` rejects any other value.

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
