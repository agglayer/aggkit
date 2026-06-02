# P8 Log

**Step:** P8 — Migrate `latest-n-injected-ger.bats` (PP) → invalid-GER case B2 (PP mode)

**Final outcome:** Completed (validator THUMBS_UP, attempt 1). Live verification deferred to the P10b full-suite gate.

## Work done
Added `test/e2e/injected_ger_pp_test.go` with `TestInvalidGERInjectionB2_PP` — a thin port that:
- Honors `testing.Short()` at the top.
- Opens a 30-min context (matching `testRemoveGER_CategoryB2`'s budget).
- Wraps `testRemoveGER_CategoryB2(t)` in `withCleanEmergencyState`.
- Ends with `assertNetworkHealthy`.

It reuses the existing B2 lifecycle helper (real bridge → fake proof at wrong `deposit_count` → invalid GER inject → claim → detect → diagnose `ScenarioCategoryB2` → recover → restore) with NO duplication of hardcoded bats proofs/GERs (e.g. ger1/ger2, the two 32-element merkle proofs, global indexes `18446744073709551618`/`...619`). A top-of-function doc comment documents the port scope, the B2-vs-B1 distinction, the bats→Go mapping, and the four deliberately-skipped cases:
- B2 (FEP mode) — FEP, out of scope (this stack is op-pp/PP only).
- A (PP mode) — bats-skipped; hardcoded GER + claim proofs needing a fresh setup.
- A (FEP mode) — bats-skipped; hardcoded reason + FEP.
- Inject LatestBlock-N GER A case PP — bats-skipped; anvil-fork case, out of scope.

## Validation
THUMBS_UP (attempt 1). `go build ./test/e2e/...`, `go vet ./test/e2e/...`, and scoped `golangci-lint run ./test/e2e/...` (`0 issues.`) all clean (exit 0). Validator confirmed the skipped-cases comment matches the real bats `@test` headers (lines 396, 716, 825, 933) and that `testRemoveGER_CategoryB2` (removeger_test.go:974) genuinely implements the B2 PP scenario.

## Relationship to existing `TestRemoveGER_CategoryB2`
The new named test delegates to the same helper (the bats B2-PP scenario was already implemented programmatically in `testRemoveGER_CategoryB2`). `TestInvalidGERInjectionB2_PP` is the plan-requested faithful, bats-named port (so `go test -run` matches the bats case name), adding only the mutating-test wrapping (`withCleanEmergencyState`) and a final `assertNetworkHealthy`. Both exercise identical underlying B2 behavior with no logic drift, since the new test delegates rather than copies.

## Deviations
Delegation approach chosen (the prompt's strongly-preferred option) over inline duplication. The test inherits the documented PRE-EXISTING B2 flakiness (e.g. the 6-min "waiting for invalid GER in aggkit logs" timeout); this was NOT fixed — out of scope. Live run deferred to P10b.

## Change-request count
0.

## Changed files
- `test/e2e/injected_ger_pp_test.go` (created only).

No production/helper/removeger files touched (`removeger_test.go`, `helpers_test.go`, `bridge_utils.go`, production code, CI, env, and the plan all left untouched).

## Commands run
`go build ./test/e2e/...`, `go vet ./test/e2e/...`, scoped `golangci-lint run ./test/e2e/...` — all clean (exit 0), run by both executor and validator. The long live `go test -run TestInvalidGERInjectionB2_PP ./test/e2e/...` was NOT run (requires a running op-pp env, ~30+ min); no live results fabricated.

## Blockers / notes for future steps
- The pre-existing removeger CategoryB2 flakiness will also affect this test at the P10b full-suite gate — expect to re-run/stabilize there. If the new test flakes in P10b, the root cause is the shared B2 pipeline, not this entry point.
- In the full suite, both `TestRemoveGER_CategoryB2` and `TestInvalidGERInjectionB2_PP` run the same mutating B2 scenario back-to-back (each restores state) — be aware of the doubled wall-clock cost and shared-env serialization.
- golangci-lint is available at `/home/aigent/go/bin/golangci-lint`.
