# P6 Step Log

## Summary

P6 added the Auto Claim L1-origin proof preparation package under `autoclaim/proof`. The new preparer builds complete
claim proof inputs for L1 to L2 bridge exits without using the bridge service REST API.

The implementation finds the first L1 info tree index that contains the bridge, fetches the selected L1 info tree leaf,
retrieves the local exit root proof from the L1 bridge syncer, retrieves the rollup exit root proof from the L1 info tree
syncer, and converts both proofs into the fixed `[32][32]byte` bridge-binding shape through
`autoclaim/types.ProofToABIProof`.

Focused tests cover pending/not-ready handling, proof lookup failures, successful proof construction, ABI proof
conversion, and parity with the existing bridge service L1-origin claim proof fields.

## Decisions And Deviations

- Added small proof-preparer interfaces for only the bridge and L1 info tree syncer methods required by P6.
- Preserved bridge-service pending semantics for `bridgeservice.ErrNotOnL1Info`: `Prepare` returns `Ready=false` with no
  error, while `PrepareProof` returns `nil, nil` for compatibility with the existing `autoclaim/types.ProofPreparer`
  interface.
- Matched the existing bridge service L1 info tree lookup behavior, including the fallback from `GetRootByLER` to
  `GetLastRoot`.
- Did not implement L2-origin proof preparation, matching the P6 non-goal.
- No deviations from the P6 acceptance criteria were reported.

## Final Validation

- Final outcome: THUMBS_UP
- Change-request count: 0
- Validator summary: THUMBS_UP
- Failed acceptance criteria: none
- Requested changes: none

## Changed Files

- `autoclaim/proof/preparer.go`
- `autoclaim/proof/preparer_test.go`

## Commands Run

Worker implementation commands:

- `sed -n '1,220p' /home/aigent/.codex/skills/follow-plan-parallel/SKILL.md`
- `sed -n '1,260p' /tmp/follow-plan/autoclaim-20260603T000000Z/P6/execution_prompt.md`
- `git status --short`
- `rg -n "getFirstL1InfoTreeIndexForL1Bridge|ClaimProofHandler|ErrNotOnL1Info|GetProof|GetRollupExitTreeMerkleProof" bridgeservice bridgesync l1infotreesync test/e2e autoclaim`
- `rg --files autoclaim bridgeservice bridgesync l1infotreesync test/e2e | sort`
- Multiple focused `sed` and `rg` inspection commands over `autoclaim/types`, `bridgeservice`, `bridgesync`,
  `l1infotreesync`, and `tree/types`.
- `which goimports || true; which gci || true` - `goimports` was not found; `gci` was found.
- `gofmt -w autoclaim/proof/preparer.go autoclaim/proof/preparer_test.go`
- `rg -n "gci|goimports" Makefile .golangci.yml .golangci.yaml -S` - reported `.golangci.yaml` missing while still
  showing relevant `.golangci.yml` config.
- `go test ./autoclaim/proof`
- `gci write --skip-generated -s standard -s default -s 'prefix(github.com/agglayer/aggkit)' autoclaim/proof/preparer.go autoclaim/proof/preparer_test.go`
- `go test ./autoclaim/...`
- Final rerun: `gofmt -w autoclaim/proof/preparer.go autoclaim/proof/preparer_test.go`
- Final rerun: `gci write --skip-generated -s standard -s default -s 'prefix(github.com/agglayer/aggkit)' autoclaim/proof/preparer.go autoclaim/proof/preparer_test.go`
- Final rerun: `go test ./autoclaim/proof`
- Final rerun: `go test ./autoclaim/...`
- `git status --short autoclaim/proof`
- `git status --short autoclaim/proof /tmp/follow-plan/autoclaim-20260603T000000Z/P6` - failed because the temp
  directory is outside the repository; non-blocking.
- `git diff --no-index -- /dev/null autoclaim/proof/preparer.go` - exited with diff status `1` as expected for a new
  file.
- `git diff --no-index -- /dev/null autoclaim/proof/preparer_test.go` - exited with diff status `1` as expected for a
  new file.

Validator commands/evidence:

- Inspected `/tmp/follow-plan/autoclaim-20260603T000000Z/P6/execution_deliverable.md`.
- Inspected `autoclaim/proof/preparer.go` and `autoclaim/proof/preparer_test.go`.
- Compared proof calls and index lookup behavior with `bridgeservice/bridge.go` L1-origin `ClaimProofHandler` and
  `getFirstL1InfoTreeIndexForL1Bridge`.
- Inspected `autoclaim/types` proof conversion additions.
- Confirmed no scoped diffs in existing context-pack files:
  `bridgeservice/bridge.go`, `bridgeservice/bridge_interfaces.go`, `bridgesync/bridgesync.go`,
  `l1infotreesync/l1infotreesync.go`, and `test/e2e/bridge_utils.go`.
- Confirmed targeted tests passed:
  - `go test ./autoclaim/proof`
  - `go test ./autoclaim/...`

## Validation Evidence

The validator confirmed that the implementation is scoped to L1-origin proof preparation, uses small syncer interfaces,
rejects non-L1 origins, and does not implement L2-origin proof preparation.

The validator also confirmed that proof calls and index lookup behavior are equivalent to the existing bridge service
L1-origin claim proof path, including:

- `bridgeL1.GetProof(ctx, depositCount, info.MainnetExitRoot)`
- `l1InfoTree.GetRollupExitTreeMerkleProof(ctx, 0, info.RollupExitRoot)`

Test coverage includes:

- `TestPreparePendingWhenBridgeNotYetIncludedOnL1InfoTree`
- `TestPrepareReturnsProofLookupFailures`
- `TestPrepareBuildsSuccessfulL1OriginClaimProof`
- `TestProofToABIProofPreservesExpectedShape`
- `TestPrepareMatchesBridgeServiceL1ClaimProofFields`

## Blockers

None.

## Future-Step Updates

- Future claim-queue steps can use `Preparer.Prepare(ctx, request)` when explicit pending/not-ready state is needed.
- Existing callers constrained to `autoclaim/types.ProofPreparer` can use `PrepareProof(ctx, request)`; a `nil, nil`
  result means the L1-origin bridge is not ready yet and should remain pending.
