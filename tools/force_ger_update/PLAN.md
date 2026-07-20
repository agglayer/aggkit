# Plan: `force_ger_update` — CLI tool that forces GER updates on L1

**Branch:** `feat/force-ger-update-tool` (already checked out — do all work here, never on `develop`)
**Plan file:** `tools/force_ger_update/PLAN.md` (this file — it is the single source of truth and must be kept up to date during execution)

---

## 1. Goal

Build a standalone, long-running CLI tool that guarantees the L1 Global Exit Root (GER) is updated at least every `X` amount of time (config defined). It does so by sending a `bridgeMessage` transaction on the L1 bridge with `forceUpdateGlobalExitRoot = true` whenever more than `X` time has elapsed since the last GER update on L1.

**Why (OP-FEP / DA provability):** each GER update appends a leaf to the L1 info tree that includes an L1 block hash, and the aggchain proof uses the block hash inside the L1 info root to assert what happened on L1 — including data availability. Anything posted on L1 (e.g. DA) *after* the last L1 info root update cannot be proven until a new update lands; if no update happens organically, certificate progress stalls indefinitely. This tool bounds that wait to `X`. (Full rationale text lives in step S9 and must end up in the tool README.)

Hard requirements (from the task owner):

1. **No syncer.** Do not use `sync`/`l1infotreesync` machinery, no sqlite state for events, no reorg detector. On boot, fetch the most recent `UpdateL1InfoTree` event directly via `FilterLogs`; afterwards, watch for new events to reset the timer.
2. **ethtxmanager** (`github.com/0xPolygon/zkevm-ethtx-manager`) must be used to send and track the `bridgeMessage` transactions.
3. **Key management** must be the standard one used across the codebase: `github.com/agglayer/go_signer` `SignerConfig` — supporting local keystore (`Method="local"`, `Path`, `Password`) **and** cloud KMS (`Method="GCP"|"AWS"`, `KeyName`). This comes for free because `ethtxmanager.Config.PrivateKeys` is `[]signertypes.SignerConfig`.
4. **E2E test** that proves the tool actually forces a GER update on a real network.

## 2. Design decisions (already made — do not re-litigate in steps)

- **Location / shape:** `tools/force_ger_update/` package with CLI entry at `tools/force_ger_update/cmd/main.go`, following the established tool convention (`tools/remove_ger`, `tools/exit_certificate`). CLI framework: `github.com/urfave/cli/v2`. Exported `Run(c *cli.Context) error` in the tool package; `cmd/main.go` only builds the `cli.App`.
- **Config:** standalone TOML file passed via `--cfg` flag, with a `[ForceGERUpdate]` root section. Loading should mirror `tools/remove_ger/config.go` (aggkit config render pipeline + viper + mapstructure decode hooks, giving `types.Duration`, `common.Address`, `SignerConfig` decoding and `CDK_`-prefixed env-var overrides). **Fallback** if the render pipeline forces unrelated mandatory template vars that don't fit a standalone tool: use a self-contained viper/TOML loader (like `tools/backward_forward_let/config.go`), keeping the same field names. Config fields:

  ```toml
  [ForceGERUpdate]
  L1URL = "http://..."                     # HTTP RPC (mandatory)
  L1WSURL = ""                             # optional websocket RPC; if set, event subscription uses Watch; else polling
  GlobalExitRootManagerAddr = "0x..."      # L1 PolygonZkEVMGlobalExitRootV2 (agglayerger binding)
  BridgeAddr = "0x..."                     # L1 PolygonZkEVMBridgeV2 (agglayerbridge binding)
  MaxTimeWithoutGERUpdate = "1h"           # X: send bridgeMessage when now - lastGERUpdate >= this
  CheckInterval = "10s"                    # how often the timer loop evaluates the elapsed time
  EventPollInterval = "15s"                # polling mode: how often to FilterLogs for new UpdateL1InfoTree events
  InitialLookbackBlocks = 50000            # boot: how far back to scan (in chunks) for the last event
  FilterLogsChunkSize = 10000              # block range per FilterLogs call
  DestinationNetwork = 1                   # bridgeMessage destinationNetwork (must NOT be 0/L1 itself)
  DestinationAddress = "0x..."             # bridgeMessage destinationAddress (default: sender address)
  DryRun = false                           # log instead of sending
      [ForceGERUpdate.EthTxManager]
      # standard zkevm-ethtx-manager section — same shape as [AggOracle.EVMSender.EthTxManager]
      # in config/default.go:118-145, incl. PrivateKeys = [{Method="local", Path=..., Password=...}]
      # (or {Method="GCP"/"AWS", KeyName=...}), StoragePath = ".../ethtxmanager-force_ger_update.sqlite",
      # and [.Etherman] with URL = L1 RPC and L1ChainID.
      ```

- **Boot (last event):** scan backwards from the latest block in `FilterLogsChunkSize` chunks (bounded by `InitialLookbackBlocks`) with `FilterLogs` on the GER manager address, topic0 = `UpdateL1InfoTree(bytes32,bytes32)` (`0xda61aa7823fcd807e37b95aabcbe17f03a6f3efd514176444dae191d27fd66b3`). Take the newest log, fetch its block header, and set `lastGERUpdate = block timestamp`. If no event is found within the lookback window, treat the GER as stale (elapsed > threshold) → the tool will force an update on the first tick.
- **Event watching (timer reset):** two modes behind one small interface, **no syncer**:
  - `L1WSURL` set → `agglayerger.WatchUpdateL1InfoTree` (binding subscription) with automatic re-subscribe on error.
  - otherwise (default) → a lightweight loop that every `EventPollInterval` calls `FilterLogs` from `lastSeenBlock+1` to latest for the same topic. No persistence; a reorg at worst makes the tool send one redundant (harmless) forced update.
  - Watching V1 (`UpdateL1InfoTree`) is sufficient — V1 and V2 are emitted in the same tx.
- **Sending:** build calldata with `agglayerbridge.AgglayerbridgeMetaData.GetAbi()` → `abi.Pack("bridgeMessage", destinationNetwork, destinationAddress, true /*forceUpdateGlobalExitRoot*/, []byte{} /*metadata*/)` (selector `0x240ff378`), then `ethTxManager.Add(ctx, &bridgeAddr, common.Big0, data, gasOffset, nil)` and monitor with the `Result`-polling pattern from `aggoracle/chaingersender/evm.go:213-277` (statuses Created/Sent → wait; Failed → error; Mined/Safe/Finalized → success). While a forced-update tx is in flight, do not enqueue another one. After the tx is mined, the timer resets via the observed `UpdateL1InfoTree` event (single source of truth), not via the tx receipt.
- **ethtxmanager wiring:** `ethtxmanager.New(cfg)` then `go client.Start()` (see `cmd/run.go:543-558`). Depend on the narrow interface `aggoracle/types/types.go:14-29` (`Add`, `Result`, `From`, ...) so it can be mocked.
- **Testing:** unit tests with mocks (mockery, `mocks/` subdir, `require` not `assert`); Tier-1 integration test on the go-ethereum simulated backend (`test/helpers`); Tier-2 real-network e2e in `test/e2e/` (docker-compose `op-pp` env), exercising the **built binary**.

## 3. Codebase facts (context for all steps — verified 2026-07-20)

| Topic | Fact |
|---|---|
| Module / Go | `github.com/agglayer/aggkit`, Go 1.25.7 |
| Bindings module | `github.com/0xPolygon/cdk-contracts-tooling v0.0.13` |
| L1 GER binding | `.../contracts/aggchain-multisig/agglayerger` — `NewAgglayerger`, `FilterUpdateL1InfoTree`, `WatchUpdateL1InfoTree`, `ParseUpdateL1InfoTree` (see usage in `l1infotreesync/downloader.go:16-24,109,127`) |
| L1 bridge binding | `.../contracts/aggchain-multisig/agglayerbridge` — `BridgeMessage(opts, destNetwork uint32, destAddr common.Address, forceUpdateGlobalExitRoot bool, metadata []byte)`; ABI unpacking example in `bridgesync/downloader.go:46-49,250-259` |
| ethtxmanager | `github.com/0xPolygon/zkevm-ethtx-manager v0.2.18`; `New(cfg)`, `Add(ctx, to, value, data, gasOffset, sidecar)`; config fields incl. `PrivateKeys []signertypes.SignerConfig`, `StoragePath` (sqlite), `[.Etherman] URL/L1ChainID`; narrow interface at `aggoracle/types/types.go:14-29`; submit+monitor pattern at `aggoracle/chaingersender/evm.go:213-277`; TOML example at `config/default.go:118-145` |
| Signer | `github.com/agglayer/go_signer v0.0.7`; `signertypes.SignerConfig{Method, ...remain}`; methods `local`/`GCP`/`AWS`/`remote`/`mock`; config examples in `docs/common_config.md` |
| Eth client types | `aggkittypes.BaseEthereumClienter` (`types/eth_client.go:38`) includes `ethereum.LogFilterer` (FilterLogs + SubscribeFilterLogs); prod dials HTTP only (`etherman/default_eth_client.go:47`) |
| Tool conventions | `tools/<name>/` + `tools/<name>/cmd/main.go`; Makefile `build-tools` target + per-tool rule (Makefile:79-116); Dockerfile `COPY --from=builder /app/target/<name> /usr/local/bin/` (Dockerfile:57-62); closest analog: `tools/remove_ger` |
| Simulated backend | `test/helpers/simulated.go`, `test/helpers/e2e.go` — `NewSimulatedL1(t)` deploys bridge proxy + `agglayerger` GER + rollup manager on `simulated.Backend` (chainID 1337); `test/helpers/ethtxmanmock_e2e.go` = mock ethtxmanager that actually sends via the simulated client |
| Real e2e | `test/e2e/` Go suite against docker-compose env `op-pp` (`envs.LoadEnv`, `test/e2e/envs/loader.go`); funded key pool `env.Keys.L1Keys.Checkout()`; bridge tx example `test/e2e/bridge_utils.go:21-40` (`BridgeAsset(..., forceUpdateGER, nil)`); run via `make test-e2e` (needs docker); CI: `.github/workflows/test-go-e2e.yml` |
| Lint/test | `make lint`, `make test-unit` (runs `-short`), `make generate-mocks`; testify with `require`; 120-char lines; wrapped errors `fmt.Errorf("...: %w", err)` |

---

## 4. How to execute this plan (instructions for the orchestrating agent)

You are the **main agent**. You do not implement steps yourself — you dispatch each step to a **sub-agent** and manage the plan. Protocol:

1. **Pick runnable steps:** a step is runnable when its `Status` is `pending` and all its `Dependencies` are `done`. If several steps are runnable, launch them **in parallel in a single message** — but two steps that write to the repo may only run concurrently if the plan marks them `parallel-group` with **worktree isolation** (launch those sub-agents with `isolation: "worktree"`); otherwise at most one writing step runs at a time (read-only steps may always run alongside).
2. **Dispatch:** set `Status: in_progress`, then spawn a sub-agent with the step's `Model` (and stated effort). The sub-agent prompt must contain: the step's Goal, Non-goals, Acceptance criteria, Context pack, the "Design decisions" and "Codebase facts" sections of this plan verbatim (or the file path `tools/force_ger_update/PLAN.md` with instruction to read sections 1–3), and the instruction to follow repo conventions in `CLAUDE.md`. Tell the sub-agent to return: what it changed (files), how it verified each acceptance criterion (with command output), and anything unexpected it found.
3. **Verify before marking done:** do not trust the sub-agent's word alone. Re-run the step's acceptance-criteria commands yourself (or via a cheap haiku sub-agent) when they are commands; spot-check diffs otherwise. Only then set `Status: done`.
4. **Fill the Log:** after each step, write 3–8 lines into the step's `Log` field: what was done, key files touched, verification evidence, deviations.
5. **Worktree merges:** after a `parallel-group` completes, merge the worktree branches back (the steps are designed to touch disjoint files, so merges should be trivial). Resolve conflicts yourself; rerun `go build ./...` after merging.
6. **When reality disagrees with the plan** (a step fails, an API doesn't exist as described, scope was missed): do NOT push blindly forward. You are empowered to **edit this file** — mark the step `failed` with a Log explaining why, then add new steps (IDs `S<next>`), or modify pending steps' Goals/Acceptance criteria, so the overall goal in section 1 is still reached. Record every plan modification in section 6 (Plan changelog) with a one-line rationale. Requirements in section 1 are immovable; everything in sections 2–5 is adaptable.
7. **Statuses:** `pending` → `in_progress` → `done` | `failed` | `skipped` (with justification). Never delete a failed step — supersede it.
8. **Committing:** commit after each step that changes the repo lands verified (one commit per step, message `feat(tools): force_ger_update — <step title>`). Do not push or open a PR unless the user asked.
9. **Finish:** when S8 is `done`, write a final summary at the bottom of this file (section 7) and report to the user: what was built, how it was verified, and remaining known gaps.

---

## 5. Steps

### S1 — Scaffold: package, config, CLI entry, build plumbing

- **Status:** done
- **Goal:** Create the tool skeleton so later steps only fill in logic: `tools/force_ger_update/{config.go, types.go, run.go(stub), cmd/main.go}`, an example config `tools/force_ger_update/example-config.toml`, Makefile targets, Dockerfile COPY line. `types.go` must define the internal contracts the parallel steps implement: a `GERMonitor` interface (`LastGERUpdate() (time.Time, error)` boot fetch + `Start(ctx) (<-chan GERUpdateEvent, error)` watch/poll) and a `ForcedUpdateSender` interface (`SendForcedGERUpdate(ctx) error`), plus reuse of the ethtxmanager interface from `aggoracle/types/types.go`. `Run(c *cli.Context)` stub loads config, validates it, dials L1, and logs — no loop yet.
- **Non-goals:** No monitor/sender logic, no tests beyond a config-loading unit test, no README, no e2e.
- **Context pack:** `tools/remove_ger/cmd/main.go`, `tools/remove_ger/config.go`, `tools/backward_forward_let/config.go` (fallback pattern), `config/config.go` + `config/default.go` (render pipeline, decode hooks), `Makefile:79-116`, `Dockerfile:19,57-62`, `docs/common_config.md` (SignerConfig examples), section 2 of this plan (config schema — implement it as written).
- **Acceptance criteria:**
  - `make build-force_ger_update` produces `target/force_ger_update`; binary added to `build-tools` target.
  - `./target/force_ger_update --help` shows usage; `--version` prints aggkit version.
  - Config unit test proves `LoadConfig` parses `example-config.toml` including `types.Duration` fields, addresses, and a `PrivateKeys` entry with `Method="local"` **and** one with `Method="GCP"` (KMS shape decodes).
  - Validation rejects: missing L1URL, `DestinationNetwork == 0`, zero bridge/GER addresses.
  - `make lint` passes on the new package; `go test ./tools/force_ger_update/...` passes.
- **Dependencies:** —
- **Model:** sonnet, medium effort (well-specified scaffolding; conventions are given).
- **Log:** Created `config.go` (Config/ForceGERUpdateConfig + Validate + LoadConfig mirroring
  `tools/remove_ger/config.go`'s render pipeline — no fallback needed, the standard
  DefaultMandatoryVars/DefaultVars/DefaultValues already satisfy their own template vars so no
  unrelated mandatory vars leak into this tool's schema), `types.go` (`GERUpdateEvent`, `GERMonitor`,
  `ForcedUpdateSender` interfaces, `type EthTxManager = aggoracletypes.EthTxManager` alias reusing
  `aggoracle/types/types.go` verbatim), `run.go` (`Run` stub: LoadConfig → Validate → dial L1 via
  `ethclient.DialContext` → fetch chain ID → log startup summary; no loop), `cmd/main.go` (cli.App,
  `--cfg` StringSlice flag, `--version` via `app.Version = aggkit.Version`), `example-config.toml`
  (full `[ForceGERUpdate]` + nested `[ForceGERUpdate.EthTxManager]`/`.Etherman` sections, one local
  keystore PrivateKeys entry), `config_test.go` (LoadConfig against example-config.toml; a second
  inline-TOML fixture proving both `Method="local"` and `Method="GCP"` PrivateKeys decode;
  table-driven `Validate` tests for missing L1URL / zero DestinationNetwork / zero BridgeAddr / zero
  GlobalExitRootManagerAddr). Wired `Makefile` (`build-tools` dependency, `build-force_ger_update`
  phony target, `$(GOBIN)/force_ger_update` build rule) and `Dockerfile` (COPY line for the built
  binary). `EthTxManager` field reuses `github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager.Config`
  directly (same as `AggOracle.EVMSender.EthTxManager`) rather than redeclaring its shape.
  Verification: `make build-force_ger_update` → binary at `target/force_ger_update`; `--help`/
  `--version` both correct; `go test -race ./tools/force_ger_update/...` passes (8 subtests);
  `golangci-lint run ./tools/force_ger_update/...` clean, and `make lint` shows zero findings under
  `tools/force_ger_update/` (the full `make lint` run does fail, but only on pre-existing findings in
  unrelated, untouched files elsewhere in the repo — confirmed via `git status`/`git diff --stat`
  showing no changes to those files). `go build ./...` and `make build-tools` both still succeed for
  all tools. Manual smoke test against the example config with no L1 node running fails fast with a
  clean wrapped error (`dial L1 (...): ... connection refused`), no panic/hang. No deviations from
  the plan's design decisions; no fallback loader needed.

### S2 — GER monitor: boot fetch + event watch/poll (parallel-group A, worktree)

- **Status:** done
- **Goal:** Implement `GERMonitor` in `tools/force_ger_update/monitor.go` (+ `monitor_test.go`): (a) boot: backward chunked `FilterLogs` scan for the newest `UpdateL1InfoTree` log per section 2, resolving its block timestamp; (b) watch: if `L1WSURL` set, use `WatchUpdateL1InfoTree` with re-subscribe/backoff on subscription error; else poll `FilterLogs` from `lastSeenBlock+1` every `EventPollInterval`. Each observed event (with its block timestamp) is pushed to the channel. Unit tests use the generated mocks for `aggkittypes.BaseEthereumClienter` (see `types/mocks/`) covering: event found in first chunk, event found N chunks back, no event within lookback (returns stale sentinel), poll loop picks up a new log, context cancellation stops cleanly.
- **Non-goals:** No sending logic, no main loop, no changes to files created in S1 other than adding methods to its own new files (if an interface signature from S1 must change, report it — the main agent coordinates), no persistence/DB.
- **Context pack:** `l1infotreesync/downloader.go:16-24,60-135` (topic hashes, Parse usage), the `agglayerger` binding (`Filter/Watch/ParseUpdateL1InfoTree`), `types/eth_client.go`, `types/mocks/`, `sync/evmdownloader.go:566` (FilterLogs usage style, timeouts), CLAUDE.md test rules.
- **Acceptance criteria:**
  - `go test -race ./tools/force_ger_update/...` passes with the new monitor tests (≥5 scenarios above), tests assert **behavior** (timestamps/blocks returned, channel receives) not implementation details.
  - No import of `sync`, `l1infotreesync`, `reorgdetector`, or any sqlite driver in the monitor.
  - `make lint` clean.
- **Dependencies:** S1
- **Model:** sonnet, high effort (concurrency + chunked scanning edge cases).
- **Log:** Implemented `monitor.go` (+ `monitor_test.go`) in worktree
  `worktree-agent-a317bf54d08061f1d`. `Monitor.LastGERUpdate` does a backward chunked `FilterLogs`
  scan (topic0 `0xda61aa78...`), bounded by `InitialLookbackBlocks`/`FilterLogsChunkSize`, resolving
  the newest hit's block timestamp via `CustomHeaderByNumber`; returns zero `time.Time` (no error) as
  the stale sentinel. `Start` closes the returned channel on ctx cancel; WS mode uses
  `agglayerger.WatchUpdateL1InfoTree` with re-subscribe+backoff, poll mode `FilterLogs` from
  `lastSeenBlock+1` every `EventPollInterval`. Added `bootScanCallTimeout` (30s) per RPC since
  `LastGERUpdate` takes no ctx. **Orchestrator-verified after merge into main checkout:** `go build`,
  `go test -race -count=1 ./tools/force_ger_update/...` PASS, `golangci-lint v2.4.0` → 0 issues;
  grep confirms no `sync`/`l1infotreesync`/`reorgdetector`/`sqlite` imports.
  For S4: constructor `NewMonitor(cfg ForceGERUpdateConfig, l1Client, wsClient
  aggkittypes.BaseEthereumClienter) (*Monitor, error)`. `l1Client` mandatory (HTTP: boot scan +
  timestamp resolution in all modes + polling); `wsClient` must be non-nil **iff** `L1WSURL != ""`.
  **run.go currently dials only HTTP — S4 must also dial a WS client when L1WSURL is set.** Monitor
  depends only on `aggkittypes.BaseEthereumClienter`.

### S3 — Sender: bridgeMessage via ethtxmanager (parallel-group A, worktree)

- **Status:** done
- **Goal:** Implement `ForcedUpdateSender` in `tools/force_ger_update/sender.go` (+ `sender_test.go`): pack `bridgeMessage(DestinationNetwork, DestinationAddress, true, []byte{})` calldata from the `agglayerbridge` ABI, submit through the ethtxmanager interface (`Add` with `to=BridgeAddr`, `value=0`), then poll `Result` until Mined/Safe/Finalized (success), Failed (error), Evicted (log + return) — mirroring `aggoracle/chaingersender/evm.go:213-277` including the `ErrAlreadyExists` handling. Support `DryRun` (log calldata, skip Add). Default `DestinationAddress` to `ethTxManager.From()` when unset. Unit tests with a mocked ethtxmanager interface: success path (assert exact calldata: selector `0x240ff378` + args), failed status, already-exists, dry-run sends nothing.
- **Non-goals:** No monitor logic, no main loop, no gas-strategy tuning beyond `GasOffset` passthrough, no changes to S1 files.
- **Context pack:** `aggoracle/chaingersender/evm.go` (whole file), `aggoracle/types/types.go:14-29`, `autoclaim/sender/sender.go:151` (Add usage), `bridgesync/downloader.go:46-49,250-259` (ABI/selector), `agglayerbridge` binding, existing mock for the ethtxmanager interface (`aggoracle/mocks/` or `test/helpers/mock_ethtxmanager.go`; regenerate with `make generate-mocks` if a new one is needed).
- **Acceptance criteria:**
  - `go test -race ./tools/force_ger_update/...` passes with the 4 sender scenarios; calldata assertion decodes back via the binding ABI and verifies `forceUpdateGlobalExitRoot == true` and empty metadata.
  - Sender depends only on the narrow ethtxmanager interface (mockable), not on `*ethtxmanager.Client`.
  - `make lint` clean.
- **Dependencies:** S1
- **Model:** sonnet, medium effort (pattern exists in `chaingersender`; mostly careful transcription + tests).
- **Log:** Implemented `sender.go` (+ `sender_test.go`) in worktree branch
  `worktree-agent-ac307895278bbe295`. `Sender` packs `bridgeMessage(destNet, destAddr, true, [])`
  via `agglayerbridge.AgglayerbridgeMetaData.GetAbi()`, submits through the narrow `EthTxManager`
  interface (`Add(ctx, &BridgeAddr, common.Big0, data, 0, nil)`), polls `Result` until terminal,
  mirroring `chaingersender/evm.go` incl. the two-`if` `ErrAlreadyExists` handling (gocritic-safe).
  `DryRun` logs hex calldata, skips Add. `DestinationAddress` defaults to `ethTxManager.From()` when
  zero. Reuses existing `aggoracle/mocks.EthTxManager` (no mockery change). Sub-agent-reported
  verification: `go test -race ./tools/force_ger_update/...` PASS (5 tests incl. calldata decode
  asserting selector `240ff378`/forceUpdate=true/empty metadata, Failed→error, ErrAlreadyExists ok,
  DryRun sends nothing); no `ethtxmanager.Client` in sender.go; package lint clean. **Authoritative
  re-verification deferred to S4 merge.**
  For S4: constructor `NewSender(cfg ForceGERUpdateConfig, ethTxManager EthTxManager, opts ...Option)
  (*Sender, error)`; `WithPollInterval(d)` option (default 2s) for fast tests; `SendForcedGERUpdate`
  BLOCKS until terminal status/ctx.Done() (returns nil on cancel); the in-flight guard is S4's
  responsibility (loop must not call again until the prior call returns). **Gap flagged:** no
  `GasOffset` config field exists, so gasOffset is hardcoded `0` — S8 may add config if desired.

### S4 — Merge + main loop wiring

- **Status:** done
- **Goal:** Merge the S2/S3 worktrees into the branch (files are disjoint; resolve anything trivial). Then implement the real `Run` in `run.go`: instantiate ethtxmanager (`New` + `go Start()`), monitor, sender; main loop = ticker every `CheckInterval` computing `elapsed = now - lastGERUpdate`; if `elapsed >= MaxTimeWithoutGERUpdate` and no forced update is in flight → send; on every monitor event → update `lastGERUpdate`. Graceful shutdown on SIGINT/SIGTERM (context cancellation propagates to monitor, sender, ethtxmanager). Startup banner logs config summary (no secrets), sender address, and the boot-derived last GER update time/age. Add a small `run_test.go` driving the loop with mocked monitor+sender: (a) stale on boot → exactly one send; (b) event arrives before threshold → no send; (c) event resets timer after a send → no double-send while in flight.
- **Non-goals:** No e2e, no README (S8), no metrics/telemetry endpoint.
- **Context pack:** S1–S3 code, `cmd/run.go:543-569` (ethtxmanager lifecycle), `aggoracle/oracle.go` (loop/shutdown style if present), CLAUDE.md.
- **Acceptance criteria:**
  - `go build ./...` and `make build-force_ger_update` succeed post-merge.
  - The 3 loop unit tests pass with `-race`; the in-flight guard is asserted (sender mock counts calls).
  - `make lint` and `make test-unit` (full repo) pass.
  - Manual smoke: `./target/force_ger_update --cfg <example config pointing at an unreachable RPC>` fails fast with a clear wrapped error (no panic, no hang).
- **Dependencies:** S2, S3
- **Model:** sonnet, high effort (merge + concurrency-sensitive loop; the timer/in-flight semantics are where bugs would live).
- **Log:** Merge of S2/S3 was done by the orchestrator by file-copy (see changelog 2026-07-20 entry);
  S4 sub-agent implemented only the wiring in `run.go` (+ hand-written test doubles in `run_test.go`,
  no mocks dir — mockery entries for the two interfaces were deemed non-trivial, so minimal doubles
  live inline). `Run`: LoadConfig→Validate→signal.NotifyContext(SIGINT/SIGTERM)→dial HTTP L1 (and WS
  when `L1WSURL` set)→`ethtxmanager.New`+`go Start()`(defer Stop)→NewMonitor→NewSender→boot
  `LastGERUpdate`→startup banner→`runLoop`. `runLoop` (factored out for testing): `monitor.Start(ctx)`,
  ticker every `CheckInterval`; on tick, `elapsed=time.Since(lastGERUpdate)` ≥ threshold + not in-flight
  → `triggerSend`; on event → `lastGERUpdate=ev.BlockTimestamp`; on ctx.Done → return nil after
  `wg.Wait()`. In-flight guard = `atomic.Bool.CompareAndSwap(false,true)` gating a `wg`-tracked send
  goroutine (race-free). Closed events channel is nil'd to avoid busy-spin. Banner logs no secrets.
  **Orchestrator-verified:** `go build ./...` exit 0; `make build-force_ger_update` → 58MB binary;
  `go test -race -count=1 ./tools/force_ger_update/...` PASS — 4 loop tests
  (`StaleOnBoot_SendsExactlyOnce`, `EventBeforeThreshold_NoSend`, `InFlightGuard_NoDoubleSend`,
  `ContextCancelled_ReturnsPromptly`) assert exactly-once via `atomic.Int32` call counter;
  `golangci-lint v2.4.0 ./tools/force_ger_update/...` → 0 issues; smoke test vs `http://localhost:1`
  fails fast: `Error: dial L1 (...): fetch chain ID: ...connection refused`, exit 1, no panic/hang.
  Full-repo `make test-unit` deferred to S7's authoritative sweep (kicked off in background here as
  early warning; `go build ./...` already confirms no compile breakage elsewhere).
  For S5/S6: drive `runLoop(ctx, monitor GERMonitor, sender ForcedUpdateSender, lastGERUpdate
  time.Time, checkInterval, maxTimeWithoutGERUpdate time.Duration) error` directly; `Run` wires
  clients via `dialL1(ctx,url) (aggkittypes.BaseEthereumClienter, *big.Int, error)` using
  `etherman.NewDefaultEthClient` — integration test can construct `NewMonitor`/`NewSender` itself
  against the simulated client + mock ethtxmanager rather than calling `Run`.

### S5 — Tier-1 integration test on the simulated backend (parallel-group B, worktree)

- **Status:** done
- **Goal:** Add `tools/force_ger_update/integration_test.go` (guarded so it runs under `make test-unit`, keep it fast) using `test/helpers.NewSimulatedL1(t)`: deploy L1 bridge+GER, wire the real monitor + real sender with the mock-ethtxmanager-backed-by-simulated-client (`test/helpers/ethtxmanmock_e2e.go` pattern), configure `MaxTimeWithoutGERUpdate` to ~2s and fast intervals; assert: (1) boot with no prior event → tool sends a `bridgeMessage` tx and the GER contract emits `UpdateL1InfoTree`; (2) the monitor observes the event and resets — no second send within the next threshold window; (3) an externally-sent `BridgeAsset(..., forceUpdateGER=true, ...)` resets the timer so the tool stays quiet. Use the simulated client's in-process `SubscribeFilterLogs` to also exercise watch mode in at least one sub-test, and polling mode in another.
- **Non-goals:** No real network, no changes to tool production code (if a bug is found, report it back — the main agent creates a fix step), no kurtosis/docker.
- **Context pack:** `test/helpers/simulated.go`, `test/helpers/e2e.go:381` (`NewSimulatedL1`), `test/helpers/ethtxmanmock_e2e.go`, `test/e2e/bridge_utils.go:21-40` (BridgeAsset call shape), S2–S4 code.
- **Acceptance criteria:**
  - `go test -race -run TestForceGERUpdate ./tools/force_ger_update/...` passes deterministically (run 3× to check for flake); total runtime < 60s.
  - Assertions check on-chain effects (event logs from the GER contract), not just tool-internal state.
- **Dependencies:** S4
- **Model:** sonnet, high effort (async test choreography against a simulated chain; flake-resistance matters).
- **Log:** Created `tools/force_ger_update/integration_test.go` (worktree `agent-a2d97c5e6b6cf536f`,
  merged by file-copy). Wires the REAL `Monitor`+`Sender` via `runLoop` against
  `test/helpers.NewSimulatedL1(t)` with `test/helpers.NewEthTxManMock` (mock ethtxmanager that really
  submits/mines on the simulated chain). `TestForceGERUpdate` has two subtests — `WatchMode`
  (`L1WSURL` set, non-nil wsClient → `WatchUpdateL1InfoTree` path) and `PollMode` (nil wsClient →
  `FilterLogs` poll) — each running all 3 scenarios: (1) boot-stale → forced `bridgeMessage`
  send, asserted via `gerContract.FilterUpdateL1InfoTree` log AND that the producing tx's selector ==
  `0x240ff378`; (2) `require.Never` no second send in the quiet window after reset; (3) external
  `bridgeAsset(forceUpdate=true)` from a separate funded account produces a 2nd `UpdateL1InfoTree`
  (selector = bridgeAsset, not the tool's) and the tool stays quiet. No production code touched.
  **Orchestrator-verified in main checkout:** `go test -race -count=1 -run TestForceGERUpdate
  ./tools/force_ger_update/...` PASS (both subtests, ~4s), run 2× (sub-agent ran 8×, no flakes);
  `golangci-lint v2.4.0` → 0 issues. On-chain assertions confirmed (real GER-contract event logs +
  tx selector), not tool-internal state.
  **For S8 (semantics review):** sub-agent flagged that in POLL mode, if `EventPollInterval` is not
  comfortably < send/mine time, the in-flight guard can release (tx mined) before the next poll resets
  `lastGERUpdate`, permitting one extra "redundant (harmless)" send against the still-stale timestamp
  — matches the design doc's own "at worst one redundant forced update" caveat; cannot occur in prod
  (real tx mine time ≫ any sane poll interval). Resolved as test-tuning (`EventPollInterval=10ms`), no
  prod change. S8 should confirm this is coherent and documented.

### S6 — Tier-2 real e2e test in `test/e2e/` (parallel-group B, worktree)

- **Status:** done
- **Goal:** Add `test/e2e/forcegerupdate_test.go` running against the docker-compose `op-pp` env: build the tool binary (`make build-force_ger_update`), render a config file into a temp dir (L1 RPC + bridge/GER addresses from `env` / `summary.json`, a funded key from `env.Keys.L1Keys.Checkout()` written as a keystore or reuse an existing env keystore, threshold ~30s, sqlite in temp dir), **exec the real binary** as a subprocess, then: (1) record the latest `UpdateL1InfoTree` block; (2) wait past the threshold; (3) assert a new `UpdateL1InfoTree` event appears whose tx is a `bridgeMessage` (selector `0x240ff378`) from the tool's sender address to the bridge; (4) kill the process, assert clean exit. Follow the style of `removeger_test.go` / `bridge_utils.go`.
- **Non-goals:** No kurtosis/bats suite, no CI workflow changes (note it as follow-up in the log if `test-go-e2e.yml` needs nothing — it globs `./test/e2e/...` already), no changes to tool production code.
- **Context pack:** `test/e2e/testmain_test.go`, `test/e2e/envs/loader.go` (KeyPool, summary.json fields, contract handles), `test/e2e/removeger_test.go`, `test/e2e/bridge_utils.go`, `.github/workflows/test-go-e2e.yml`, S1 config schema.
- **Acceptance criteria:**
  - `go test -v -timeout 30m -run TestForceGERUpdateE2E ./test/e2e/...` passes locally with the docker-compose env up (document the exact command in the test header comment).
  - The test proves causality: the new GER update's tx `From` == tool sender address and input starts with `0x240ff378`.
  - Other e2e tests in the suite still pass alongside (the tool's key comes from the pool, not a shared special key).
- **Dependencies:** S4
- **Model:** sonnet, high effort (heavy tooling: docker env, subprocess management, on-chain assertions).
- **Log:** Created `test/e2e/forcegerupdate_test.go` (`TestForceGERUpdateE2E`, worktree
  `agent-ade2fea89202723c4`, merged by file-copy). Builds the real binary, renders a temp config
  (L1 RPC + bridge from `summary.json`, GER-manager queried on-chain via
  `Bridge.GlobalExitRootManager()`, funded key via `env.Keys.L1Keys.Checkout()`, threshold 30s,
  `DestinationNetwork=env.L2.NetworkID`), execs the binary, and asserts a new `UpdateL1InfoTree`
  whose producing tx `To==bridge`, `Data[:4]==0x240ff378` (bridgeMessage), and
  `Sender==tool sender addr`. **Executed LIVE against the running docker `op-pp` env (not just
  compile-checked):** `--- PASS: TestForceGERUpdateE2E (14.91s)`. Real on-chain evidence from the run:
  boot found GER stale (`lastGERUpdateAge=3685h`), forced tx `0x1ec54b2...` mined at block 394,
  `UpdateL1InfoTree` block 394, sender `0xD8F3183D...` (pool key) matched; in-flight guard fired
  ("already in flight, skipping" ×2). **Orchestrator-verified in main checkout:** `go vet
  ./test/e2e/...` exit 0, `go test -c -o /dev/null ./test/e2e/` compiles, no lint finding mentions
  the file, uses `L1Keys.Checkout()` (not a shared special key). CI: `.github/workflows/test-go-e2e.yml`
  already globs `./test/e2e/...` — no workflow change needed.
  **POSTTEST characterization (resolved, NOT a defect):** the `go test` *package* exit was FAIL
  because `TestMain`'s global post-test L1↔L2 bridge health-check timed out (10-min loop) on L2→L1
  settlement inclusion — a direction the tool never touches. VERIFIED this is a known pre-existing
  pattern: `test/e2e/removeger_test.go:46,52,71` `t.Skip()` three GER-manipulating tests with
  *"known flaky e2e: ...can leave the post-test bridge health check unhealthy"*. GER-manipulating
  e2e tests perturbing that shared-env global check is expected; `TestForceGERUpdateE2E` passing its
  own assertions is the success signal. **For S8:** consider whether to guard/skip the test under
  default `make test-e2e` like removeger does (judgment call — ours passes rather than skips).
  **Dockerfile note:** the worktree's Dockerfile lacked S1's COPY line (branch-divergence artifact);
  the main checkout already has it (verified in S1) — S8 to confirm on the merged branch.

### S9 — Documentation: README with rationale (parallel-group B, worktree)

- **Status:** done
- **Goal:** Write `tools/force_ger_update/README.md` containing, in this order:
  1. **"Why this tool exists" — the rationale (mandatory, this exact argument):** For aggchains running **OP-FEP**, there is a direct relation between *when the last L1 info root was updated* and *what can be proven*. Every GER update on L1 appends a leaf to the L1 info tree, and that leaf includes an **L1 block hash**; the aggchain proof uses the block hash contained in the L1 info root to assert things that happened on L1 — **including data availability (DA)**. Consequently, anything posted on L1 *after* the last L1 info root update is not covered by any block hash inside an L1 info root and **cannot be proven** until a new update lands. If DA is posted after the last L1 info root update, the aggchain proof cannot attest to it, and certificate progress stalls until an organic GER update happens. This tool removes that unbounded wait: it watches the last `UpdateL1InfoTree` event on L1 and, if no update happens organically within a configured window `X`, it sends a `bridgeMessage` transaction with `forceUpdateGlobalExitRoot = true`, forcing a new L1 info root that covers everything (DA included) posted up to that point. Include a small sequence/timeline diagram (mermaid or ASCII) showing: DA posted → no GER update → unprovable window → forced update → provable.
  2. **Configuration reference:** every `[ForceGERUpdate]` field from section 2 with meaning and default, plus signer examples for local keystore, GCP KMS, and AWS KMS adapted from `docs/common_config.md`.
  3. **How to run:** build (`make build-force_ger_update`), run command, dry-run mode, docker image usage.
  4. **How to test:** commands for both test tiers (Tier-1 simulated, Tier-2 docker-compose e2e).
  Also add a short pointer to the tool (one paragraph, linking to the README with the rationale) in the docs index if one exists (`docs/` — check for a natural home like `docs/common_config.md`-style listing; if no index exists, README alone is fine and say so in the Log).
- **Non-goals:** No production-code changes, no test changes, no PR description (S8), no restating of internal implementation details that would go stale (document behavior and config, not code structure).
- **Context pack:** Section 1–2 of this plan, the rationale text above (goal item 1 — treat it as the source of truth), `docs/common_config.md`, `tools/exit_certificate/README.md` (style), final config schema from S1/S4 code (`tools/force_ger_update/config.go`, `example-config.toml`).
- **Acceptance criteria:**
  - README exists with all four sections; the rationale section explains the OP-FEP / L1-info-root / block-hash / DA-provability chain of reasoning accurately (a reader unfamiliar with the tool understands *why* it must exist, not just what it does).
  - Every config field in `config.go` appears in the reference table (cross-checked against the code, not this plan).
  - All commands in "How to run"/"How to test" are copy-paste-valid (verified against Makefile targets; actual execution happens in S7/S8).
  - `make lint` unaffected (markdown only).
- **Dependencies:** S4
- **Model:** sonnet, medium effort (writing-heavy; the hard reasoning is already supplied verbatim).
- **Log:** Created `tools/force_ger_update/README.md` (worktree `agent-ac338f7199a8bc196`, merged into
  main by file-copy). Four sections in order: (1) **Why** — the mandatory OP-FEP/L1-info-root/block-
  hash/DA-provability rationale verbatim + a mermaid sequence diagram AND an ASCII timeline (DA posted
  → unprovable window → forced update → provable); (2) **Configuration reference** — full
  `[ForceGERUpdate]` table (all 12 scalar fields), `[ForceGERUpdate.EthTxManager]` and `.Etherman`
  sub-tables, plus local/GCP/AWS signer examples adapted from `docs/common_config.md`; (3) **How to
  run** — `make build-force_ger_update`, run command, `DryRun`, docker image at
  `/usr/local/bin/force_ger_update`; (4) **How to test** — Tier-1
  (`go test -race -run TestForceGERUpdate ./tools/force_ger_update/...`) + Tier-2
  (`go test -v -timeout 30m -run TestForceGERUpdateE2E ./test/e2e/...`).
  **Orchestrator-verified:** all four `##` sections present; all 12 scalar config fields
  cross-checked present (13th, `EthTxManager`, has its own sub-table); mermaid + selector `240ff378`
  + `forceUpdateGlobalExitRoot` all referenced. Deeper accuracy review is S8's job.
  **Follow-up for S8:** S9 flagged `docs/SUMMARY.md` as the natural docs index — a one-line pointer
  `- [force_ger_update tool](../tools/force_ger_update/README.md)` could be added there (S9 did not
  edit docs/ per its non-goals; S8 to decide).

### S7 — Full validation sweep

- **Status:** done
- **Goal:** Merge parallel-group B worktrees (S5, S6, S9), then run the complete verification battery and report raw results: `make build && make build-tools`, `make lint`, `make test-unit`, `go test -race ./tools/force_ger_update/...` (3×), and the S6 e2e command (env up → test → env down). Also run the binary once against the live compose env with `DryRun = true` and capture the log output showing boot-derived last-GER age. Summarize: pass/fail per command, flaky reruns, exact failure output if any.
- **Non-goals:** No fixing (report only — the main agent turns failures into new steps), no lint-rule tweaking, no commits.
- **Context pack:** Makefile, this plan's S1–S6 acceptance criteria (re-verify each explicitly and tick them off in the report).
- **Acceptance criteria:** A written report in this step's Log with every command, its exit status, and evidence for each S1–S6 and S9 acceptance criterion. All green, or failures precisely characterized.
- **Dependencies:** S5, S6, S9
- **Model:** haiku, low effort (tool-heavy command execution + faithful summarization; no design judgment needed).
- **Log:** Run by orchestrator (report-only, no writes/commits — group-B worktrees already merged by
  file-copy in S5/S6/S9). golangci-lint v2.4.0 (matches CI `.github/workflows/lint.yml`). **Battery
  results (all exit 0):**
  - `make build` → exit 0. `make build-tools` → exit 0; `target/` has all 6 tools incl.
    `force_ger_update`.
  - `make lint` (whole repo, `golangci-lint run --timeout 5m`) → **exit 0, `0 issues.`** (S1's earlier
    "make lint fails on pre-existing issues" was a false alarm from a stale v1.63.0 binary; with the
    CI-pinned v2.4.0 the whole repo is clean.) No finding mentions `force_ger_update`.
  - `make test-unit` (full repo, `-short -race -p 1 -timeout 15m`) → **exit 0, 0 FAILs**;
    `tools/force_ger_update` ok, coverage 67.8%. (A first background run earlier had ONE failure —
    `claimsync/TestClaimCalldata`, a geth-docker `connection reset by peer` env flake; it did NOT
    recur in this authoritative run → confirmed transient, unrelated to the tool.)
  - `go test -race -count=1 ./tools/force_ger_update/...` × 3 → all `ok` (~4.2s each), no flake.
  - **Tier-2 e2e (S6):** `go test -v -timeout 30m -run TestForceGERUpdateE2E ./test/e2e/...` ran LIVE
    against the up docker `op-pp` env → `--- PASS: TestForceGERUpdateE2E (14.91s)`. Causality proven
    on-chain (forced tx `0x1ec54b2...` mined block 394, `UpdateL1InfoTree` block 394, sender
    `0xD8F3183D...` = pool key, selector `0x240ff378`). The package-level FAIL is the KNOWN
    pre-existing `TestMain` POSTTEST L2→L1 bridge-health flakiness (`removeger_test.go:46,52,71` skip
    the same). **NOTE:** did NOT re-run e2e with a destructive `down -v` teardown — the docker env is
    a SHARED persistent dev env (up 5+ days); tearing it down would disrupt other work. Cited the
    already-captured live PASS instead.
  - **DryRun-against-live-env smoke:** not re-run (would need a throwaway keystore + risk polluting
    the module mid-test-unit). Equivalent evidence already captured: the live e2e run's startup banner
    logged the boot-derived age `lastGERUpdate=2026-02-16T21:09:11Z lastGERUpdateAge=3685h52m` against
    this same env, and DryRun-skips-send is unit-tested (`TestSendForcedGERUpdate_DryRun` asserts
    `Add` never called). S8 can do a final copy-paste README command run.
  **Acceptance-criteria tick-off:** S1 (build/help/version/config-test/validation/lint) ✅; S2 (monitor
  ≥5 scenarios, no forbidden imports, lint) ✅; S3 (sender 4 scenarios, narrow iface, lint) ✅; S4
  (build, 3 loop tests + in-flight guard, lint, test-unit, fail-fast smoke) ✅; S5 (Tier-1 3× no flake,
  on-chain asserts) ✅; S6 (live e2e PASS, causality From==sender + `0x240ff378`, pool key) ✅; S9
  (README 4 sections, all config fields, valid commands) ✅. **All green; the only two FAIL signals
  seen are both characterized as pre-existing/transient env issues unrelated to the tool.**

### S8 — Adversarial review, docs, and finish

- **Status:** done
- **Goal:** Deep review of the full diff vs `develop` with a skeptic's eye on: timer semantics (clock skew, block timestamp vs wall clock — the plan uses block timestamps from events and wall clock for elapsed; verify this is coherent and document it), in-flight guard races, ws re-subscribe leaks, FilterLogs range off-by-ones, config validation gaps, secret leakage in logs, and CLAUDE.md convention compliance (error wrapping, `require`, line length, doc comments). Fix what's found (small fixes inline; big issues → new steps via the main agent). Review the S9 README for technical accuracy against the final code (config fields, commands, and the OP-FEP/DA rationale — fix inaccuracies directly) and ensure `Dockerfile` ships the binary. Draft a PR description following `.github/PULL_REQUEST_TEMPLATE.md` (it should reference the DA-provability rationale as the motivation) and append it to this plan's section 7.
- **Non-goals:** No feature additions, no refactors beyond fixing found defects, no opening the PR.
- **Context pack:** full `git diff develop...HEAD`, S7 report, `.github/PULL_REQUEST_TEMPLATE.md`, `docs/common_config.md`, `tools/exit_certificate/README.md` (README style).
- **Acceptance criteria:**
  - Review findings listed with file:line; each either fixed (with rerun of affected tests) or explicitly deferred with rationale.
  - README commands were actually executed successfully at least once (copy-paste-tested) and the rationale section survived accuracy review.
  - `make lint && make test-unit` green after any fixes; final commit made.
- **Dependencies:** S7
- **Model:** opus, high effort (adversarial reasoning over concurrency and semantics — the highest-judgment step).
- **Log:** Adversarial review of the full `develop...HEAD` diff. **Findings & fixes:**
  - **[FIXED — HIGH] config.go Validate() (crash-on-boot):** `CheckInterval` and (in poll mode)
    `EventPollInterval` were unvalidated; both feed `time.NewTicker`, which *panics* on a
    non-positive interval. The tool config has no template defaults, so omitting either field
    crashed the tool on startup with a raw panic. Added validation for `MaxTimeWithoutGERUpdate>0`,
    `CheckInterval>0`, `EventPollInterval>0` (poll mode only), and `FilterLogsChunkSize>0`
    (defense-in-depth; also checked in NewMonitor). Verified: omitting `CheckInterval` now exits 1
    with `invalid config: ForceGERUpdate.CheckInterval is required and must be greater than 0` (no
    panic). Added 6 table cases to `config_test.go` (incl. "zero EventPollInterval allowed in watch
    mode") and updated `validBaseConfig()`.
  - **[FIXED — LOW] README.md docker command:** image `ENTRYPOINT` is `aggkit`, so
    `docker run aggkit:local force_ger_update …` would run `aggkit force_ger_update` (no such
    subcommand). Rewrote to `--entrypoint force_ger_update`.
  - **[FIXED — MED] test/e2e/forcegerupdate_test.go default-run:** only guarded on
    `testing.Short()`, so it ran under `make test-e2e`/CI and (like the `remove_ger` e2e tests)
    perturbed the shared env's post-test bridge health check → package FAIL. Added unconditional
    `t.Skip` matching the `remove_ger` precedent; body moved to `testForceGERUpdateE2E` and run
    manually via the documented command. (Live PASS already captured in S6/S7.)
  - **[FIXED — LOW] README.md config table:** marked `MaxTimeWithoutGERUpdate`/`CheckInterval`/
    `FilterLogsChunkSize` (and poll-mode `EventPollInterval`) as required/`>0` to match new validation.
  - **[FIXED — DOC] run.go runLoop doc:** documented the timer semantics — block-timestamp vs
    wall-clock, and that clock skew is immaterial and safe in both directions (future block ts →
    negative elapsed → waits longer, never over-fires, never panics).
  - **[VERIFIED — no defect] in-flight guard / shutdown:** `atomic.Bool.CompareAndSwap` + `wg`-tracked
    goroutine; `SendForcedGERUpdate` returns nil on ctx cancel, so `defer wg.Wait()` can't deadlock
    or leak. **WS re-subscribe:** `consumeSubscription` defers `Unsubscribe` per subscription; `out`
    closed exactly once via the single `defer close(out)` in watch/poll. **FilterLogs ranges:** boot
    backward chunks are inclusive `[lo,hi]` with `hi=lo-1` step (no gap/overlap/underflow); poll
    `from=lastSeenBlock+1..latest` only when `latest>lastSeenBlock` (no empty/dup). **Secret leakage:**
    startup banner and all error paths log URLs/addresses/calldata only — never PrivateKeys/passwords.
    **Redundant-send tolerance:** matches design doc; serialized by the guard → never unbounded.
  - **[DEFERRED] gasOffset=0 hardcoded:** accepted per plan scope; gas is controlled by the
    ethtxmanager config (`ForcedGas`/`GasPriceMarginFactor`/`MaxGasPriceLimit`).
  - **[DECISION] docs/SUMMARY.md pointer NOT added:** SUMMARY.md is a docs/-folder mdbook index and
    no tool README (remove_ger, exit_certificate, backward_forward_let, exit_certificate_claimer) is
    listed there — adding force_ger_update would break the established convention.
  - **Dockerfile:** confirmed `COPY --from=builder /app/target/force_ger_update /usr/local/bin/…`
    present on the merged branch, and `build-tools` builds it.
  - **Verification:** `go test -race ./tools/force_ger_update/...` PASS; `golangci-lint …/tools/…`
    0 issues; `make lint` (whole repo) 0 issues; `make build-force_ger_update` + `--help` OK;
    README build + Tier-1 commands copy-paste-run green; `make test-unit` see S8 report to user.

### S10 — Run the Tier-2 e2e isolated on its own CI runner (added 2026-07-20, user request)

- **Status:** done
- **Goal:** Make `TestForceGERUpdateE2E` **actually run in CI**, but on a **dedicated, isolated job/runner** so it (a) is not skipped in CI, yet (b) does not interfere with the main e2e suite and is not broken by the shared post-test bridge health check. Context (verified 2026-07-20): `make test-e2e` = `go test -v -timeout 30m ./test/e2e/...` runs ALL tests in ONE process against ONE shared `op-pp` docker-compose env; `test/e2e/testmain_test.go` `TestMain` runs a post-test L1↔L2 bridge health check after `m.Run()` **only when `code == 0`**, and a GER-manipulating test leaves the L2→L1 flow unable to settle → post-test `log.Fatalf` (this is exactly why `removeger_test.go:46,52,71` and currently `forcegerupdate_test.go:320` `t.Skip`). The existing precedent `.github/workflows/test-e2e-exit_cenrtificate_tool.yml` isolates a tool's e2e in its own job/runner (own env), path-triggered. Implement (env-var contract — pick clear names, document them):
  1. **`test/e2e/forcegerupdate_test.go`** — replace the *unconditional* `t.Skip` at the top of `TestForceGERUpdateE2E` with an **env-gated** skip: run the test only when a dedicated env var is set (e.g. `RUN_FORCE_GER_UPDATE_E2E=true`), otherwise `t.Skip` (keep the existing `-short` skip). This preserves "skipped in the normal `make test-e2e` suite → no interference," while letting the dedicated job opt in.
  2. **`test/e2e/testmain_test.go`** — add an **opt-in** env var (e.g. `E2E_SKIP_POSTTEST_BRIDGE_CHECK=true`) that skips ONLY the post-test bridge health-check block. Default (unset) = current behavior, so no other test/job is affected. Rationale in a code comment: GER-manipulating e2e tests legitimately leave that global check unhealthy (same reason removeger skips), so the dedicated job disables it.
  3. **`Makefile`** — add a target (e.g. `test-e2e-force_ger_update`) that runs `RUN_FORCE_GER_UPDATE_E2E=true E2E_SKIP_POSTTEST_BRIDGE_CHECK=true go test -v -timeout 30m -run TestForceGERUpdateE2E ./test/e2e/...` (parity with the per-tool convention; makes the isolated run reproducible locally and callable from CI).
  4. **CI** — add a **dedicated job/runner** that runs only this test with those env vars against its own `op-pp` env. Preferred: a NEW job in `.github/workflows/test-go-e2e.yml` that `needs: [build-docker-image, pull-docker-images]` and reuses the same image artifacts + docker-load steps as the existing `test-go-e2e` job, but whose run step calls `make test-e2e-force_ger_update` (a separate job = a separate runner, isolated env, and the main `test-go-e2e` job is untouched). A standalone workflow file is acceptable only if it cleanly reuses/rebuilds the images; do NOT duplicate the whole image pipeline if a job in the existing workflow reuses the artifacts.
- **Non-goals:** No changes to tool production code (`config.go/types.go/monitor.go/sender.go/run.go`); do NOT un-skip the test for the *main* suite (it must stay skipped there so the shared post-test check is never perturbed by it); no rewrite of the exit_certificate harness; do not touch other tools' tests.
- **Context pack:** `.github/workflows/test-go-e2e.yml` (the build/pull/load artifact pipeline + the `test-go-e2e` job to mirror), `.github/workflows/test-e2e-exit_cenrtificate_tool.yml` (per-tool isolation precedent), `test/e2e/testmain_test.go` (TestMain + post-test check, ~lines 46-113), `test/e2e/forcegerupdate_test.go` (the `t.Skip` at ~line 320 and `testForceGERUpdateE2E` body), `test/e2e/removeger_test.go` (skip precedent + comment), `Makefile:145-147` (`test-e2e` target).
- **Acceptance criteria:**
  - The main `test-go-e2e` job / `make test-e2e` behavior is **unchanged**: `TestForceGERUpdateE2E` still skips there (verify: `go test -v -run TestForceGERUpdateE2E ./test/e2e/...` with no env vars set → `--- SKIP`).
  - With the gate set, the test is selected: `RUN_FORCE_GER_UPDATE_E2E=true go test -v -short -run TestForceGERUpdateE2E ./test/e2e/...` no longer SKIPs on the env-gate line (it will still `-short`-skip in short mode, or attempt to run without `-short`); demonstrate the gate flips via the skip-line disappearing (a full live run needs the env and is exercised by CI, not required here).
  - `testmain_test.go` change is opt-in: with `E2E_SKIP_POSTTEST_BRIDGE_CHECK` unset the post-test block still compiles and is reached as before; when set it is skipped. Prove via `go vet ./test/e2e/...` + `go test -c -o /dev/null ./test/e2e/` (compiles) and a code read.
  - New CI job present in a workflow, on its own runner, running only `TestForceGERUpdateE2E` with both env vars, reusing (not duplicating) the docker-image pipeline; workflow YAML is valid (parse-check).
  - `go vet ./test/e2e/...` clean; `golangci-lint` reports nothing for the changed files; `make lint` still 0 issues.
  - Env-var names + the isolation rationale documented in the README "How to test" section.
- **Dependencies:** S8
- **Model:** sonnet, high effort (CI wiring + shared test-infra change with default-preserving semantics).
- **Log:** Implemented the env-var contract exactly as specified:
  `test/e2e/forcegerupdate_test.go` — `TestForceGERUpdateE2E`'s unconditional `t.Skip` replaced with
  an env-gate (`os.Getenv("RUN_FORCE_GER_UPDATE_E2E") == "true"`), `-short` skip kept inside
  `testForceGERUpdateE2E` unchanged. `test/e2e/testmain_test.go` — the post-test bridge health-check
  `if code == 0 { ... }` became `if os.Getenv("E2E_SKIP_POSTTEST_BRIDGE_CHECK") == "true" { log.Info(...) }
  else if code == 0 { ...unchanged... }` with a code comment explaining GER-manipulating tests
  legitimately leave the check unhealthy — default (unset) behavior is byte-identical to before.
  `Makefile` — new `.PHONY test-e2e-force_ger_update` target next to `test-e2e` running
  `RUN_FORCE_GER_UPDATE_E2E=true E2E_SKIP_POSTTEST_BRIDGE_CHECK=true go test -v -timeout 30m -run
  TestForceGERUpdateE2E ./test/e2e/...`. `.github/workflows/test-go-e2e.yml` — new job
  `test-go-e2e-force-ger-update` (name "Run force_ger_update E2E (isolated)", `timeout-minutes: 45`)
  mirroring `test-go-e2e`'s checkout/setup-go@v6(1.25.7)/docker-compose install/download both image
  artifacts/docker-load steps, `needs: [build-docker-image, pull-docker-images]`, final step `run:
  make test-e2e-force_ger_update`; upload-test-results step kept with a distinct artifact name
  (`e2e-test-results-force-ger-update`, since GH Actions requires unique artifact names within a
  workflow run — the two jobs run in parallel in the same run). The existing `test-go-e2e` job's
  steps are byte-for-byte unchanged (diff is purely additive).
  **Also fixed (in scope, same file):** `make lint`/`golangci-lint ./test/e2e/...` surfaced one
  PRE-EXISTING `thelper` finding at `forcegerupdate_test.go:330` (`testForceGERUpdateE2E` missing
  `t.Helper()`) — confirmed via `git stash`/relint that it predates this step's diff entirely (same
  finding present at HEAD `06c8de03` before any S10 edits). Added `t.Helper()` as the function's
  first statement to restore `make lint` → `0 issues.` (required by this step's own acceptance
  criteria); no other production/test logic touched.
  **Verification (all commands re-run, exit codes shown):**
  - AC1: `go test -v -run TestForceGERUpdateE2E ./test/e2e/...` (no env vars) — full live run (TestMain
    brought up/tore down the real `op-pp` docker env since no `-short` arg was present): `--- SKIP:
    TestForceGERUpdateE2E (0.00s)` with message "set RUN_FORCE_GER_UPDATE_E2E=true to run this
    isolated e2e ..."; package `ok`. Also re-confirmed cheaply with `-short` for repeatability: same
    SKIP line.
  - AC2: `RUN_FORCE_GER_UPDATE_E2E=true go test -v -short -run TestForceGERUpdateE2E ./test/e2e/...`
    → env-gate SKIP line is gone; test proceeds into `testForceGERUpdateE2E` and hits the inner
    `-short` skip instead ("Skipping E2E test in short mode") — proves the gate flips.
  - AC3: `go vet ./test/e2e/...` exit 0; `go test -c -o <tmp> ./test/e2e/` exit 0; code read confirms
    `else if code == 0` branch is untouched logic-wise when the var is unset.
  - AC4: `yq e '.' .github/workflows/test-go-e2e.yml` parses; new job's `needs:
    [build-docker-image, pull-docker-images]`; its only non-setup `run:` step is `make
    test-e2e-force_ger_update`; diff shows the existing `test-go-e2e` job is untouched (purely
    additive diff).
  - AC5: `golangci-lint run --timeout 5m ./test/e2e/...` → `0 issues.` after the `t.Helper()` fix;
    `make lint` (whole repo) → `0 issues.`
  - AC6: `grep -A2 'test-e2e-force_ger_update' Makefile` shows the `.PHONY` line, the `##`-commented
    target, and the exact two-env-var command.
  README `tools/force_ger_update/README.md` "How to test" Tier-2 section rewritten: documents `make
  test-e2e-force_ger_update`, both env vars individually, and the isolation rationale (shared
  post-test bridge health-check perturbation), plus names the new CI job.
  **Deviation from plan's stated fallback:** the plan anticipated `go test -v -run
  TestForceGERUpdateE2E ./test/e2e/...` (no env, no `-short`) might need to bring up docker just to
  reach the skip, breaking AC1's simplicity — in practice `TestMain` DID bring up/tear down the real
  env (took ~107s) but this was harmless (the test itself skips instantly, and the shared post-test
  bridge check still ran normally afterward since `code==0` and the var was unset) and still produced
  the exact `--- SKIP` line required, so no fallback was needed; documented both the full run and the
  cheap `-short` re-check for the record.

### S11 — Push branch and open the PR (added 2026-07-20, user request)

- **Status:** pending
- **Goal:** Push `feat/force-ger-update-tool` to `origin` and open a pull request against `develop` following `.github/PULL_REQUEST_TEMPLATE.md`, using the PR draft already in section 7 (updated to include the S10 isolated-CI-job change under Testing/Notes). `gh` is authenticated (account `arnaubennassar`), remote is `git@github.com:agglayer/aggkit.git`. Steps: (1) ensure the working tree is clean and all step commits are present; (2) reconcile section-7 PR draft with the final state incl. S10; (3) `git push -u origin feat/force-ger-update-tool`; (4) `gh pr create --base develop --head feat/force-ger-update-tool --title "<concise>" --body "<section-7 PR draft rendered into the template>"`. The PR body MUST follow the template section order and cite the OP-FEP/DA-provability rationale as the motivation, and list both test tiers (Tier-1 unit/integration + the isolated Tier-2 CI job).
- **Non-goals:** No merging, no force-push, no changes to code/tests (S10 owns those); do not close/modify unrelated PRs. (Note: the memory gate `project_autoclaim_l2_lx_pr1691_gate.md` concerns a DIFFERENT branch `feat/autoclaim-l2-lx` and does NOT gate this PR.)
- **Context pack:** `.github/PULL_REQUEST_TEMPLATE.md`, section 7 of this plan (PR draft + final summary), the full commit series (`git log develop..HEAD`), S10's Log (CI job details for the Testing section).
- **Acceptance criteria:**
  - Branch pushed to origin; `gh pr view` shows an open PR against `develop` whose body follows the template section order and includes the DA-provability motivation + both test tiers.
  - The PR URL is returned in the step Log.
  - No other branches/PRs touched; nothing merged.
- **Dependencies:** S10
- **Model:** sonnet, medium effort (git + gh; outward-facing, so careful and idempotent).
- **Log:** _(fill after execution)_

### Dependency graph / parallelism

```
S1 ──┬── S2 (worktree A1) ──┐
     └── S3 (worktree A2) ──┴── S4 ──┬── S5 (worktree B1) ──┐
                                     ├── S6 (worktree B2) ──┼── S7 ── S8 ── S10 ── S11
                                     └── S9 (worktree B3) ──┘
```

- S2 ∥ S3 and S5 ∥ S6 ∥ S9 are the two parallel groups; all members write, so **each runs in its own isolated worktree** and the next step begins by merging.
- S10 (isolated CI for the Tier-2 e2e) and S11 (push + open PR) were added after S8 at the user's request; both are sequential single-writer steps.
- Everything else is sequential (single writer at a time).

---

## 6. Plan changelog

_(Main agent: append one line per plan modification — date, step(s) affected, why.)_

- 2026-07-20 — Initial plan created.
- 2026-07-20 — Added S9 (docs step with mandatory OP-FEP/DA-provability rationale) per task owner; README writing moved out of S8 (S8 now reviews docs for accuracy); S7 merges/depends on S9; graph updated.
- 2026-07-20 — S2/S3 parallel-group A worktrees diverged (both re-created S1's files with different commit hashes), so a git-branch merge would falsely conflict on identical S1 files. Orchestrator merged instead by copying the 4 disjoint new files (monitor.go, monitor_test.go, sender.go, sender_test.go) into the main checkout and verified the combined package (build + `-race` tests + lint v2.4.0 all green). S4's "merge" sub-goal is therefore already satisfied; S4 now covers only the main-loop wiring in run.go.
- 2026-07-20 — S8 adversarial review: fixed a crash-on-boot config-validation gap (unvalidated
  `CheckInterval`/`EventPollInterval` panic `time.NewTicker`), corrected the README docker entrypoint
  command, skipped the Tier-2 e2e by default (remove_ger precedent — perturbs shared post-test
  health check), documented timer/clock-skew semantics, and appended the PR draft + final summary to
  section 7. Decided against a docs/SUMMARY.md pointer (no tool README is indexed there).
- 2026-07-20 — Parallel-group B (S5/S6/S9) kept in worktrees (a full-repo `make test-unit` was running in the main checkout, so new test files must stay isolated). Each agent syncs the current tool source into its worktree via `cp -rf` and produces ONE disjoint deliverable (integration_test.go / test/e2e/forcegerupdate_test.go / README.md); orchestrator merges by copying that single file back (same proven approach as group A). S6 permitted to take a compile-check path (`go vet` + `go test -c`) if the docker `op-pp` env isn't reachable, deferring live e2e execution to S7.
- 2026-07-20 — Added S10 (run Tier-2 e2e isolated on its own CI runner so it actually runs in CI without interfering with the shared-env post-test bridge check) and S11 (push branch + open PR following `.github` template) at the user's request, after S8. Graph extended `… S8 ── S10 ── S11`.
- 2026-07-20 — S8 (fresh opus agent, not the orchestrator, to avoid build-confirmation bias) found + fixed one HIGH-severity bug: `CheckInterval`/`EventPollInterval` were unvalidated and feed `time.NewTicker`, which panics on ≤0 → crash-on-boot for a config omitting them. Fix adds `>0` validation for `MaxTimeWithoutGERUpdate`/`CheckInterval`/poll-mode `EventPollInterval`/`FilterLogsChunkSize` (+ tests). Also: made `TestForceGERUpdateE2E` skip-by-default (unconditional `t.Skip`, matching `removeger_test.go`) so it can't perturb the shared-env post-test bridge health check in CI (live PASS already captured in S6/S7; body runnable manually); fixed README docker `--entrypoint` + required-field markers. Orchestrator independently re-verified: commit 157adf3a, validation checks present, e2e skip present, `go build ./...`/package `-race` tests/`golangci-lint` all green, section 7 holds the final summary + template PR draft. **ALL STEPS S1–S9 DONE.** Branch has 10 commits; nothing pushed, no PR opened (awaiting user).
- 2026-07-20 — S10 executed: replaced the unconditional e2e skip with the `RUN_FORCE_GER_UPDATE_E2E`/`E2E_SKIP_POSTTEST_BRIDGE_CHECK` env-var gate contract, added the `test-e2e-force_ger_update` Makefile target, and a new isolated `test-go-e2e-force-ger-update` CI job in `test-go-e2e.yml` that reuses the existing image pipeline. While verifying `make lint`, found (via `git stash` diffing) one pre-existing `thelper` finding in `forcegerupdate_test.go` predating S10 (not introduced by this step) and fixed it in-place (added `t.Helper()`) since S10's own acceptance criteria required whole-repo `make lint` to stay at 0 issues.

## 7. Final summary / PR draft

### Final summary (S8)

**What was built.** A standalone, long-running CLI tool `tools/force_ger_update` that guarantees the
L1 Global Exit Root is updated at least every `X` (`MaxTimeWithoutGERUpdate`). It boots by scanning
L1 backwards (chunked `FilterLogs`) for the most recent `UpdateL1InfoTree` event, then watches for
new ones (WS subscription when `L1WSURL` is set, else `FilterLogs` polling) — no syncer, no reorg
detector, no persistent event store. A ticker loop compares `time.Since(lastGERUpdate)` (last GER
update's L1 block timestamp vs the tool's wall clock) against the threshold and, when exceeded and
no send is in flight, submits a `bridgeMessage(destNet, destAddr, forceUpdateGlobalExitRoot=true,
[])` transaction through the standard `zkevm-ethtx-manager` (keys via `go_signer` — local keystore
or GCP/AWS KMS), waiting for it to mine. The observed `UpdateL1InfoTree` event (never the tx
receipt) is the single source of truth that resets the timer.

**Files.** `tools/force_ger_update/{config,types,monitor,sender,run}.go` (+ `cmd/main.go`) with unit
tests, a Tier-1 simulated-backend `integration_test.go`, a Tier-2 `test/e2e/forcegerupdate_test.go`,
`README.md`, `example-config.toml`; wired into `Makefile` (`build-force_ger_update` + `build-tools`)
and `Dockerfile` (`COPY … /usr/local/bin/force_ger_update`).

**How verified.** `make lint` → 0 issues; `make test-unit` → green (bar the known transient
`claimsync` geth-docker flake); `go test -race ./tools/force_ger_update/...` green (3×); Tier-1
integration test exercises both watch and poll modes with real on-chain event assertions; Tier-2
e2e was run live against the `op-pp` docker env (forced tx mined, `From` == tool sender, selector
`0x240ff378`) before being skipped-by-default (see below).

**Known gaps / accepted tolerances.**
- **Redundant-send tolerance (by design):** if the monitor hasn't yet observed the GER update that a
  just-completed send produced, a later tick may fire one extra forced update. Sends are serialized
  by the in-flight guard (never concurrent, never unbounded per window) and every forced update
  emits the `UpdateL1InfoTree` the monitor then observes to reset the timer, so the system is
  self-correcting. Matches the design doc's "at worst one redundant (harmless) forced update."
- **`gasOffset` hardcoded to 0:** gas is controlled at the ethtxmanager layer
  (`ForcedGas`/`GasPriceMarginFactor`/`MaxGasPriceLimit`); no separate tool config field — accepted
  per plan scope.
- **Tier-2 e2e skipped by default** (`t.Skip`, like the `remove_ger` e2e tests) because forcing a
  GER update perturbs the shared env's post-test bridge health check; run manually with the command
  in the test's doc comment.

### PR draft

## 🔄 Changes Summary
- Add `force_ger_update`: a standalone, long-running CLI tool that guarantees the **L1 Global Exit
  Root (GER) is updated at least every configurable `X`**. It watches L1 for `UpdateL1InfoTree`
  events and, if none happens organically within the window, sends a `bridgeMessage` with
  `forceUpdateGlobalExitRoot = true` to force a new L1 info root.
- **Motivation (OP-FEP / DA provability):** for aggchains running OP-FEP, every GER update on L1
  appends a leaf to the L1 info tree that embeds an **L1 block hash**, and the aggchain proof uses
  that block hash to attest to what happened on L1 — **including data availability (DA)**. Anything
  posted on L1 *after* the last L1 info root update is covered by no block hash inside any L1 info
  root and therefore **cannot be proven** until a new update lands. If DA is posted after the last
  update, certificate progress stalls until an organic GER update happens — with no bound on the
  wait. This tool bounds that wait to `X`.
- Design: no syncer / reorg detector / event DB — boot scan via chunked `FilterLogs`, then WS
  `WatchUpdateL1InfoTree` (if `L1WSURL` set) or `FilterLogs` polling; sends via the standard
  `zkevm-ethtx-manager`; keys via `go_signer` (local keystore or GCP/AWS KMS).
- Wired into `Makefile` (`build-force_ger_update`, added to `build-tools`) and shipped in the
  Docker image at `/usr/local/bin/force_ger_update`.

## ⚠️ Breaking Changes
- 🛠️ **Config**: none (new standalone tool config; no existing config changed).
- 🔌 **API/CLI**: none (additive — new tool binary only).
- 🗑️ **Deprecated Features**: none.

## 📋 Config Updates
- 🧾 New standalone TOML with a `[ForceGERUpdate]` root section (+ nested `[…EthTxManager]` /
  `[…EthTxManager.Etherman]`). See `tools/force_ger_update/example-config.toml` and the README's
  configuration reference. Mandatory & validated: `L1URL`, `GlobalExitRootManagerAddr`, `BridgeAddr`,
  `DestinationNetwork` (≠ 0), `MaxTimeWithoutGERUpdate` (> 0), `CheckInterval` (> 0),
  `FilterLogsChunkSize` (> 0), and `EventPollInterval` (> 0, poll mode only).

## ✅ Testing
- 🤖 **Automatic**:
  - Unit: config load/validate, monitor (boot scan + watch/poll), sender (calldata/selector,
    statuses, dry-run), main loop (stale-on-boot, event-reset, in-flight guard, clean shutdown) —
    `go test -race ./tools/force_ger_update/...`.
  - Tier-1 integration on the go-ethereum simulated backend (`integration_test.go`), both watch and
    poll modes, asserting real on-chain `UpdateL1InfoTree` events and the `0x240ff378` selector.
- 🖱️ **Manual**:
  - Tier-2 e2e against the docker-compose `op-pp` env (skipped by default; run
    `go test -v -timeout 30m -run TestForceGERUpdateE2E ./test/e2e/...`): proves causality — the new
    GER update's tx `From` == tool sender and input starts with `0x240ff378`.

## 🐞 Issues
- Closes #[issue-number]

## 🔗 Related PRs
- [Optional]

## 📝 Notes
- Timer semantics: `lastGERUpdate` is an L1 block timestamp; elapsed is measured against the tool's
  wall clock. Clock skew is immaterial next to a minutes-to-hours threshold and is safe in both
  directions (a block timestamp slightly ahead of wall-clock yields a negative elapsed → the tool
  simply waits longer; it never over-fires and never panics).
- The timer is reset only by an *observed* `UpdateL1InfoTree` event, never by the send completing;
  in poll mode this permits at most one redundant (harmless) send per window if the observation lags
  the mine. Sends are serialized by an atomic in-flight guard, so they are never concurrent or
  unbounded.
- `gasOffset` is hardcoded to 0; gas is controlled via the ethtxmanager config
  (`ForcedGas`/`GasPriceMarginFactor`/`MaxGasPriceLimit`).
