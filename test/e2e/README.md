# E2E

This package contains tests run against a dockerized environment, loaded from `test/e2e/envs/loader.go`. The tests follow this flow:

1. Test env is loaded by `test/e2e/testmain_test.go`
2. Some sanity checks are performed to assert that the testing env is operating as expected
3. Tests in this package are then run
4. Finally, after the actual tests are run, if they pass, a L1 -> L2 and a L2 -> L1 bridge are going to be sent to validate that the network is still operational after the tests

## Inventory

- `test/e2e/removeger_test.go`: test the remove GER tool (`tools/remove_ger`)
- `test/e2e/helpers_test.go`: shared, reusable test helpers (network-health assertion, ERC20/ETH/message bridge-and-claim helpers, emergency-state defer/restore, cert-settlement waiter). See [Shared helpers](#shared-helpers).

## Conventions for migrated tests

These conventions apply to every test migrated from the legacy `agglayer/e2e` bats suite (see the [bats → Go mapping table](#bats--go-mapping-table)). Follow them so the migration stays mechanical and the shared env remains healthy.

- **One `*_test.go` per migrated bats file.** Name the file after the area being ported (e.g. `bridge-e2e.bats` → `bridge_core_test.go`).
- **One top-level `Test<Area>` with `t.Run` subtests, one subtest per bats `@test`.** The subtest name should match the bats test title (e.g. `t.Run("ERC20 deposit L1->L2", ...)`). This keeps a 1:1 traceability between a bats `@test` and a Go subtest.
- **Shared single env — mandatory cleanup/defers.** All tests in package `e2e` run in-process against **one** env instance loaded once in `TestMain` (`testEnv`). Any mutating test (GER injection, emergency state, aggkit stop/restart, extra containers, on-chain config changes) **must** restore state before returning, via `defer`:
  - Deactivate emergency state if it activated it — use `withCleanEmergencyState` (generalizes the inline defer blocks previously duplicated in `removeger_test.go`).
  - Remove any injected GER it added.
  - Return every key checked out from `env.Keys.L1Keys` / `env.Keys.L2Keys` with `defer pool.Return(key)`.
  - Restart/reset aggkit config if it changed it (use `env.RestartAggkitWithConfig`).
  - When in doubt, call `assertNetworkHealthy(ctx, t, env)` at the end so leaked state is caught close to its origin rather than only in the `TestMain` post-suite check.
- **`testing.Short()` skip.** Every E2E test must start with `if testing.Short() { t.Skip("Skipping E2E test in short mode") }`. In short mode `TestMain` does not load the env, so `testEnv` is nil.
- **Prefer Go over `cast`/`jq`.** Use the go-ethereum contract bindings and the bridge-service client (`env.Clients.BridgeService`) instead of shelling out. Only fall back to `cast`/`exec` when a binding genuinely does not exist, and guard such tests with `exec.LookPath("cast")` + `t.Skip` (see `TestJustBridge`).
- **Leave the env healthy.** The `TestMain` post-suite L1↔L2 / L2↔L1 bridge health-check runs after the whole suite; a test that leaks state will fail that check (and block teardown). Treat "env healthy after my test" as part of every test's contract.

### Shared helpers

Reusable helpers live in `helpers_test.go` (added in P1) and `bridge_utils.go` / `removeger_test.go` (pre-existing). Prefer these over re-implementing flows:

- `assertNetworkHealthy(ctx, t, env)` — runs `env.CheckEnv` plus a bridge-service health probe.
- `bridgeETHL1ToL2AndClaim(ctx, t, env, l1Opts, l2Opts, amount) *bridgeResult` — full ETH L1→L2 bridge + claim (wraps `BridgeL1ToL2WithResult`).
- `bridgeERC20L2ToL1AndClaim(ctx, t, env, l1Opts, l2Opts, amount)` — mint+approve the env `MintableERC20` on L2, then bridge+claim L2→L1.
- `mintAndApproveERC20OnL2(ctx, t, env, opts, amount)` — mint the env `MintableERC20` to `opts.From` and approve the L2 bridge.
- `bridgeMessageL1ToL2AndClaim(ctx, t, env, l1Opts, l2Opts, destination, amount, metadata) *bridgeResult` — message (LeafType 1) bridge + claim L1→L2.
- `withCleanEmergencyState(ctx, t, env, fn)` — runs `fn` and, on return, deactivates emergency state via the SovereignAdmin key if `fn` left it activated.
- `waitForSettledCertificate(ctx, t, env, timeout) uint64` — polls the aggsender SQLite DB (read-only, via `env.GetAggsenderDBPath()`) until a certificate with status `Settled` exists; returns the highest settled height.
- Pre-existing in `bridge_utils.go`: `BridgeL1ToL2`, `BridgeL1ToL2WithResult`, `BridgeL1NoClaim`, `BridgeL2ToL1`.
- Pre-existing in `removeger_test.go`: `pollWithBackoff`, `injectInvalidGER`, `assertGERExistsOnL2`/`assertGERRemovedFromL2`, `assertClaimedOnL2`/`assertClaimUnsetOnL2`, `performBridgeL1NoClaim`, `buildB1ClaimProof`/`executeB1Claim`, `buildFakeMerkleProofForWrongDepositCount`, `waitForGEROnBridgeService`, `waitForClaimOnBridgeService`, `detectInvalidGERFromAggkitLogs`.

## Bats → Go mapping table

Migrated from the legacy `agglayer/e2e` suite (`e2e/tests/aggkit/`) onto the single OP-PP env. Status reflects migration progress; the `MIGRATION_PLAN.md` step (P-number) tracks each row.

| Legacy bats file | Planned Go test file | Top-level test | Status |
|---|---|---|---|
| `e2e-pp.bats` | `cert_settlement_test.go` | `TestCertificateSettlement` | pending — P2 |
| `bridge-e2e.bats` | `bridge_core_test.go` | `TestBridge` | pending — P3 |
| `bridge-sovereign-chain-e2e.bats` | `sovereign_chain_test.go` | `TestSovereign` | pending — P4 |
| `claim-reetrancy.bats` | `claim_reentrancy_test.go` | `TestClaimReentrancy` | pending — P5 |
| `internal-claims.bats` | `internal_claims_test.go` | `TestInternalClaims` | pending — P6 |
| `bridge-e2e-nightly.bats` | `bridge_nightly_test.go` | `TestBridgeNightly` | pending — P7 |
| `latest-n-injected-ger.bats` (PP B2 case only) | `injected_ger_pp_test.go` | `TestInvalidGERInjectionB2_PP` | pending — P8 (FEP + `skip`-ped cases out of scope) |
| `trigger-cert-modes.bats` | `trigger_cert_modes_test.go` | `TestTriggerCertModes` | pending — P9 |
| `aggsender-committee-updates.bats` | `committee_updates_test.go` | `TestCommitteeUpdates` | pending — P10 (needs an extra `aggsender-validator` container/config) |

The remove-GER tool and a basic L1↔L2 / L2↔L1 bridge are already covered by `removeger_test.go` and the `TestMain` post-suite check — dedupe against these when migrating P3/P8.

### Out of scope (kept on the legacy kurtosis stack)

| Legacy bats file | Reason |
|---|---|
| `bridge-e2e-custom-gas.bats` | op-pp uses native ETH gas; no custom-gas env. |
| `bridge-e2e-2-chains.bats` / `bridge-e2e-3-chains.bats` | Multi-network (L2↔L2 / 3-chain); op-pp is single-network. |
| `bridge-e2e-aggoracle-committee.bats` | Needs FEP + aggoracle-committee env. |
| `tests/op/optimistic-mode.bats` | Tagged `op-fep`; requires FEP mode. |
| FEP-mode cases inside `latest-n-injected-ger.bats` | Require FEP; only the PP B2 case is migrated (P8). |

## Execution-time strategy (recommendation for P11)

**Decision: keep a single package (`./test/e2e/...`), no build tags.** The package already shares one env via `TestMain` and runs tests in-process serially; splitting into build-tagged groups would require multiple env bring-ups (each `docker compose up` is the dominant cost) and break the shared-env / single-post-suite-health-check model. Selection between groups is done with `go test -run <regex>` against the single package, which is enough for parallel CI sharding without build tags.

**Timeout impact (do NOT change the Makefile/CI in P1 — this is the recommendation for P11):**

- The Makefile currently runs the suite as `go test -v -timeout 30m ./test/e2e/...` (`test-e2e` target, verified). The new-stack CI job (`test-go-e2e.yml`) historically allots ~45m.
- The heavy migrated tests are slow: cert settlement (~10 min), the GER B-scenarios (up to 20–30 min), trigger-cert-mode interval measurement, and committee updates. Run back-to-back in one package they will exceed both the 30m `go test` timeout and the 45m CI job timeout.
- Recommended for P11:
  - Raise `make test-e2e` `-timeout` to at least **90m** (suggest `120m` headroom) so the full serial suite fits.
  - Raise the `test-go-e2e.yml` job `timeout-minutes` to **120** (matching/above the `go test -timeout`).
  - Optionally shard CI into parallel jobs by `-run` group (e.g. fast bridge tests vs. heavy GER/cert/committee tests) using `go test -run`, keeping the single package.
  - Move the heaviest, lowest-signal tests (e.g. trigger-cert-mode interval measurement, committee updates) behind a **nightly/scheduled** trigger so PR runs stay shorter.