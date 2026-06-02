# P1 Step Log

**Step:** P1 — Foundation: shared test helpers, conventions, and isolation strategy

**Final outcome:** completed (validator THUMBS_UP on attempt 1)

## Work done

Established the patterns every later migration step will reuse:

- Created `test/e2e/helpers_test.go` (package `e2e`) containing only reusable
  utilities that did not already exist, all built on top of the pre-existing
  helpers in `bridge_utils.go` and `removeger_test.go`:
  - `assertNetworkHealthy` — delegates to `env.CheckEnv(ctx)` plus a
    bridge-service `HealthCheck` probe (no duplication of `CheckEnv`'s body).
  - `mintAndApproveERC20OnL2` — mints the env `MintableERC20` to `opts.From` and
    approves the L2 bridge to spend it.
  - `bridgeERC20L2ToL1AndClaim` — mint+approve env ERC20 on L2 then bridge+claim
    on L1.
  - `bridgeETHL1ToL2AndClaim` — full ETH L1→L2 bridge+claim; thin wrapper over
    `BridgeL1ToL2WithResult`.
  - `bridgeMessageL1ToL2AndClaim` — message (LeafType 1) bridge from L1
    (`BridgeMessage`) + claim on L2 (`ClaimMessage`).
  - `withCleanEmergencyState` — records emergency state, runs `fn`, and best-effort
    deactivates emergency state on return only if `fn` activated it (generalizes
    the inline defer blocks from `removeger_test.go`).
  - `waitForSettledCertificate` — the cert-settlement waiter; polls the aggsender
    SQLite DB (read-only) via `env.GetAggsenderDBPath()` until a Settled
    (`status == 4`) certificate exists, returning the highest settled height.
  - Internal support helpers also added and reusable later: `waitForBridgeByTxHash`,
    `waitForL1InfoTreeIndex`, `waitForInjectedL1InfoLeaf`, `claimProofToContractProofs`,
    `openAggsenderDBReadOnly`, `querySettledCertHeight`, plus the
    `certStatusSettled = 4` constant.
- Updated `test/e2e/README.md` with: conventions for migrated tests, a "Shared
  helpers" subsection, the full bats→Go mapping table (in-scope P2–P10 rows +
  out-of-scope table with reasons), and the execution-time / timeout strategy
  recommendation for P11.
- Reused (did not recreate): `BridgeL1ToL2(WithResult)`, `BridgeL1NoClaim`,
  `BridgeL2ToL1`, `bridgeResult` (bridge_utils.go); `pollWithBackoff`,
  `injectInvalidGER`, the `assertGER*`/`assertClaim*` helpers, key-pool
  checkout/return, `backoffInitial`/`backoffMax` (removeger_test.go); the
  `mattn/go-sqlite3` driver.

## Deviations / decisions not explicit in the step instructions

These are emphasized because they were judgment calls beyond the literal step text:

- **(a) Self-skipping `TestHelpersCompile` stub.** To satisfy both the `unused`
  linter and the acceptance criterion's "helpers compile and are referenced by at
  least a stub or existing test," a single immediately-skipping
  `TestHelpersCompile(t *testing.T)` was added inside `helpers_test.go`. Its first
  statement is `t.Skip(...)`; every new helper is referenced in the unreachable
  code below the skip purely as a compile-time reference. This deliberately avoids
  editing the out-of-scope `bridge_test.go` (which lies outside P1's two-file write
  scope) and adds no behavioral test.
- **(b) Execution-time strategy and P11 timeout recommendation.** Decision: a
  **single package** (`./test/e2e/...`) with **no build tags**, because the package
  shares one env via `TestMain`, runs serially in-process, and build-tagged groups
  would force multiple expensive `docker compose` bring-ups and break the
  shared-env / single post-suite health-check model. CI sharding, if needed, should
  use `go test -run <regex>` within the single package. Recommended for P11 (not
  applied in P1): raise `make test-e2e` `-timeout` to ~90–120m (suggest 120m for
  headroom), raise `test-go-e2e.yml` job `timeout-minutes` to ~120, optionally shard
  CI by `-run` group, and move the heaviest/lowest-signal tests
  (trigger-cert-mode interval measurement, committee updates) to a
  nightly/scheduled trigger.
- The cert-settlement signal reads the aggsender SQLite DB directly (read-only)
  rather than via the agglayer RPC, since the DB path is already exposed on the Env
  surface and is the lowest-friction pure-Go signal; the step explicitly allowed
  reading the aggsender SQLite. P2/P9 may layer an RPC cross-check if desired.

## Validation outcome

THUMBS_UP. Change-request count: **0**.

## Changed files

- `/home/aigent/repos/agglayer/aggkit/test/e2e/helpers_test.go` (created)
- `/home/aigent/repos/agglayer/aggkit/test/e2e/README.md` (updated)

## Commands run

- `go build ./test/e2e/...` → exit **0** (clean build).
- `go vet ./test/e2e/...` → exit **0** (compiles `_test.go`, incl. `helpers_test.go`
  and `TestHelpersCompile`).
- `golangci-lint run --timeout 5m ./test/e2e/...` (scoped) → **0 issues** (exit 0).

## Blockers / notes for future steps

- **IMPORTANT — repo-wide `make lint` is currently red for a pre-existing,
  out-of-scope reason.** `make lint` exits non-zero ONLY because of duplicate
  `func main` declarations in two untracked scratch files under `aggkit/tmp/`:
  `build_case1_override.go` and `check_keystore.go` (both dated 2026-04-23,
  predating this task; the working dir is not a git repo). These are unrelated to
  `test/e2e/` and outside every step's write scope. No `make lint` failure
  references any `test/e2e/` file.
  - **Guidance for P2–P10:** whose acceptance criteria mention `make lint`, verify
    lint via the scoped `golangci-lint run ./test/e2e/...` (which is clean) until
    the `aggkit/tmp/` scratch files are removed/fixed by their owner.
- **Reusable helper names now available to later steps:** `assertNetworkHealthy`,
  `bridgeETHL1ToL2AndClaim`, `bridgeERC20L2ToL1AndClaim`, `mintAndApproveERC20OnL2`,
  `bridgeMessageL1ToL2AndClaim`, `withCleanEmergencyState`,
  `waitForSettledCertificate`.

## Future-step updates

- **P2** (cert settlement) and **P9** (trigger cert modes): use
  `waitForSettledCertificate(ctx, t, env, timeout) uint64` (reads the aggsender DB
  read-only; `Settled` status == 4; treats a missing table as "not ready" and keeps
  polling).
- **P3** (core bridges) and **P7** (nightly orderings): use the bridge/ERC20/message
  helpers — `bridgeETHL1ToL2AndClaim`, `bridgeERC20L2ToL1AndClaim`
  (+ `mintAndApproveERC20OnL2`), and `bridgeMessageL1ToL2AndClaim`; the internal
  wait/convert helpers are reusable for bespoke asset/message claim flows.
- **P4** (sovereign) and **P8** (GER injection): wrap mutating bodies in
  `withCleanEmergencyState(ctx, t, env, func(){ ... })` instead of copying the inline
  defer block; still remove injected GER and return pooled keys yourself.
- The bats→Go mapping table lives in `test/e2e/README.md`; flip each row's Status
  from `pending — Pn` to `migrated` as it is ported (P14 sweeps this).
