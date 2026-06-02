# P7 Log — Migrate `bridge-e2e-nightly.bats` → asset/message ordering combinations

## Step
P7 — Migrate `bridge-e2e-nightly.bats` → asset/message ordering combinations.

## Final outcome
Completed. Validator returned THUMBS_UP on attempt 1. Live verification deferred to the P10b full-suite gate.

## Work done
Added `test/e2e/bridge_nightly_test.go` containing `TestBridgeNightly` with 6 subtests, one per bats
ordering combo. All combos are L1→L2 only — no L2→L1 settlement dependency, so the test avoids the
slow PP-certificate settlement path.

- Combos 1/2/5/6 defer claims (bridge both legs, then claim); combo 6 reverses the claim order
  (B-then-A). Combos 3/4 are fully sequential (claim before next bridge).
- Composed local in-file deferred bridge/claim helpers because no shared helper performs a valid
  deferred ERC20 claim (`BridgeL1NoClaim` is ETH-only; `executeB1Claim` uses an invalid GER):
  - `bridgeERC20L1ToL2NoClaim` / `claimERC20L1ToL2`
  - `bridgeMessageL1ToL2NoClaim` / `claimMessageL1ToL2`
  - `freshRecipient` (gas-free fresh recipient for exact wrapped-token `==` balance assertions)
  - `waitForBridgeByDepositCount` (deposit-count keyed bridge re-read at claim time)
- Reused P1/P3 wait helpers (`waitForBridgeByTxHash`, `waitForL1InfoTreeIndex`,
  `waitForInjectedL1InfoLeaf`, `claimProofToContractProofs`, `waitForWrappedTokenAddress`,
  `l1BridgeAddress`, `bridgeMessageL1ToL2AndClaim`, `pollWithBackoff`); no shared wait loop
  re-implemented except the new deposit-count lookup.
- Two distinct L1-origin ERC20s (labels A/B) deployed for combos 4/5/6 via the `mintableerc20`
  binding; amount 1e17 (0.1 token) matching bats `0.1ether`. Messages bridged with amount 0,
  load-bearing assertion is a successful `ClaimMessage` receipt.
- `testing.Short()` honored; pooled L1/L2 transactors checked out per subtest with immediate
  `defer Return`; `assertNetworkHealthy` runs at end of `TestBridgeNightly`.

## Validation
THUMBS_UP (attempt 1). `go build ./test/e2e/...`, `go vet ./test/e2e/...`, and scoped
`golangci-lint run ./test/e2e/...` all clean (`0 issues.`). All 6 combos and their ordering verified
against the bats, including the combo-1 asset-then-message nuance and the combo-6 reversed claim order.
Scope confirmed via mtime (repo is not a git repo): only `bridge_nightly_test.go` touched in the P7
window; no shared helper modified.

## Deviations
- Combo-1 follows the actual claimed-tx order (asset then message, per the bats `process_bridge_claim`
  tx-hash arguments) rather than the loose "asset A" prose label; documented in a header comment.
- Composed local split bridge/claim helpers in-file rather than editing shared helpers.
- Fresh gas-free recipients (P3 pattern) instead of a shared `$receiver`; each asset uses its own
  recipient (stricter than the bats single `$receiver`).
- Live run deferred to P10b.

## Change-request count
0.

## Changed files
- `test/e2e/bridge_nightly_test.go` (created only). No production, helper, or CI files touched.

## Commands run
`go build`, `go vet`, and scoped `golangci-lint` (all clean) by both executor and validator. Live
`go test -run TestBridgeNightly ./test/e2e/...` NOT run — deferred to P10b.

## Blockers / notes for future steps
None specific. The reusable local deferred-bridge/claim composition pattern is documented in-file. If
a future refactor adds a shared deposit-count lookup to `helpers_test.go`, the local
`waitForBridgeByDepositCount` could be dropped in favor of it. P10b live selector:
`go test -run TestBridgeNightly ./test/e2e/...` (needs a live op-pp env with healthy bridge service);
expected runtime ~30–60+ min across the six sequential combos.
