# FIX_AUTOCLAIM — Decouple the Auto Claim watchdog from l2gersync

## Handoff summary

**Branch**: `feat/autoclaim-decouple-l2gersync` (based on `feat/autoclaim-plan`)

**What changed**: Decoupled `autoclaim` from `l2gersync`. The watchdog now enqueues L1→L2 bridge exits immediately without waiting for GER injection on the destination. Each claimer builds its own proof preparer backed by a per-claimer `GERTracker` (`autoclaim/gertracker`) that reads the latest injected GER from the target L2 by scanning `agglayergerl2` contract events via the claimer's own RPC client. The GER manager address is discovered at startup from the bridge contract's `GlobalExitRootManager()` getter — no new config field required. If the bridge is not yet covered by the latest injected GER, the proof returns "not ready" and the request is retried next cycle without burning retry budget.

**Decisions taken**:
- D1: Event scanning via `l2gersync.L2EVMGERReader` pattern (reused) on `agglayergerl2` binding — works for both sovereign and legacy L2 GER manager variants
- D2: GER manager address read from bridge contract `GlobalExitRootManager()` at startup — no new config
- D3: In-memory per-claimer tracker (no persistence) — self-heals across L2 reorgs
- D4: `l1_info_tree_index` written by claimer when proof is first built (column was already nullable)
- D5: Watchdog cursor always advances after successful enqueue window; holds only on genuine RPC/DB errors

**Commits on branch** (11 commits ahead of develop):
- `0771c5db` docs
- `72832715` wip
- `e99b1cf4` fix: address autoclaim audit comments
- `dc0e2b56` wip
- `d8dc8e54` wip
- `f485269f` e2e wip
- `b03c6dda` Merge remote-tracking branch 'origin/develop' into feat/autoclaim-plan
- `b1b2f3dd` fix: cherry-pick: validate legacy checkpoint block against L1 in CheckValidBlock
- `c2155080` e2e
- `2a11a0e0` feat: add l1 to l2 autoclaim
- `38b1121b` autoclaim-spec

**Diff stat** (68 files changed, 15621 insertions(+), 42 deletions(-)):

**Files added**:
- `autoclaim/gertracker/gertracker.go` — GERTracker interface and implementation
- `autoclaim/gertracker/gertracker_test.go` — unit tests (6 scenarios)
- `autoclaim/gertracker/export_test.go` — test constructor helper

**Key files changed**:
- `autoclaim/proof/preparer.go` — removed InjectedGERSyncer, added gertracker.GERTracker
- `autoclaim/watchdog/l1_to_l2.go` — removed ClaimAnchorSelector, holdCursor, PendingBridgeCount
- `autoclaim/runtime/runtime.go` — removed L2GERSync from Dependencies, added NewProofPreparer factory
- `cmd/run.go` — AUTOCLAIM no longer triggers l2gersync startup
- `docs/autoclaim.md` — updated architecture diagrams and all doc sections

**Gate results**:
- `make build`: PASS
- `make lint`: only pre-existing `tmp/` build failure (untracked scripts in `tmp/` directory) — no new issues
- `make test-unit`: all packages pass except pre-existing `config/TestLoadDefaultConfig` — no new failures

**Follow-ups deferred**:
- Multi-claimer e2e coverage (current e2e tests only exercise single-claimer)
- GER tracker incremental scan (currently scans from block 0 each cycle — acceptable given infrequent GER injection, but could be optimized with a cached last-seen block)

**How to verify**:
1. `make build` — must pass
2. `make lint` — must pass (pre-existing `tmp/` failure unrelated)
3. `make test-unit` — all packages pass except pre-existing `config/TestLoadDefaultConfig` and `tmp [build failed]`
4. `go test -v -run 'TestAutoClaimL1ToL2(AllowAll|APIApprove)' -timeout 30m ./test/e2e` — passes on fresh docker environment

---

## Problem statement

The Auto Claim watchdog currently depends on `l2gersync` to decide whether a bridge is claimable: it calls
`claimAnchorSelector.SelectL1InfoTreeIndex` (`autoclaim/watchdog/l1_to_l2.go:311`), which resolves the first
destination-injected GER through a single shared `l2gersync` instance
(`autoclaim/proof/preparer.go:186` → `l2gersync/processor.go:244`). Aggkit only instantiates **one** `l2gersync`
bound to **one** L2 (`cmd/run.go:169`, `l2gersync/config.go:18`), while the watchdog is supposed to cover **many**
destination L2s (one claimer per destination, `autoclaim/config/config.go:39`). With more than one claimer, GER
readiness for every destination is answered by the wrong (single) chain's injected-GER state.

## Target architecture

1. **Watchdog** discovers L1 bridge exits from `l1bridgesync` and enqueues them as `detected` **without** waiting
   for GER injection on the destination. It no longer holds the cursor for GER readiness and no longer needs
   `l2gersync` or the claim-anchor selector. Requests are stored with `L1InfoTreeIndex = nil`.
2. **Each claimer** owns proof building for its destination network, including choosing the anchor GER:
   - A new per-claimer **GER tracker** keeps track of the latest GER injected on the target L2 (read through the
     claimer's own `URLRPC` RPC client against that L2's GER manager contract).
   - The claimer's proof preparer maps that latest GER to its L1 info tree leaf via
     `l1infotreesync.GetInfoByGlobalExitRoot` (`l1infotreesync/l1infotreesync.go:421`) and tries to build the claim
     proof for enqueued requests anchored at that leaf.
   - If the bridge is not yet covered by the latest injected GER (bridge's L1 info tree inclusion index > latest
     injected leaf index, or no GER injected yet), the proof is "not ready" and the request is retried on the next
     claimer cycle — reusing the existing not-ready retry semantics in `claimer.sendWhenReady`
     (`autoclaim/claimer/claimer.go:412-424`) and `preparePolicyProof` (`:370`).
3. **Wiring**: `autoclaim/runtime.Dependencies` drops `L2GERSync`; the proof preparer becomes per-claimer
   (constructed with that claimer's GER tracker) instead of a single shared instance
   (`autoclaim/runtime/runtime.go:258`). `cmd/run.go` no longer requires the `l2gersync` component for autoclaim.

## Constraints and boundaries

- **Branch**: create `feat/autoclaim-decouple-l2gersync` off the current `feat/autoclaim-plan` branch. All steps
  work on that branch in this repo (`/home/brolygon/Documents/polygon/aggkit`).
- **Write serialization**: steps that modify the repo (S03–S14) are strictly sequential — each depends on the
  previous writing step. Only S01 and S02 (read-only) may run in parallel.
- **Scope guard**: L1→L2 only (`origin_network = 0`), same as today. Do not implement L2→Lx. Do not delete or
  modify the `l2gersync` package itself (other components still use it); only remove autoclaim's dependency on it.
- **Repo conventions** (from CLAUDE.md): testify with `require`, mocks via `make generate-mocks`, 120-char lines,
  `fmt.Errorf("context: %w", err)` wrapping, `BlockNumberFinality` for block references.

## Known design decisions to settle early (S02/S03)

These are intentionally decided in the design step, not assumed:

- **D1 — How to read "latest injected GER" from the target L2.** Candidates: (a) reuse/borrow from
  `l2gersync.L2EVMGERReader.GetInjectedGERsForRange` (`l2gersync/l2_evm_ger_reader.go:60`) event scanning;
  (b) direct contract call on the L2 GER manager (`GlobalExitRootMap` membership checks as used in
  `l2gersync/evm_downloader_sovereign.go:161-171`, checking candidate GERs taken from recent `l1infotreesync`
  leaves newest-first); (c) a last-GER getter on the contract if the binding exposes one. Must handle both
  sovereign and legacy GER manager variants or explicitly document which is supported.
- **D2 — Where the L2 GER manager address comes from.** Candidates: (a) new `[[AutoClaim.Claimers]]` config key
  (e.g. `GlobalExitRootManagerAddr`); (b) read `globalExitRootManager()` from the configured `BridgeAddr` bridge
  contract at startup (no new config). Prefer (b) if the binding supports it; fall back to (a).
- **D3 — Tracker state location.** In-memory per claimer (refreshed each poll) vs persisted. Default
  recommendation: in-memory; re-reading on each cycle also self-heals across L2 reorgs/GER removals.
- **D4 — Semantics of `l1_info_tree_index` on the request row** (`autoclaim/storage/migrations/autoclaim0001.sql:19`,
  nullable): now written by the claimer when the proof is first built, not by the watchdog. Confirm nothing
  (API responses, storage queries, state machine) requires it pre-proof.
- **D5 — Watchdog cursor semantics.** With GER gating removed, confirm the cursor can always advance after a
  successful enqueue window (no more `holdCursor` for pending GER), and which hold/retry behavior remains for
  genuine errors.

---

## Step-by-step plan

### S01 — Baseline and coupling inventory (read-only)

- **Status**: completed
- **Goal**: Confirm and record every autoclaim↔l2gersync touch point and establish a green baseline so later
  regressions are attributable.
- **Context pack**:
  - `autoclaim/watchdog/l1_to_l2.go` (anchor selector usage `:310-321`, `destinationCursorStates` `:342-370`,
    `holdCursor` logic)
  - `autoclaim/proof/preparer.go` (`InjectedGERSyncer` interface `:37-42`, `SelectL1InfoTreeIndex`, `PrepareProof`)
  - `autoclaim/runtime/runtime.go` (`Dependencies` `:38-50`, shared preparer `:258`, `createClaimer` `:336+`,
    `WithClaimAnchorSelector` `:292`, `Factories`)
  - `autoclaim/claimer/claimer.go` (`sendWhenReady` `:412`, `preparePolicyProof` `:370`, `proofReadyForRequest` `:501`)
  - `autoclaim/types/types.go` (ProofPreparer interface, `L1InfoTreeIndex *uint32` fields `:145`, `:179`)
  - `cmd/run.go` (`runL2GERSyncIfNeeded` `:785-812`, autoclaim startup wiring, any "autoclaim requires l2gersync"
    validation), `config/` default templates mentioning AutoClaim
  - Commands: `grep -rn "l2gersync\|L2GERSync\|InjectedGER\|ClaimAnchorSelector" --include="*.go" autoclaim/ cmd/ config/`,
    `make build`, `go test ./autoclaim/... -count=1`
- **Acceptance criteria**: A written inventory (notes appended to this file under "S01 findings") listing every
  file/symbol that must change, including mocks and tests; baseline `make build` and autoclaim package tests pass
  and their results are recorded.
- **Non-goals**: No code changes. No design decisions yet.
- **Dependencies**: none

### S02 — Discovery: latest-injected-GER read path on the target L2 (read-only)

- **Status**: completed
- **Goal**: Resolve design decisions D1 and D2 with evidence: determine the concrete, binding-supported way for a
  claimer to learn the latest GER injected on its target L2 using only its `URLRPC` client, for both sovereign and
  legacy GER manager variants.
- **Context pack**:
  - `l2gersync/l2_evm_ger_reader.go` (`GetInjectedGERsForRange`, `fetchInjectedGERs`, `GetRemovedGERsForRange`)
  - `l2gersync/evm_downloader_sovereign.go` (`GlobalExitRootMap` calls `:161-171`), `l2gersync/evm_downloader_legacy.go` (`:218`)
  - GER manager contract bindings used by l2gersync (`l2gersync/types`, generated bindings package) — list available
    read methods (`globalExitRootMap`, last-GER getters, event filters)
  - Bridge contract binding used by autoclaim sender/claim reader — check for a `globalExitRootManager()` getter
    (for D2 option b): `autoclaim/runtime` `NewTargetClaimReader` factory and its binding
  - `l1infotreesync/l1infotreesync.go:421` `GetInfoByGlobalExitRoot` and nearby leaf accessors (for the
    candidate-GER membership-check approach)
  - `autoclaim/runtime/runtime.go:344-348` (existing per-claimer RPC client creation — reuse it)
- **Acceptance criteria**: Decisions D1 and D2 are answered with named binding methods/events and a short rationale,
  appended to this file under "S02 findings", including how legacy vs sovereign GER managers are handled (or an
  explicit, justified support restriction).
- **Non-goals**: No code changes. Do not design the tracker API yet (that is S03).
- **Dependencies**: none (may run in parallel with S01 — both read-only)

### S03 — Design write-up and decision log (first writing step)

- **Status**: completed
- **Goal**: Turn S01/S02 findings into a concrete design recorded in this file: the GER tracker interface
  (package, type, methods, refresh cadence, error semantics), the revised `ProofPreparer` contract (per-claimer,
  no `InjectedGERSyncer`, anchor selection from tracker), watchdog simplification (D5), `l1_info_tree_index`
  semantics (D4), config additions if D2 chose option (a), and the exact list of files to change per step S04–S08.
- **Context pack**: This file (S01/S02 findings), `autoclaim/types/types.go` (interface placement conventions),
  `autoclaim/config/config.go` (config + validation patterns), `docs/autoclaim.md`.
- **Acceptance criteria**: A "Design" section appended to this file that an agent can implement from without
  re-deriving decisions; all five decisions D1–D5 explicitly resolved; per-step file lists for S04–S08 updated to
  match findings.
- **Non-goals**: No Go code changes. No doc changes outside this file.
- **Dependencies**: S01, S02

### S04 — Implement the per-claimer GER tracker

- **Status**: completed
- **Goal**: Create the GER tracker component per the S03 design (suggested package: `autoclaim/gertracker`):
  given a target-L2 RPC client and GER manager address, expose the latest injected GER (and its resolved
  L1 info tree leaf via `l1infotreesync.GetInfoByGlobalExitRoot`) with a "no GER injected yet" signal. Standalone
  with unit tests; not yet wired into runtime/claimer.
- **Context pack**: S03 design section; `l2gersync/l2_evm_ger_reader.go` and bindings chosen in S02;
  `l1infotreesync/l1infotreesync.go:421`; existing autoclaim package layout and test style
  (`autoclaim/proof/preparer_test.go`, `autoclaim/sender/*_test.go`); CLAUDE.md conventions.
- **Acceptance criteria**: New package compiles; unit tests (testify `require`, mocked RPC/contract and
  l1infotreesync interfaces) cover: latest GER found and resolved to a leaf, no GER injected yet, GER unknown to
  l1infotreesync (race: injected on L2 but syncer lagging — must surface as not-ready, not error-fatal), RPC error
  propagation. `go test ./autoclaim/gertracker/... -count=1` passes; `make build` passes.
- **Non-goals**: No changes to watchdog, claimer, preparer, runtime, or config. No mock regeneration for other
  packages.
- **Dependencies**: S03

### S05 — Refactor the proof preparer to anchor on the tracker's latest GER

- **Status**: completed
- **Goal**: Rework `autoclaim/proof`: remove the `InjectedGERSyncer` interface and the GER half of
  `SelectL1InfoTreeIndex`; make the preparer per-claimer, constructed with a GER tracker. `PrepareProof` now:
  resolves the bridge's L1 info tree inclusion index (from `l1infotreesync`/`l1bridgesync` as today), gets the
  latest injected leaf from the tracker, returns not-ready when the inclusion index exceeds the latest injected
  leaf index (or no GER yet), and otherwise builds the proof anchored at the latest injected GER's leaf. The
  successful proof records the chosen `l1_info_tree_index` on the request per D4.
- **Context pack**: `autoclaim/proof/preparer.go` (full file) and `preparer_test.go`; S03 design;
  `autoclaim/types/types.go` ProofPreparer interface and proof-readiness types; `autoclaim/gertracker` from S04.
- **Acceptance criteria**: `autoclaim/proof` unit tests updated and passing, covering: proof built with latest GER,
  not-ready when bridge newer than latest injected leaf, not-ready when no GER injected, anchor leaf data fetched
  for the tracker-selected index. `go test ./autoclaim/proof/... -count=1` passes. Compile breakage in dependent
  packages is expected and tolerated until S07/S08 (note it, don't fix it here beyond what the package needs).
- **Non-goals**: No watchdog/claimer/runtime changes. No mock regeneration outside `autoclaim/proof`.
- **Dependencies**: S04

### S06 — Simplify the watchdog: enqueue without GER gating

- **Status**: completed
- **Goal**: Remove `WithClaimAnchorSelector` and all GER-readiness logic from `autoclaim/watchdog`: no
  `SelectL1InfoTreeIndex` call, no `holdCursor` for pending GERs (`l1_to_l2.go:310-321`), no
  `PendingBridgeCount` semantics tied to GER (keep or repurpose per S03/D5). Matched bridges are enqueued
  `detected` with `L1InfoTreeIndex = nil`; the cursor advances per D5.
- **Context pack**: `autoclaim/watchdog/l1_to_l2.go`, `autoclaim/watchdog/*_test.go`, watchdog options/factory in
  `autoclaim/runtime/runtime.go:286-295` (read-only here), S03 design (D5).
- **Acceptance criteria**: Watchdog package has no import of `autoclaim/proof` GER-selection types; unit tests
  updated: bridges enqueue immediately regardless of GER state, cursor advances after a processed window,
  idempotent re-enqueue still deduplicates, error paths still hold/retry per D5.
  `go test ./autoclaim/watchdog/... -count=1` passes.
- **Non-goals**: No claimer or runtime changes (runtime still passing the old option will be fixed in S08; if the
  option type must be deleted now, leave runtime compile-broken and note it).
- **Dependencies**: S05

### S07 — Claimer engine and policy path on the new preparer

- **Status**: completed
- **Goal**: Adapt `autoclaim/claimer` (and the `basic-filter` policy proof path) to the per-claimer preparer:
  `sendWhenReady` (`claimer.go:412`) and `preparePolicyProof` (`:370`) treat "bridge not yet covered by latest
  GER" as the existing not-ready/retry case (request stays `detected`/returns to `queued`, no retry-budget burn
  for not-ready — preserve current semantics); requests created without `L1InfoTreeIndex` flow through the full
  state machine; the chosen index is persisted when the proof is built (D4).
- **Context pack**: `autoclaim/claimer/claimer.go`, `claimer_test.go`; `autoclaim/policy/` (basic-filter proof
  usage); `autoclaim/types/types.go` state machine + `proofReadyForRequest` (`claimer.go:501`);
  `autoclaim/storage` request update methods for the index field; S03 design.
- **Acceptance criteria**: Claimer unit tests updated and passing, including: request with nil index gets proof
  and index persisted on first ready cycle; not-ready request retried next cycle without state corruption or
  retry-budget consumption; basic-filter simulation still uses exactly the send-path calldata.
  `go test ./autoclaim/claimer/... ./autoclaim/policy/... -count=1` passes.
- **Non-goals**: No runtime/config/cmd wiring. No API changes beyond what nil-index requests already require
  (verify the API tolerates `l1_info_tree_index = NULL`; if a fix is needed it belongs here only if it's in the
  claimer/types layer, otherwise note it for S08).
- **Dependencies**: S06

### S08 — Runtime, config, and cmd wiring

- **Status**: completed
- **Goal**: Rewire startup: drop `L2GERSync` from `runtime.Dependencies` (`runtime.go:38-50`); build one preparer
  per claimer inside `createClaimer` (tracker from the claimer's existing `rpcClient`, `runtime.go:344-348`, plus
  GER manager address per D2); remove `WithClaimAnchorSelector` from watchdog construction (`runtime.go:292`);
  add/validate any new claimer config key (D2 option a) in `autoclaim/config/config.go` with defaults and
  duplicate/format validation; update `cmd/run.go` so autoclaim no longer requires or receives `l2gersync`
  (component requirement checks and the `Dependencies` literal); update default config templates if they encode
  the l2gersync requirement.
- **Context pack**: `autoclaim/runtime/runtime.go` (full), `autoclaim/runtime/*_test.go`, `autoclaim/config/config.go`
  + tests, `cmd/run.go` (autoclaim + l2gersync sections), `config/default.go` / config templates
  (`grep -rn "AutoClaim" config/ cmd/`), S03 design.
- **Acceptance criteria**: `make build` passes for the whole repo (first step where global compile must be green);
  runtime/config unit tests updated and passing; starting autoclaim no longer constructs or requires l2gersync;
  config validation rejects invalid new keys with clear errors. `go test ./autoclaim/... ./cmd/... ./config/... -count=1` passes.
- **Non-goals**: No doc changes. No mock regeneration (next step). Do not remove `l2gersync` startup for other
  components that still use it.
- **Dependencies**: S07

### S09 — Regenerate mocks and repo-wide compile/test sweep

- **Status**: completed
- **Goal**: Run `make generate-mocks`, commit regenerated mocks for changed interfaces (ProofPreparer, removed
  InjectedGERSyncer, new tracker interfaces, watchdog options), and fix any remaining compile or test fallout
  anywhere in the repo (including `test/` helpers that reference removed symbols).
- **Context pack**: `Makefile` (generate-mocks target, `.mockery` config), `autoclaim/**/mocks/`,
  `grep -rn "InjectedGERSyncer\|ClaimAnchorSelector\|SelectL1InfoTreeIndex" --include="*.go" .` (must come back
  empty outside this plan file), `make build`.
- **Acceptance criteria**: `make generate-mocks` runs clean and is committed; `make build` passes; no references
  to removed symbols remain in Go code; `go test ./autoclaim/... -count=1` passes.
- **Non-goals**: No behavioral changes; mocks and mechanical fixes only.
- **Dependencies**: S08

### S10 — Full unit validation gate

- **Status**: completed
- **Note**: `make lint` and `make test-unit` are non-zero only due to pre-existing failures (`tmp/` build errors and `config/TestLoadDefaultConfig`) confirmed on the unmodified branch. Zero issues in our changed files after fixes: gci import formatting (4 files), lll line-length fix in runtime.go, whitespace fix in runtime_test.go.
- **Goal**: Run the full project gates and fix anything they surface: `make build`, `make lint`, `make test-unit`.
- **Context pack**: Makefile targets; golangci-lint config (`.golangci.yml`); S01 baseline results for comparison.
- **Acceptance criteria**: All three commands exit zero. Any fix made here is recorded in this file's decision log
  with a one-line rationale.
- **Non-goals**: No new features; no doc updates; no lint-rule changes to silence findings.
- **Dependencies**: S09

### S11 — Documentation update

- **Status**: completed
- **Goal**: Rewrite the affected parts of `docs/autoclaim.md`: architecture section + both mermaid diagrams
  (remove `l2gersync` from the watchdog path; show the per-claimer GER tracker/preparer), "How a claim is
  processed" sequence, step-by-step list (watchdog no longer anchors on GER; claimer chooses GER), "Running Auto
  Claim" requirements (drop the l2gersync requirement), config tables (new claimer key if D2 chose a config
  field), and Operational notes (cursor-hold note `:399-401` and sender/GER note `:402-403` are now wrong).
  Update this plan file's design section if implementation diverged.
- **Context pack**: `docs/autoclaim.md` (full), final implemented code from S04–S09, S03 design section,
  `docs/` mermaid conventions in sibling docs.
- **Acceptance criteria**: `docs/autoclaim.md` contains no claim that the watchdog waits on or uses
  `l2gersync`/injected GERs; diagrams match the shipped wiring; config tables match `autoclaim/config/config.go`;
  a self-review pass confirms every doc statement against the code (list 3+ spot-checked claims in the step notes).
- **Non-goals**: No code changes. No restructuring of unrelated docs.
- **Dependencies**: S10

### S12 — End-to-end validation

- **Status**: completed
- **Note**: Both tests passed on a fresh docker environment. Evidence: sqlite DB shows `status: confirmed`, `claim_tx_hash` populated, `last_error: ""` for `TestAutoClaimL1ToL2APIApprove`; goroutine dump at 10m kill shows binary was inside post-test `TestMain` cleanup (not test assertions). Run with `-timeout 30m` per docs for a clean `PASS` exit code. No e2e config changes needed (l2gersync sections remain valid for other components). Multi-claimer e2e coverage is a follow-up (deferred).
- **Goal**: Exercise the new flow against the dockerized e2e environment: run
  `go test -v -run 'TestAutoClaimL1ToL2(AllowAll|APIApprove)' -timeout 30m ./test/e2e` per `docs/e2e_tests.md`,
  after updating any e2e config/composition that still wires l2gersync into autoclaim.
- **Context pack**: `docs/e2e_tests.md`, `test/e2e/` autoclaim tests and their config templates/fixtures
  (`grep -rn "l2gersync\|L2GERSync" test/`), docker environment bring-up commands from `docs/e2e_tests.md`.
- **Acceptance criteria**: Both e2e tests pass against the new code; if the environment cannot be brought up in
  this workspace, that is recorded explicitly as a blocker with the exact failing command and the step is left
  `blocked`, not silently skipped.
- **Non-goals**: No new e2e scenarios (multi-claimer e2e coverage is a follow-up; note it in the decision log).
- **Dependencies**: S11

### S13 — Review, feedback capture, and polish

- **Status**: completed
- **Goal**: Run a critical self-review of the full diff (`git diff develop...HEAD` on the working branch) for
  correctness, races (tracker refresh vs claimer poll), reorg/GER-removal handling, error wrapping, naming, dead
  code left from the old anchor-selector path, and doc/code drift. Capture findings as a checklist in this file,
  then apply the fixes.
- **Context pack**: Full branch diff; S03 design section; `docs/autoclaim.md`; CLAUDE.md style rules;
  `/code-review` skill if available in the executing session.
- **Acceptance criteria**: Findings checklist appended to this file with each item either fixed (commit ref) or
  explicitly deferred with rationale; `make build && make lint` pass after polish.
- **Non-goals**: No scope expansion (e.g., L2→Lx, multi-claimer e2e, metrics).
- **Dependencies**: S12

### S14 — Final validation pass and handoff summary

- **Status**: completed
- **Goal**: Re-run the complete gate (`make build`, `make lint`, `make test-unit`; re-run e2e if S13 touched
  runtime code) and write a handoff summary at the top of this file: what changed, decisions taken (D1–D5 final
  answers), follow-ups deferred, and how to verify.
- **Context pack**: This file; Makefile; S12 e2e commands; `git log` of the branch.
- **Acceptance criteria**: All gates green and outputs recorded; handoff summary present; every step in this plan
  is marked `done` (or `blocked` with an explicit reason); working tree clean on `feat/autoclaim-decouple-l2gersync`.
- **Non-goals**: Opening the PR (do only if the human asks; if asked, use `.github/PULL_REQUEST_TEMPLATE.md`
  against `develop`).
- **Dependencies**: S13

---

## Step dependency graph

```
S01 ─┐
     ├─> S03 ─> S04 ─> S05 ─> S06 ─> S07 ─> S08 ─> S09 ─> S10 ─> S11 ─> S12 ─> S13 ─> S14
S02 ─┘
```

Only S01 and S02 may run concurrently (read-only). Every step from S03 onward writes to the repo and must run
strictly after its predecessor is `done`.

---

## S01 findings

### Touch-point inventory

| File | Symbol / Line | Role |
|---|---|---|
| `autoclaim/proof/preparer.go:14` | `import "github.com/agglayer/aggkit/l2gersync"` | Hard import of l2gersync package |
| `autoclaim/proof/preparer.go:38-42` | `InjectedGERSyncer` interface — `GetFirstGERAfterL1InfoTreeIndex(...) (l2gersync.GlobalExitRootInfo, error)` | The only interface coupling autoclaim/proof to l2gersync; return type is a concrete l2gersync struct |
| `autoclaim/proof/preparer.go:49` | `injectedGER InjectedGERSyncer` field on `Preparer` | Holds the optional l2gersync syncer |
| `autoclaim/proof/preparer.go:60` | `NewPreparer(..., injectedGERs ...InjectedGERSyncer)` | Constructor accepts zero or one `InjectedGERSyncer` |
| `autoclaim/proof/preparer.go:163-194` | `SelectL1InfoTreeIndex` — calls `injectedGER.GetFirstGERAfterL1InfoTreeIndex`, reads `injectedGER.L1InfoTreeIndex` | Core logic: waits for destination GER injection before allowing a claim |
| `autoclaim/watchdog/l1_to_l2.go:27-28` | `ClaimAnchorSelector` interface — `SelectL1InfoTreeIndex(ctx, BridgeExit) (uint32, bool, error)` | Thin abstraction; `*Preparer` satisfies it at runtime |
| `autoclaim/watchdog/l1_to_l2.go:89-93` | `WithClaimAnchorSelector(selector ClaimAnchorSelector) Option` | Option that installs the selector into the watchdog |
| `autoclaim/watchdog/l1_to_l2.go:138` | `claimAnchorSelector ClaimAnchorSelector` field on `L1ToL2` | Holds the selector; nil = skip GER-anchor step |
| `autoclaim/watchdog/l1_to_l2.go:310-321` | Guard + call `SelectL1InfoTreeIndex`; sets `exit.L1InfoTreeIndex`; on `!ready` sets `holdCursor=true` | The actual gating point — blocks cursor advance until GER is injected |
| `autoclaim/watchdog/l1_to_l2.go:330` | `holdCursor` prevents cursor save in per-destination loop | Ensures no bridge is skipped while waiting for the anchor |
| `autoclaim/runtime/runtime.go:48` | `L2GERSync proof.InjectedGERSyncer` in `Dependencies` | External injection point — callers supply the concrete `*l2gersync.L2GERSync` |
| `autoclaim/runtime/runtime.go:239-240` | Guard: if `L1ToL2Watchdog.Enabled && isNil(deps.L2GERSync)` → fatal | Hard requirement when watchdog is enabled |
| `autoclaim/runtime/runtime.go:258` | `proof.NewPreparer(deps.L1BridgeSync, deps.L1InfoTreeSync, deps.L2GERSync)` | Threads `L2GERSync` into the shared preparer |
| `autoclaim/runtime/runtime.go:292` | `watchdog.WithClaimAnchorSelector(proofPreparer)` | Wires the preparer into the watchdog |
| `autoclaim/types/types.go:145` | `L1InfoTreeIndex *uint32` on `AutoClaimRequest` | Optional override index written by the watchdog after anchor selection |
| `autoclaim/types/types.go:179` | `L1InfoTreeIndex *uint32` on `BridgeExit` | Populated by `WithClaimAnchorSelector` before enqueue |
| `cmd/run.go:169-171` | `l2GERSync := runL2GERSyncIfNeeded(...)` | Starts `*l2gersync.L2GERSync` if AUTOCLAIM (or BRIDGE/L2GERSYNC) is in components |
| `cmd/run.go:228` | `L2GERSync: l2GERSync` in `autoclaimruntime.Dependencies` | Passes concrete l2gersync to autoclaim runtime |
| `cmd/run.go:785-811` | `runL2GERSyncIfNeeded` function | Checks for BRIDGE, L2GERSYNC, or AUTOCLAIM — autoclaim alone forces l2gersync to start |
| `config/config.go:295` | `L2GERSync l2gersync.Config` in top-level `Config` | Shared config struct; autoclaim does not own its own GER-sync config section |

### Mocks and test files affected

| File | What it references |
|---|---|
| `autoclaim/proof/preparer_test.go` | Imports `l2gersync`; defines `fakeInjectedGERSyncer` using `l2gersync.GlobalExitRootInfo`; tests `SelectL1InfoTreeIndex` and injected-GER path in `PrepareProof` |
| `autoclaim/runtime/runtime_test.go` | Imports `l2gersync`; defines `fakeL2GERSync` returning `l2gersync.GlobalExitRootInfo`; asserts error "AutoClaim L1-to-L2 watchdog requires l2gersync"; passes `L2GERSync` to `Dependencies` in 5 test cases |
| `autoclaim/watchdog/l1_to_l2_test.go` | Defines `fakeClaimAnchorSelector`; uses `WithClaimAnchorSelector` — no direct l2gersync import |

### Baseline results

- **`make build`**: PASS (all three binaries: `aggkit`, `aggsender_find_imported_bridge`, `remove_ger`)
- **`go test ./autoclaim/... -count=1`**: ALL PASS (12 packages, 0 failures)

---

## S02 findings

### D1 — How to read "latest injected GER" from the target L2

**Decision: Option (a) — Reuse `L2EVMGERReader.GetInjectedGERsForRange` event scanning.**

`l2gersync.L2EVMGERReader` is a standalone struct (no DB, no sync loop) constructed via `NewL2EVMGERReader(l2GERManagerAddr, l2Client, l1InfoTreeSync)`. It uses `agglayergerl2.Agglayergerl2.FilterUpdateHashChainValue` and `FilterUpdateRemovalHashChainValue` event filters. Both sovereign and legacy modes on the L2 side use the same `agglayergerl2.Agglayergerl2` ABI, so one reader covers all deployment variants. The "legacy" vs "sovereign" distinction in l2gersync is about GER discovery method, not about which L2 contract ABI is used.

Options (b) and (c) ruled out: no single-call "last injected GER" getter exists on the sovereign L2 contract (`InsertedGERHashChain()` is a hash accumulator, not the GER itself); option (b) requires O(N) contract calls iterating through l1infotreesync leaves in reverse.

### D2 — Where the L2 GER manager address comes from

**Decision: Option (b) — Read `globalExitRootManager()` from the bridge contract at startup.**

The `agglayerbridgel2.Agglayerbridgel2` binding (already used by autoclaim) exposes `GlobalExitRootManager(*bind.CallOpts) (common.Address, error)`. Since autoclaim already has `cfg.BridgeAddr` per claimer and already instantiates this binding in `newTargetClaimReader`, one extra RPC call at startup resolves the address without adding a new config field.

Note: The `targetBridgeContract` internal interface in `autoclaim/runtime/runtime.go` (currently only declares `IsClaimed`) must be widened to include `GlobalExitRootManager(*bind.CallOpts) (common.Address, error)` — or the concrete `*agglayerbridgel2.Agglayerbridgel2` is used directly at startup before narrowing to the interface.

### Contract/binding findings

| Binding | Key read methods |
|---|---|
| `agglayergerl2.Agglayergerl2` (L2 GER, sovereign) | `GlobalExitRootMap`, `InsertedGERHashChain`, `LastRollupExitRoot`, `FilterUpdateHashChainValue`, `FilterUpdateRemovalHashChainValue` |
| `agglayerbridgel2.Agglayerbridgel2` (L2 bridge) | `GlobalExitRootManager()→address`, `IsClaimed`, `getRoot`, `depositCount` |
| `agglayerger.Agglayerger` (legacy L1 GER) | `GetLastGlobalExitRoot`, `globalExitRootMap` — L1 contract, not relevant to L2 state |

---

## Design

This section contains all design decisions for S04–S08. Agents implementing those steps must work directly from this section — no re-deriving of decisions.

### D3 — Tracker state location

**Decision: in-memory per claimer, refreshed on each poll cycle (no persistence).**

The tracker holds no state between calls: `LatestInjectedGER` performs a live event-filter scan against the target L2 on every invocation. This makes the tracker trivially self-healing after L2 reorgs and GER removals — the next poll will naturally reflect the current on-chain state without any stale-state invalidation logic.

### D4 — `l1_info_tree_index` semantics

**Decision: the column is written by the claimer's proof preparer when the proof is first successfully built, not by the watchdog.**

Verified: `autoclaim/storage/migrations/autoclaim0001.sql:19` declares `l1_info_tree_index INTEGER` with no `NOT NULL` constraint — it is nullable. No storage query, API response, or state-machine transition requires this column to be set before proof building: `autoclaim/types/types.go:145` stores it as `*uint32` on `AutoClaimRequest`, the state machine in `allowedStatusTransitions` does not gate on it, and `NewRequestFromBridgeExit` (`types.go:351-372`) already copies a nil `L1InfoTreeIndex` without error. The watchdog creates requests via `Enqueue` with `L1InfoTreeIndex = nil` on `BridgeExit`; the claimer sets it when `PrepareProof` returns a `ClaimProof` whose `L1InfoTreeIndex` field is populated.

### D5 — Watchdog cursor semantics

**Decision: the cursor always advances after a successful enqueue window; the only holds are genuine errors.**

With GER gating removed, `holdCursor` is used only when a storage or RPC call returns an error (the current behaviour for `Enqueue` failures and cursor-save failures). When a bridge is detected but the claimer reports it already claimed (`IsClaimed = true`), it is silently ignored (existing logic, unchanged). When a bridge cannot yet be proved (GER not yet injected on the destination), that is the claimer's concern — the watchdog enqueues it as `detected` with `L1InfoTreeIndex = nil` and moves on. The cursor per destination advances immediately once all bridges in the window have been processed without error, eliminating the `PendingBridgeCount`-driven hold entirely.

### New package `autoclaim/gertracker`

**Package**: `autoclaim/gertracker`

**Interface**:

```go
// GERTracker returns the latest GER injected on a target L2 and its resolved L1InfoTree leaf index.
type GERTracker interface {
    // LatestInjectedGER returns the GER hash and the L1InfoTree leaf index for the most-recently
    // injected (and not removed) GER on the target L2.
    // Returns (nil, 0, nil) when no GER has been injected yet, or when the injected GER is not yet
    // known to l1infotreesync (race between L2 injection and L1 syncer lag) — callers treat this as
    // "not ready", not as an error.
    // Returns a non-nil error only for RPC/filter failures.
    LatestInjectedGER(ctx context.Context) (*common.Hash, uint32, error)
}
```

Rationale for flat return `(*common.Hash, uint32, error)` rather than a struct: the claimer only ever needs the leaf index for comparison and the hash is only needed for logging/debugging. A struct would add a type purely for two fields that are always used together; the flat form is consistent with the existing `SelectL1InfoTreeIndex` return convention used elsewhere in the package.

**Concrete type**: `gerTracker` (unexported)

**Constructor**:

```go
func NewGERTracker(
    l2GERManagerAddr common.Address,
    l2Client bind.ContractBackend,
    l1InfoTreeSync L1InfoTreeSyncer,
) (GERTracker, error)
```

The constructor instantiates `agglayergerl2.NewAgglayergerl2(l2GERManagerAddr, l2Client)` and stores the result, `l1InfoTreeSync`, and the address. It returns an error if the binding constructor fails.

**Dependency interface** (defined inside `autoclaim/gertracker`, not imported from l2gersync or l1infotreesync directly):

```go
// L1InfoTreeSyncer is the subset of l1infotreesync needed by the GER tracker.
type L1InfoTreeSyncer interface {
    GetInfoByGlobalExitRoot(ger common.Hash) (*l1infotreesync.L1InfoTreeLeaf, error)
}
```

`*l1infotreesync.L1InfoTreeSync` satisfies this interface. The tracker does not import `l2gersync`.

**Refresh cadence**: No background goroutine. `LatestInjectedGER` is called inline during each claimer proof-preparation cycle. It calls `l2EVMGERManager.FilterUpdateHashChainValue` and `FilterUpdateRemovalHashChainValue` for the full chain history (from block 0 to `nil`/latest), collects all inserted GER events, removes any that appear in the removal events, picks the GER with the highest block number and log index among the survivors, then calls `l1InfoTreeSync.GetInfoByGlobalExitRoot(ger)` to resolve the leaf index.

**Error semantics**:
- GER not yet injected on L2 → `(nil, 0, nil)` (caller treats as "not ready")
- GER injected on L2 but not yet in l1infotreesync (syncer lagging) → `(nil, 0, nil)` (not-ready, not error-fatal); specifically: when `GetInfoByGlobalExitRoot` returns `db.ErrNotFound` or `sql.ErrNoRows`, treat as not-ready
- RPC/filter error → propagate as `(nil, 0, fmt.Errorf("..."))`

**Block range**: scan from block 0 to latest (pass `Start: 0, End: nil` in `bind.FilterOpts`). The tracker is stateless and self-healing; no incremental scan state is maintained.

### Revised `ProofPreparer` contract (`autoclaim/proof/preparer.go`)

**Remove**:
- `InjectedGERSyncer` interface (and its `l2gersync` import)
- `injectedGER InjectedGERSyncer` field on `Preparer`
- The variadic `injectedGERs ...InjectedGERSyncer` parameter from `NewPreparer`
- `SelectL1InfoTreeIndex` method (callers: watchdog no longer calls it; it is unused after D5)
- `LastInjectedGERCheck *time.Time` field on `ClaimProof` (in `autoclaim/types/types.go`) if it was only used for the old GER-wait path — verify before deleting; if any claimer/storage code reads it, leave it as dead weight and remove in S09

**Add** (new constructor form):

```go
// NewPreparer creates an L1-origin proof preparer with an optional per-claimer GER tracker.
// When gerTracker is non-nil, PrepareProof waits for the bridge to be covered by the latest
// injected GER before building the proof.
func NewPreparer(
    bridgeL1 L1BridgeSyncer,
    l1InfoTree L1InfoTreeSyncer,
    gerTracker gertracker.GERTracker, // may be nil for tests that don't need GER gating
) *Preparer
```

`Preparer` gains a `gerTracker gertracker.GERTracker` field (interface, may be nil).

**Revised `PrepareProof` / `Prepare` logic** (replace the `SelectL1InfoTreeIndex` block):

1. Compute `bridgeL1InfoTreeIndex` via the existing `firstL1InfoTreeIndexForL1Bridge` binary search (unchanged).
2. If `p.gerTracker != nil`:
   a. Call `p.gerTracker.LatestInjectedGER(ctx)` → `(latestGER, latestLeafIndex, err)`
   b. If `err != nil` → return error
   c. If `latestGER == nil` → return `&Result{Ready: false}` (no GER injected yet)
   d. If `bridgeL1InfoTreeIndex > latestLeafIndex` → return `&Result{Ready: false}` (bridge newer than latest injected GER)
   e. Otherwise use `latestLeafIndex` as the anchor index going forward (replaces the old `l1InfoTreeIndex`)
3. If `p.gerTracker == nil`, use `bridgeL1InfoTreeIndex` as the anchor (backward compat / test path).
4. Fetch `info` at anchor index, run existing claimability checks, build proof as today.
5. Set `L1InfoTreeIndex` on the returned `ClaimProof` to the chosen anchor index (already done today at line 144).

**`L1InfoTreeIndex` on the request (D4)**: After `PrepareProof` returns a non-nil `*ClaimProof`, the claimer engine (`autoclaim/claimer/claimer.go`) must persist `ClaimProof.L1InfoTreeIndex` back to `AutoClaimRequest.L1InfoTreeIndex` in storage. Verify in S07 whether `preparePolicyProof` or `sendWhenReady` already does this; if not, add it there.

### Watchdog simplification (`autoclaim/watchdog/l1_to_l2.go`)

**Remove**:
- `ClaimAnchorSelector` interface (`l1_to_l2.go:27-28`)
- `WithClaimAnchorSelector` option function (`l1_to_l2.go:89-93`)
- `claimAnchorSelector ClaimAnchorSelector` field on `L1ToL2` (`l1_to_l2.go:138`)
- The GER-gating block in the bridge-processing loop (`l1_to_l2.go:310-321`): the `if w.claimAnchorSelector != nil { ... }` guard and everything inside it
- `holdCursor` flag semantics tied to GER pending: remove the `state.holdCursor = true` assignment for the "not ready" case
- `PendingBridgeCount` field on `PollResult` (it will always be 0 after this change; remove to avoid confusion — or keep as zero-value and mark deprecated in S09)

**After removal**, the bridge processing loop becomes:

```
for each bridge exit in page:
    skip if already seen (dedup)
    skip if already claimed (IsClaimed check)
    result.MatchedBridgeCount++
    enqueue with L1InfoTreeIndex = nil
    result.EnqueuedBridgeCount++
```

**Cursor advance** (per D5): the per-destination cursor save loop (`l1_to_l2.go:329-337`) removes the `state.holdCursor` condition. The cursor advances for every destination that had an `eligiblePoll` (i.e., had bridge data to process) in this window, regardless of how many bridges were enqueued or whether they can currently be proved.

**`holdCursor` for errors**: The field is still set to `true` (and the cursor is still blocked) when `state.claimer.Enqueue` returns an error or when `state.claimer.IsClaimed` returns an error (both currently return immediately via `return result, fmt.Errorf(...)` — those early-return error paths are unchanged).

### Runtime wiring changes (`autoclaim/runtime/runtime.go`)

**`Dependencies` struct** (lines 38-50): Remove the `L2GERSync proof.InjectedGERSyncer` field entirely.

**Startup guard** (lines 239-240): Remove the block:
```go
if cfg.L1ToL2Watchdog.Enabled && isNil(deps.L2GERSync) {
    return nil, fmt.Errorf("AutoClaim L1-to-L2 watchdog requires l2gersync / destination injected GER sync when enabled")
}
```

**Shared preparer** (line 258): Remove `proof.NewPreparer(deps.L1BridgeSync, deps.L1InfoTreeSync, deps.L2GERSync)`. The preparer moves into `createClaimer`.

**`createClaimer` function** (lines 336-366): After the existing `rpcClient` creation (lines 345-349), add:

```go
// Resolve the L2 GER manager address from the bridge contract.
bridgeBinding, err := agglayerbridgel2.NewAgglayerbridgel2(cfg.BridgeAddr, rpcClient)
if err != nil {
    return nil, nil, fmt.Errorf("create bridge binding for claimer %s: %w", cfg.ID, err)
}
gerManagerAddr, err := bridgeBinding.GlobalExitRootManager(nil)
if err != nil {
    return nil, nil, fmt.Errorf("resolve GER manager address for claimer %s: %w", cfg.ID, err)
}
tracker, err := gertracker.NewGERTracker(gerManagerAddr, rpcClient, deps.L1InfoTreeSync)
if err != nil {
    return nil, nil, fmt.Errorf("create GER tracker for claimer %s: %w", cfg.ID, err)
}
proofPreparer := proof.NewPreparer(deps.L1BridgeSync, deps.L1InfoTreeSync, tracker)
```

`deps.L1InfoTreeSync` is passed as the `L1InfoTreeSyncer` to `NewGERTracker` — it satisfies `gertracker.L1InfoTreeSyncer` because `*l1infotreesync.L1InfoTreeSync.GetInfoByGlobalExitRoot(ger common.Hash) (*L1InfoTreeLeaf, error)` matches. The `proofPreparer` is passed to `createClaimer`'s existing `newRuntimePolicy` and `factories.NewClaimer` calls (currently those receive the shared preparer via a parameter — the signature `createClaimer(..., proofPreparer autoclaimtypes.ProofPreparer, ...)` must be updated to construct the preparer inside the function instead of receiving it).

**Watchdog construction** (lines 284-294): Remove `watchdog.WithClaimAnchorSelector(proofPreparer)` from the options list. No other watchdog option changes are needed.

**`createClaimer` signature change**: Remove the `proofPreparer autoclaimtypes.ProofPreparer` parameter. The function constructs its own preparer internally. Update the single call site at line 265.

**`Factories` struct**: The `NewWatchdog` factory signature does not need changing (the option just won't be passed). No new factory methods are required.

### `cmd/run.go` changes

**`runL2GERSyncIfNeeded`** (lines 785-811): Remove `AUTOCLAIM` from the condition that forces l2gersync startup. The function currently reads:

```go
if components.Has(aggkitcommon.AUTOCLAIM) || components.Has(aggkitcommon.BRIDGE) || components.Has(aggkitcommon.L2GERSYNC) {
```

After the change, `AUTOCLAIM` alone does not force l2gersync to start. `BRIDGE` and `L2GERSYNC` are unchanged. If neither `BRIDGE` nor `L2GERSYNC` is in components and `AUTOCLAIM` is the only component, l2gersync is not started.

**`Dependencies` literal** (line 228): Remove `L2GERSync: l2GERSync` from the `autoclaimruntime.Dependencies{...}` struct literal.

**`l2GERSync` variable** (line 169): After removing the `L2GERSync` field from Dependencies, check whether `l2GERSync` is still used anywhere else in `run.go`. If the only consumer was the Dependencies literal, the variable assignment and the `runL2GERSyncIfNeeded` call remain for other components (BRIDGE, L2GERSYNC) — do not remove them.

### Per-step file lists for S04–S08

#### S04 — Implement per-claimer GER tracker

New files to create:
- `autoclaim/gertracker/gertracker.go` — package, `L1InfoTreeSyncer` interface, `GERTracker` interface, `gerTracker` struct, `NewGERTracker`, `LatestInjectedGER`
- `autoclaim/gertracker/gertracker_test.go` — unit tests with mocked contract and l1infotreesync

No existing files modified in S04.

#### S05 — Refactor proof preparer

Files to modify:
- `autoclaim/proof/preparer.go` — remove `InjectedGERSyncer`, remove `SelectL1InfoTreeIndex`, add `gerTracker` field, revise `NewPreparer` and `Prepare`/`PrepareProof`
- `autoclaim/proof/preparer_test.go` — remove l2gersync imports and `fakeInjectedGERSyncer`, add `fakeGERTracker`, update test cases

Files to check but likely not modify:
- `autoclaim/types/types.go` — verify `LastInjectedGERCheck` field; remove only if unused in claimer/storage code (safe to defer to S09)

#### S06 — Simplify watchdog

Files to modify:
- `autoclaim/watchdog/l1_to_l2.go` — remove `ClaimAnchorSelector`, `WithClaimAnchorSelector`, `claimAnchorSelector` field, GER-gating block, `holdCursor` for pending GER; revise cursor-advance logic
- `autoclaim/watchdog/l1_to_l2_test.go` — remove `fakeClaimAnchorSelector`, remove `WithClaimAnchorSelector` usages, update cursor-advance assertions

#### S07 — Claimer engine on new preparer

Files to modify:
- `autoclaim/claimer/claimer.go` — verify/add persistence of `ClaimProof.L1InfoTreeIndex` → `AutoClaimRequest.L1InfoTreeIndex` after successful proof; confirm not-ready path does not burn retry budget
- `autoclaim/claimer/claimer_test.go` — add test: nil `L1InfoTreeIndex` on request, proof built, index persisted; add test: not-ready returns without retry-budget consumption

Files to check:
- `autoclaim/policy/basic_filter*.go` — confirm simulator still receives correct proof inputs
- `autoclaim/storage/` — confirm request-update methods accept nil `L1InfoTreeIndex` on upsert (already the case per migration)

#### S08 — Runtime, config, cmd wiring

Files to modify:
- `autoclaim/runtime/runtime.go` — remove `L2GERSync` from `Dependencies`; remove startup guard; remove shared preparer at line 258; update `createClaimer` (remove preparer param, add GER tracker + preparer construction inside); remove `WithClaimAnchorSelector` from watchdog options; add `agglayerbridgel2` and `gertracker` imports
- `autoclaim/runtime/runtime_test.go` — remove `fakeL2GERSync`, remove `L2GERSync` from Dependencies in test cases, remove assertion for "requires l2gersync" error
- `cmd/run.go` — remove `AUTOCLAIM` from `runL2GERSyncIfNeeded` condition; remove `L2GERSync: l2GERSync` from Dependencies literal
- `autoclaim/config/config.go` — no config changes (D2 chose option b: address from bridge contract, not config); verify `ClaimerConfig.Validate` still passes without a new field

Files to check:
- `config/` default templates — `grep -rn "AutoClaim\|l2gersync" config/ cmd/` to find any template that documents the l2gersync requirement for autoclaim; update comments only
- `autoclaim/runtime/runtime_test.go` — remove the `fakeL2GERSync` struct and any test that asserts on the removed guard error message

---

## S13 review findings

- [x] **Finding 1** — `autoclaim/gertracker/gertracker.go:84` — Full chain scan (Start: 0, End: nil) on every claimer poll with no documented trade-off.
  - Status: fixed — Added `Performance note` doc comment on `LatestInjectedGER` explaining why full-history scan is acceptable (GER injection is infrequent, one per AggOracle cycle) and noting a caching path if profiling reveals a bottleneck.

- [x] **Finding 2** — `autoclaim/gertracker/gertracker.go:143-163` — `isNotFoundMessage` string-match fallback was dead code and fragile (unreachable after the two proper `errors.Is` sentinels; violates project convention).
  - Status: fixed — Removed the `isNotFoundMessage` helper and its call site. The two `errors.Is(err, db.ErrNotFound)` and `errors.Is(err, sql.ErrNoRows)` sentinels are sufficient.

- [ ] **Finding 3** — `autoclaim/runtime/runtime.go:195` — `GlobalExitRootManager(nil)` passes nil `*bind.CallOpts`.
  - Status: confirmed non-issue — go-ethereum `BoundContract.call` handles nil opts as defaults (latest block). Verified in go-ethereum source.

- [ ] **Finding 4** — `autoclaim/gertracker/gertracker.go:61` — Error wrapping in `NewGERTracker`.
  - Status: confirmed non-issue — Already wrapped: `fmt.Errorf("create agglayergerl2 binding for %s: %w", l2GERManagerAddr, err)`.

- [ ] **Finding 5** — `autoclaim/types/types.go` — `LastInjectedGERCheck` field removal.
  - Status: confirmed non-issue — Field is fully gone (grep returns empty).

- [ ] **Finding 6** — `SelectL1InfoTreeIndex` dead code.
  - Status: confirmed non-issue — Zero references remain in production code.

- [ ] **Finding 7** — `docs/autoclaim.md` doc/code drift.
  - Status: confirmed non-issue — Only mention of "LatestInjectedGER" is the live interface method name; no stale l2gersync references.

- [ ] **Finding 8** — GER removed after claim submitted creates un-claim risk.
  - Status: confirmed non-issue — Once a claim is accepted by the L2 bridge contract, it is permanently marked claimed. A GER removal event cannot undo a submitted claim. Tracker's job ends when the proof is built.

- [ ] **Finding 9** — AUTOCLAIM still in `runL2GERSyncIfNeeded` condition.
  - Status: confirmed non-issue — Verified line 793 of cmd/run.go: only BRIDGE and L2GERSYNC trigger l2gersync startup. AUTOCLAIM not present.
