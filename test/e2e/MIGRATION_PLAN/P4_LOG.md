# P4 Log

- **Step:** P4 — Migrate `bridge-sovereign-chain-e2e.bats` → sovereign bridge + invalid GER on L2

## Final outcome
Completed. Validator returned THUMBS_UP on attempt 1. Authoritative live verification is deferred
to the P10b full-suite gate per the agreed verification strategy.

## Work done
Created `test/e2e/sovereign_chain_test.go` with top-level test `TestSovereignChain` (matches
`go test -run TestSovereign`) and two `t.Run` subtests:

- **`SovereignTokenAddressMapping`** — SovereignAdmin transactor calls
  `SetMultipleSovereignTokenAddress` then `RemoveLegacySovereignTokenAddress`; decodes the emitted
  events from the receipt logs (`ParseSetSovereignTokenAddress` /
  `ParseRemoveLegacySovereignTokenAddress`) and asserts field values (`OriginNetwork==0`,
  `OriginTokenAddress`, `SovereignTokenAddress`, `IsNotMintable==false`, removed legacy address).
- **`InvalidGEROnL2BridgesValid`** — `performBridgeL1NoClaim` → `buildB1ClaimProof` →
  `injectInvalidGER` → `executeB1Claim` → assert claimed/indexed → `removeInvalidGER` →
  `assertGERRemovedFromL2`.

Both subtests are wrapped in `withCleanEmergencyState` and defer GER/mapping removal plus
`assertNetworkHealthy`. Reuses `removeger_test.go` and P1 helpers by direct call; new file-local
helpers (`decodeSetSovereignTokenAddressEvent`, `decodeRemoveLegacySovereignTokenAddressEvent`,
`removeInvalidGER`, `getIndexedClaim`, `forceEmitDetailedClaimEvent`) are genuinely new.

## Validation
THUMBS_UP. Fast checks all exit 0:
- `go build ./test/e2e/...` → BUILD_EXIT=0
- `go vet ./test/e2e/...` → VET_EXIT=0
- scoped `golangci-lint run ./test/e2e/...` → `0 issues.` LINT_EXIT=0

Both required bats cases ported faithfully (mapping events + invalid-GER-on-L2).

## Deviations / decisions (emphasized)
1. **Invalid GER construction** — derived via the existing `buildB1ClaimProof` path because the env
   exposes no L1 GER binding (`env.L1.Contracts` has only `RollupManager` and `Bridge`). No L1 GER
   binding was added. The "bridges are valid" property is preserved identically; the bats
   `rollup_exit_root == invalid_rer` assertion becomes
   `indexed claim rollup_exit_root == proof.RollupExitRoot`.
2. **MigrateLegacyToken sub-portion OMITTED** — judged not part of P4's two named cases and
   unsupported by the env (single pre-deployed `MintableERC20`, no arbitrary-ERC20 deploy helper, no
   `grantRole`/`migrateLegacyToken` plumbing, no `GetLegacyTokenMigrations` helper). Validator
   confirmed this is acceptable (no CHANGE_REQUEST).
3. **Live verification deferred to P10b** — long live `go test -run TestSovereign` intentionally not
   run; no live results fabricated.

## Change-request count
0.

## Changed files
- `test/e2e/sovereign_chain_test.go` (created).

No production or helper files were touched.

## Commands run
`go build`, `go vet`, and scoped `golangci-lint` — all clean — run by both the executor and the
validator.

## Blockers / notes for future steps
- None blocked the two required cases.
- **P8** (invalid-GER PP case) can reuse the same `injectInvalidGER` / `buildB1ClaimProof` /
  `assertGER*` pattern and the `withCleanEmergencyState` wrapper used here.
- `removeInvalidGER` is the SovereignAdmin-key inverse of `injectInvalidGER` (aggoracle key); P8 can
  reuse it for SovereignAdmin GER removal.
