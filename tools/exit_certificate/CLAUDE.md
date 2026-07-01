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
├── step_g1.go           — resolve shadow-fork block (real-L2 bridgesync pre-sync)
├── step_g2.go           — NewLocalExitRoot computation (Step G2)
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

1. **Anvil installed** — `anvil` must be in `$PATH` (required by Step G2 only when `options.verifyNewLocalExitRootUsingShadowFork=true`). Fails with a clear error pointing to [getfoundry.sh](https://getfoundry.sh) if missing.
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
- **Option:** `ignoreOnTraceError=true` skips failed traces instead of aborting.

### Step B — EOA balance checking + ERC-20 detection

Three sub-steps: B1, B2, B3. Running `--step b` executes all three.

#### Step B1 — EOA classification and balance fetching

1. `eth_getCode` → classify each address as EOA or contract
2. `eth_getBalance` for all EOAs at `targetBlock`
3. `balanceOf(address)` per wrapped token × per EOA (token list from LBT)

**`options.ignoreAddresses`:** any EOA in this list still has its balances fetched, but it is then
split off (`extractIgnoredBalances`) into `step-b-ignored-balances.json` and removed from both
`EOABalances` and `Accumulated`. Because its value no longer counts as EOA-held, it rolls into the
per-token SC-locked total (Step C) and is bridged to `exitAddress` by Step D — the certificate still
balances against the LBT (Step F stays green). No exit is ever created back to the ignored address.

- **Output:** `step-b-eoa-balances.json` (`[]EOABalance`), `step-b-accumulated.json` (`[]AccumulatedBalance`), `step-b-contract-addresses.json` (`[]common.Address`), `step-b-ignored-balances.json` (`[]EOABalance`, *only when `options.ignoreAddresses` matched at least one address*)

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

- **Mode:** `options.useAgglayerAdminToStepFCheck` (default `true`) selects the comparison source:
  - **`true` (agglayer mode):** calls `admin_getTokenBalance` on the agglayer admin RPC and performs a **three-way comparison** per token: `LBT (Step 0) == agglayer == certificate sum`. Requires `agglayerAdminURL` (errors without it). When LBT is unavailable, falls back to two-way (certificate vs agglayer).
  - **`false` (offline mode, `runStepFOfflineLBT`):** **no agglayer query** — performs a two-way **LBT (Step 0) vs certificate sum** comparison per token. No `agglayerAdminURL` needed. When no LBT data is available there is nothing to compare and the step is skipped with a benign all-match result. `AgglayerAmount` is empty in the checks and `step-f-token-balances.json` is not written.
- Each token is logged with ✅ or ❌. The shared `finalizeStepFResult` applies the `ignoreBalanceMismatch` policy in both modes.
- **On mismatch:** aborts the pipeline with an error by default.
- **`ignoreBalanceMismatch=true`:** suppresses the error and produces `step-f-capped-certificate.json`, where each mismatched token's bridge exits are trimmed so their sum equals the token budget `min(agglayer, lbt)`. The pipeline (and `runSingleG`) automatically uses this capped certificate for subsequent steps.
- `capCertificateExits` is the internal helper that applies the caps. It is a **greedy per-token allocator**: it walks each token's exits, deducting each amount from the budget, capping the boundary exit to the leftover and dropping any exit once the budget is exhausted. `options.capMode` selects the allocation order — `"appearance"` (default) serves exits in the order they appear; `"amount"` serves the largest-amount exits first (big holders kept intact, small ones capped/dropped). In both modes the surviving exits are emitted in their original order.
- **Output:** `step-f-token-balances.json`, `step-f-checks.json` (`[]TokenBalanceCheck`), `step-f-capped-certificate.json` *(only when `ignoreBalanceMismatch=true` and mismatches exist)*

### Step G — Compute NewLocalExitRoot (shadow-fork)

Two sub-steps: G1, G2. Running `--step g` executes both; `g1`/`g2` run them individually, and `g`
expands to `g1,g2` in ranges (e.g. `f-g` → `f,g1,g2`).

#### Step G1 — Sync the L2 bridge history and resolve the shadow-fork block

**Persists** every L2 bridge from genesis up to `targetBlock` using the **lite bridge syncer**
(`tools/exit_certificate/bridgesyncerlite`), reading `BridgeEvent` logs from the **real L2**
(`l2RpcUrl`) in parallel into the DB at `output/step-g1-l2bridgesyncerlite.sqlite`. It does **not**
build the exit tree here — that is deferred to Step G2, which assembles the whole tree once from the
full set (genesis→fork plus replayed). The shadow-fork block is exactly the resolved `targetBlock`
(the lite syncer fetches that range), so Anvil forks there aligned to the contract's state at that
block. Running the full-history scan against the *fast* real L2 is the point of the G1/G2 split: G2
never re-scans the chain.

The lite syncer aborts if the chain emitted any event that would invalidate a BridgeEvent-only
reconstruction (`SetSovereignTokenAddress`, `MigrateLegacyToken`,
`RemoveLegacySovereignTokenAddress`, `BackwardLET`, `ForwardLET`) — unless
`options.ignoreUnsupportedL2Events=true`, which downgrades the abort to a warning and skips the event
(the resulting LER may then be incorrect). `NewWrappedToken` is ignored (it is neither indexed nor
processed).

- **Output:** `step-g1-shadow-fork-block.json` (`StepG1Result`: `shadowForkBlock`) and the lite DB
  `output/step-g1-l2bridgesyncerlite.sqlite`.

#### Step G2 — Compute NewLocalExitRoot

> **Input priority (single-step mode):** loads the shadow-fork block from `step-g1-shadow-fork-block.json` (run G1 first); uses `step-f-capped-certificate.json` if it exists (logged with ⚠️), otherwise falls back to `step-e-exit-certificate.json`. In `runAll` the in-memory certificate already reflects any capping done by Step F.

Step G2 has two modes, selected by `options.verifyNewLocalExitRootUsingShadowFork` (default `true`,
i.e. the shadow-fork mode below).

##### Off-chain lite exit tree (no Anvil) — `options.verifyNewLocalExitRootUsingShadowFork=false`

`runStepG2LiteOnly` → `buildLiteTreeFromCertificate` (`step_g_events.go`): **copies** the lite DB
Step G1 populated (`output/step-g1-l2bridgesyncerlite.sqlite` → `output/step-g-l2bridgesyncerlite.sqlite`,
so G1's DB stays intact), converts the certificate's `bridge_exits` into lite leaves **in their
given order** — continuing the deposit counts after the genesis→fork bridges — and **builds the
whole exit tree once**. The tree root is the `NewLocalExitRoot`. No reorder, no Anvil.

Each leaf is encoded as the bridge contract would: a native exit (nil/zero token info, or the gas
token) takes the gas token as origin; an ERC-20 exit takes its `TokenInfo` origin. **Metadata is
taken verbatim from each `BridgeExit`** (empty unless a prior step populated it). This is the one
value not verified against the chain in this mode — if an exit needs non-empty metadata (e.g. an
L2-native token bridged out, where the contract encodes name/symbol/decimals), the off-chain LER
would diverge from the real one. Use the shadow-fork mode to verify.

##### Anvil shadow-fork (default — `options.verifyNewLocalExitRootUsingShadowFork=true`)

`runStepG2ShadowFork` drives the **actual** bridge contract on a fork, eliminating any leaf-encoding
divergence risk, and verifies the off-chain reconstruction against it.

1. **Fork L2 at the Step G1 block** — spin up an Anvil instance (`anvil --fork-url <l2RpcUrl>
   --fork-block-number <g1ShadowForkBlock> --block-time <anvilBlockTimeSeconds> --disable-block-gas-limit
   --auto-impersonate --no-rate-limit`). Anvil is a required external dependency for this mode.
   **Interval mining** (`--block-time`) is used instead of auto-mine: with auto-mine each `bridgeAsset`
   would produce its own block, so a mainnet replay (hundreds of thousands of exits) accumulates that
   many blocks and Anvil degrades until receipt polling times out. Anvil instead mines a block every
   interval, batching all pending txs into it; `--disable-block-gas-limit` lets one block hold every
   pending tx. `--auto-impersonate` drops the per-tx `anvil_impersonateAccount` calls (balance is set
   once per sender). `--no-rate-limit` disables Anvil's internal ~330 CUPS throttle to the fork
   backend, which otherwise caps cold-state fetches to a few exits/s regardless of concurrency.

   > **Fork backend is the bottleneck.** Replaying against a *remote* `l2RpcUrl` means every cold
   > storage slot is a network round-trip; throughput is bound by the upstream RPC's latency and rate
   > limits. Transient fork errors are retried (`isTransientForkError`,
   > `--retries`/`--fork-retry-backoff`). For a large replay, fork against a **local archive node**.
2. **Fund the senders** — Anvil runs with `--auto-impersonate`, so any account can send txs; each
   sender's ETH balance is set once with `anvil_setBalance`. For ERC-20 exits, the sender's token
   balance is patched to `MaxUint256` via storage and a single `approve(bridge, MaxUint256)` is sent
   per (sender, token).
3. **Replay bridge exits via a send/collect pipeline** — for each `BridgeExit`, send `bridgeAsset`
   (`forceUpdateGlobalExitRoot=false`, empty `permitData`). `replayBridgeExits` does **not** wait for
   each tx's receipt before sending the next; sender workers (one per sender group,
   `concurrency = options.concurrencyLimit`) fire all of a sender's txs onto a bounded channel
   (`replayInFlightWindow`) while collector workers pull them and fetch receipts in parallel.
   **Exits are grouped by sender (`DestinationAddress`)**: same-sender txs are sent sequentially so
   Anvil assigns nonces in order (approve before bridge). As each receipt is collected its
   `BridgeEvent` is parsed into a `bridgesyncerlite.BridgeLeaf` — the on-chain `depositCount`, leaf
   content, metadata and block position — and stored at the exit's original index
   (`replayBridgeExits` returns `[]BridgeLeaf`).
4. **Read `getRoot()`** on the forked contract after every exit is replayed — the authoritative
   on-chain LER, which becomes `Certificate.NewLocalExitRoot`.
5. **Reorder the certificate by deposit count** — `reorderCertificateByDepositCount` (`step_g_order.go`)
   sorts the exits (and the metadata slice) by the captured `DepositCount`, aligning the certificate
   with the on-chain exit-tree leaf order (agglayer rebuilds the LER by inserting `bridge_exits` in
   order). The metadata also comes from the replayed leaves (the real on-chain metadata).
6. **Verify** — `buildLiteTreeWithReplayed` inserts the replayed leaves into the copied lite DB on
   top of the genesis→fork bridges and builds the tree; its root **must** equal the contract's
   `getRoot()`. A mismatch aborts Step G2 — except when `options.ignoreUnsupportedL2Events=true`,
   where divergence is expected (the syncer skipped events the contract processed) and is only logged.

   The replay is **fail-fast on hard errors**: the first `approve`/`bridgeAsset` send failure or
   on-chain revert cancels the shared context, aborts with the real error (not `context.Canceled`),
   kills Anvil via `defer cleanup()`, and persists the offending exit to `step-g-failed-exit.json`
   (`FailedBridgeExit`).

   A **receipt timeout** (`receiptPollTimeout`, 300s — the block did not mine in time, typically a
   slow remote fork backend) is **not** fatal: the exit is deferred and retried after the
   send/collect phase drains (`retryDeferredExit`). The retry loops **unbounded** until the exit
   mines: each iteration **re-polls the current tx** (Anvil has usually mined its block by then) and,
   only if the receipt is still absent — i.e. the tx never landed — **re-sends** the `bridgeAsset`
   and polls the new hash next. Re-polling before each re-send is what keeps the tree correct: a tx
   that did mine is never sent twice (which would double-count the exit's leaf). The retry exits only
   on success, a **revert**, or **context cancellation** — those (and a re-send send failure) are
   terminal and abort as above. A slow fork backend is never abandoned.

**Empty bridge exits:** if the certificate has no `bridge_exits`, both modes skip straight to the
canonical `bridgesynctypes.EmptyLER` (no Anvil, no tree).

**Reordered certificate output:** the orchestrator saves the (shadow-fork-reordered, or
default-order) certificate as `step-g-reordered-certificate.json` — written in both G2 modes. In
`runAll` the in-memory certificate flows to Step I; in single-step mode Step I **always** reads
`step-g-reordered-certificate.json` (no fallback to the capped/Step-E certificates) so the final
certificate matches the computed LER.

- **Output (G1):** `step-g1-shadow-fork-block.json` (`StepG1Result`) and the lite syncer DB `output/step-g1-l2bridgesyncerlite.sqlite`.
- **Output (G2):** `step-g-new-local-exit-root.json` (`StepGResult`), `step-g-reordered-certificate.json`, `step-g-l2bridgesyncerlite.sqlite` (working copy of the G1 lite DB with the certificate's/replayed bridges + built tree); in shadow-fork mode also `step-g-failed-exit.json` *(only on replay failure)*

### Step H — Fetch PreviousLocalExitRoot

- **Requires:** `options.agglayerGrpcUrl` — uses `agglayer.NewAgglayerClient` (gRPC), same as step SUBMIT.
- Calls `interop_getNetworkInfo` with `l2NetworkId` on the agglayer JSON-RPC and reads `settled_ler`.
- If no certificate has been settled yet (`settled_ler` is null), `PreviousLocalExitRoot` is zero.
- **Output:** `step-h-previous-local-exit-root.json` (`StepHResult`)

### Step I — Assemble final certificate

- Reads the base certificate. In single-step mode it **always** loads
  `step-g-reordered-certificate.json` (run Step G first — there is no fallback to the capped/Step-E
  certificates); in `runAll` the in-memory reordered certificate flows directly from Step G. Also
  reads `step-g-new-local-exit-root.json` and `step-h-previous-local-exit-root.json` (optional).
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
- **Requires:** `options.agglayerGrpcUrl` — the agglayer gRPC endpoint; and `l1RpcUrl`.
- Loads `exit-certificate-signed.json`, creates an agglayer gRPC client, captures the **latest L1 block right before submission**, and calls `SendCertificate`.
- **Output:** `step-submit-result.json` (`StepSubmitResult` with `certificateHash` and `l1LatestBlockBeforeSubmittingCertificate`)

### Step WAIT — Wait for certificate settlement

- **Not part of `runAll`** — must be triggered explicitly with `--step wait`.
- **Requires:** `options.agglayerGrpcUrl` and `l1RpcUrl`.
- Reads `step-submit-result.json` (the whole `StepSubmitResult`, including `l1LatestBlockBeforeSubmittingCertificate`).
- Polls `GetCertificateHeader` by hash every 5 seconds until the submitted certificate is `Settled` (success) or `InError` (returns an error). Logs the settlement tx hash on success.
- **L1 settlement confirmation:** after the certificate settles, scans the RollupManager contract on L1 from `l1LatestBlockBeforeSubmittingCertificate` to the **finalized** block for the `VerifyBatchesTrustedAggregator` event matching the rollupID (`l2NetworkId`) and the certificate's `NewLocalExitRoot`. The RollupManager address is `rollupManagerAddress` if set, otherwise resolved on-chain from `sovereignRollupAddr.rollupManager()`. It re-resolves the finalized block and re-scans every 5 seconds until found (the settlement tx may not be finalized yet) or the context is cancelled, recording the L1 block and tx hash. **Errors** when `l1RpcUrl` is unset or when neither `rollupManagerAddress` nor `sovereignRollupAddr` is available to resolve the RollupManager.
- **L1 info tree updates:** in that same L1 block, reads the `l1GlobalExitRootAddress` contract's `UpdateL1InfoTree` and `UpdateL1InfoTreeV2` events (the global-exit-root update accompanying the settlement) and records the **last** occurrence of each (`updateL1InfoTree`, `updateL1InfoTreeV2`). Requires `l1GlobalExitRootAddress`; errors if either event is missing from the block.
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
| `StepG1Result` | `ShadowForkBlock` (the L2 block Step G2 forks at; the resolved targetBlock up to which G1 lite-synced the bridge history) |
| `StepGResult` | `NewLocalExitRoot` hash + bridge exit count + `BridgeExitMetadata` (per-exit BridgeEvent metadata, in deposit order) |
| `StepHResult` | `PreviousLocalExitRoot` + next certificate height from agglayer |
| `StepSubmitResult` | `certificateHash` returned by the agglayer after submission + `l1LatestBlockBeforeSubmittingCertificate` (latest L1 block captured just before the submit) |
| `StepWaitResult` | `certificateHash`, `finalStatus`, optional `settlementTxHash`, `elapsedSeconds`, the L1 `VerifyBatchesTrustedAggregator` settlement (`verifyBatchesL1Block` + `verifyBatchesTxHash`), and the last `updateL1InfoTree` / `updateL1InfoTreeV2` GER events in that block |
| `L1InfoTreeUpdate` | `UpdateL1InfoTree` event: `mainnetExitRoot`, `rollupExitRoot`, `txHash` |
| `L1InfoTreeV2Update` | `UpdateL1InfoTreeV2` event: `currentL1InfoRoot`, `leafCount`, `blockhash`, `minTimestamp`, `txHash` |

## Config fields (`config.go`)

**File format:** `LoadConfig` accepts both **JSON** and **TOML**, selected by file extension — a
`.toml` path is parsed as TOML, anything else (`.json` or no extension) as JSON. TOML is normalized
to JSON internally (`tomlToJSON`: decode to a map, re-encode as JSON) so both formats share one
parsing/validation path, including `signerConfig` and `agglayerClient`. Field names are identical in
both formats (camelCase keys, e.g. `l2RpcUrl`; `signerConfig` uses PascalCase `Method`/`Path`/`Password`).

Required: `l2RpcUrl`, `l2BridgeAddress`, `exitAddress`, `targetBlock`.

`exitAddress` is validated by `LoadConfig`: it must be present **and** must not be the zero address
(`0x00…00`) — both cases return an error. SC-locked value is bridged to this address on
`destinationNetwork`, so it must be an address whose private key the operator controls (the funds can
only be recovered by signing from it).

`targetBlock` accepts: a finality keyword (`LatestBlock`, `FinalizedBlock`, `SafeBlock`, `PendingBlock`), an optional negative offset appended with `/` (e.g. `LatestBlock/-10`), a decimal block number (`"21000000"`), or a hex block number (`"0x1406f40"`). An empty string defaults to `LatestBlock`. The keyword is resolved to a concrete `uint64` at the start of Step 0 and written to `step-0-l2_target_block.json`; all subsequent steps (A, B, G) read that fixed number. The old lowercase aliases (`latest`, `finalized`, `safe`, `pending`) are **not** accepted — use the PascalCase keywords.

Notable optional fields:

- `sovereignRollupAddr` — address of the `aggchainbase` contract on L1. Required by Step CHECK (checks 4–6). Without it Step CHECK fails.
- `l1GlobalExitRootAddress` — address of `PolygonZkEVMGlobalExitRootV2` on L1. Required by Step I to fetch `L1InfoTreeLeafCount`. Without it Step I fails.
- `rollupManagerAddress` — **optional** address of the `PolygonRollupManager` (AgglayerManager) contract on L1. Used by Step WAIT to confirm the certificate's L1 settlement via the `VerifyBatchesTrustedAggregator` event. When unset it is resolved on-chain from `sovereignRollupAddr.rollupManager()` (PolygonConsensusBase). Step WAIT errors if neither `rollupManagerAddress` nor `sovereignRollupAddr` is set.
- `options.capMode` — `"appearance"` (default) or `"amount"`. Only relevant with `ignoreBalanceMismatch=true`: selects how Step F allocates each token's cap budget when trimming exits. `"appearance"` serves exits in the order they appear; `"amount"` serves the largest-amount exits first (big holders kept intact, small ones capped/dropped). Surviving exits are emitted in their original order in both modes.
- `options.ignoreAddresses` — optional `[]string` of addresses whose balances must not be returned to them. Validated by `LoadConfig` (each must be a valid, non-zero hex address). Step B1 still fetches their balances and records them in `step-b-ignored-balances.json`, but excludes them from the EOA exits and accumulated totals, so their value rolls into the SC-locked total and is bridged to `exitAddress` by Step D (the certificate stays balanced against the LBT — Step F stays green).
- `options.bridgeServiceURL` — base URL of the bridge service REST API. When set, Step E cross-checks unclaimed deposits against the bridge service and errors on discrepancies.
- `options.bridgeServiceType` — `"aggkit"` (default) or `"zkevm"`. Selects the API flavour used for the cross-check.
- `options.useAgglayerAdminToStepFCheck` — `true` (default). When `true`, Step F runs the agglayer admin balance check (`admin_getTokenBalance`, three-way comparison; requires `agglayerAdminURL`). When `false`, Step F skips the agglayer query and instead compares the LBT (Step 0) totals against the certificate bridge-exit sums offline (no `agglayerAdminURL` needed; skipped only if no LBT data exists). Set to `false` when no agglayer admin endpoint is available.
- `options.ignoreUnsupportedL2Events` — `false` (default). When `true`, the Step G lite syncer logs a warning and continues instead of aborting when it encounters an event that would invalidate a BridgeEvent-only reconstruction (`SetSovereignTokenAddress`, `MigrateLegacyToken`, `RemoveLegacySovereignTokenAddress`, `BackwardLET`, `ForwardLET`). The computed `NewLocalExitRoot` may then be incorrect — enable only to inspect such a chain knowingly.
- `options.verifyNewLocalExitRootUsingShadowFork` — `true` (default). When `true`, Step G2 spins up the Anvil shadow-fork, replays every exit against the real bridge contract, reorders the certificate to the on-chain deposit order with the on-chain metadata, and verifies the lite tree root against the contract's `getRoot()` (requires Anvil). When `false`, Step G2 computes the `NewLocalExitRoot` off-chain from the lite exit tree (G1's genesis→fork bridges + the certificate's exits) — fast, no Anvil, but it trusts the off-chain leaf encoding/metadata.

Defaults applied by `LoadConfig`:

- `l1BridgeAddress` defaults to `l2BridgeAddress`
- `l2NetworkId` defaults to `1`
- `options.blockRange` = 5000, `concurrencyLimit` = 20, `rpcBatchSize` = 200
- `options.ignoreGenesisBalance` = `false` — when `false` (default), Step B aborts if any address has a non-zero ETH balance at block 0 (genesis preload guard). Set `true` to downgrade it to a warning, only for Kurtosis/test environments.
- `options.ignoreBalanceMismatch` = `false` — when `true`, Step F does not abort on token balance mismatches and instead produces a capped certificate.
- `options.useAgglayerAdminToStepFCheck` = `true` — when `false`, Step F skips the agglayer admin query and compares LBT (Step 0) vs certificate sums offline instead.
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
- **File chain:** Step D → `step-d-exit-certificate.json`; Step E → `step-e-exit-certificate.json` (adds unclaimed deposits); Step G2 → `step-g-reordered-certificate.json` (deposit-order exits); Step I reads `step-g-reordered-certificate.json` → `exit-certificate-final.json` (sets `NewLocalExitRoot` from G and `PrevLocalExitRoot` from H). Always submit `exit-certificate-final.json` (or the signed variant).
- **LBT resolution:** `resolveOrGenerateLBT` always runs Step 0 and saves `step-0-lbt.json`.
- **Step F reads from `step-d-exit-certificate.json`** for the balance check (not the final certificate), so the comparison reflects pure L2 exits before Step E additions. When capping is triggered, the caps are also applied to the final (Step E) certificate's `BridgeExits` in `runAll`, and saved as `step-f-capped-certificate.json`.
- **File chain with capping:** when `ignoreBalanceMismatch=true` produces a capped cert, the effective chain becomes: Step D → Step E → **Step F (capped)** → Step G → … Always check whether `step-f-capped-certificate.json` exists when investigating balance issues.
- **`--verbose` flag:** the logger defaults to `info` level; pass `--verbose` to enable `debug` output.
- **SC-locked value can be negative** when genesis state was pre-loaded or the LBT is stale — the genesis-balance guard (`ignoreGenesisBalance=false`, the default) catches this early.
- **`debug_traceTransaction` must be available** on the L2 RPC (Step A). Archive node required.
- **Step G2 requires Anvil only in shadow-fork mode** (`options.verifyNewLocalExitRootUsingShadowFork=true`; `anvil` binary in `$PATH`, from the Foundry toolchain). The default off-chain mode needs no Anvil.
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

From the repo root, using the top-level Makefile (binary is written to `target/exit_certificate`):

```bash
make build-exit_certificate
```

Or directly with `go`:

```bash
cd tools/exit_certificate
go build -o exit-certificate ./cmd
```

## Coding rules

- **Contract binding**: Use the library "github.com/0xPolygon/cdk-contracts-tooling/contracts/". Here you can find all the contract, for instance, for bridge you can use: "github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
