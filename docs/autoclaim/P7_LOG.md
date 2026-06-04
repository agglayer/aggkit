# P7 Step Log

## Summary

P7 added the Auto Claim sender package under `autoclaim/sender`. The sender encodes L2 bridge `claimAsset` and
`claimMessage` calls, submits the packed calldata through `EthTxManager.Add`, records transaction attempts, polls
`EthTxManager.Result`, and maps monitored transaction statuses back to Auto Claim request states.

The implementation prevents duplicate sends when storage already records a confirmed request or the target bridge
reports the claim as already completed. It also treats `ethtxmanager.ErrAlreadyExists` as an idempotent submission
outcome and persists the returned manager ID.

Focused tests cover asset and message ABI packing, successful transaction-manager submission, idempotent
`ErrAlreadyExists` handling, status polling, context cancellation, failed and evicted status behavior, duplicate
confirmed-claim prevention, and attempt persistence.

## Decisions And Deviations

- Added a narrow status transition allowance from `queued` or `sending` to `confirmed` in `autoclaim/types` so
  target-claimed requests can be persisted as confirmed without sending another transaction.
- Used a minimal local Polygon bridge ABI JSON in `autoclaim/sender` instead of importing
  `github.com/0xPolygon/cdk-contracts-tooling/contracts/banana/ipolygonzkevmbridgev2`, because that package was
  present in the module cache but not declared for this worktree.
- Sender tests compare packed calldata against the existing generated `test/contracts/claimmock` ABI.
- No deviations from the P7 acceptance criteria were reported.

## Final Validation

- Final outcome: THUMBS_UP
- Change-request count: 0
- Validator summary: THUMBS_UP
- Failed acceptance criteria: none
- Requested changes: none

## Changed Files

- `autoclaim/sender/sender.go`
- `autoclaim/sender/sender_test.go`
- `autoclaim/types/types.go`

## Commands Run

Worker implementation commands:

- `gofmt -w autoclaim/sender/sender.go autoclaim/sender/sender_test.go autoclaim/types/types.go`
- `go test -v ./autoclaim/sender` - failed initially due to an undeclared ABI package import; fixed by using local
  bridge ABI metadata.
- `gofmt -w autoclaim/sender/sender.go autoclaim/sender/sender_test.go`
- `go test -v ./autoclaim/sender` - failed while tightening tests for ABI unpacked raw array types and cancellation
  timing; production code was unchanged.
- `gofmt -w autoclaim/sender/sender_test.go`
- `go test -v ./autoclaim/sender` - failed once on a raw `bytes32` hash assertion type; the test assertion was fixed.
- `gofmt -w autoclaim/sender/sender_test.go`
- `go test -v ./autoclaim/sender` - passed.
- `go test -v ./autoclaim/types ./autoclaim/storage` - passed.
- `awk 'length($0) > 120 { print FILENAME ":" FNR ":" length($0) }' autoclaim/sender/sender.go autoclaim/sender/sender_test.go autoclaim/types/types.go` - passed after wrapping two test lines; no output.
- `go test -v ./autoclaim/...` - passed.
- `go test -v ./autoclaim/sender` - passed.

Validator commands/evidence:

- Inspected `/tmp/follow-plan/autoclaim-20260603T000000Z/P7/execution_deliverable.md`.
- Inspected `docs/autoclaim.md`, `docs/autoclaim-boundaries.md`, `autoclaim/sender/sender.go`,
  `autoclaim/sender/sender_test.go`, `autoclaim/types/types.go`, `autoclaim/types/interfaces.go`,
  `autoclaim/storage/storage.go`, `autoclaim/storage/storage_test.go`, `aggoracle/types/types.go`,
  `aggoracle/chaingersender/evm.go`, `test/helpers/mock_ethtxmanager.go`, and
  `test/contracts/claimmock/claimmock.go`.
- Confirmed no production Auto Claim sender direct generated binding transactors or `bind.TransactOpts` sends were
  found in `autoclaim`.
- Confirmed targeted tests passed:
  - `go test -v ./autoclaim/sender`
  - `go test -v ./autoclaim/types ./autoclaim/storage`
  - `go test -v ./autoclaim/...`

## Validation Evidence

The validator confirmed that the sender submits only through:

- `EthTxManager.Add(ctx, &target.BridgeAddr, common.Big0, data, target.GasOffset, nil)`

The validator also confirmed that ABI packing and sender behavior are covered by these focused tests:

- `TestPackClaimCalldataForAsset`
- `TestPackClaimCalldataForMessage`
- `TestSubmitClaimAddsTransactionAndConfirmsMinedResult`
- `TestSubmitClaimTreatsErrAlreadyExistsAsIdempotent`
- `TestSubmitClaimPollsInflightStatusesUntilConfirmed`
- `TestSubmitClaimHonorsContextCancellationWhilePolling`
- `TestSubmitClaimFailedStatusWithoutRetryMarksFailed`
- `TestSubmitClaimEvictedStatusWithRetryRequeues`
- `TestSubmitClaimPreventsDuplicateConfirmedClaims`

Persistence checks in the sender tests cover attempt recording after `Add`, monitored result updates, manager ID,
status, transaction data, retry counts, max retries, target address, and claim transaction hash when known.

## Blockers

None.

## Future-Step Updates

- Future claimer work can construct the sender with `sender.New(storage, ethTxManager, targetClaimReader, opts...)`.
- `SubmitClaim` returns a `TransactionAttempt` for submitted or monitored attempts.
- Stored-confirmed and target-claimed duplicate requests return a finalized no-op attempt and do not persist a
  transaction attempt because no send occurred.
- `target.WaitPeriod` controls polling when set; otherwise the sender fallback poll period is used.
- Failed or evicted monitored statuses with remaining retry budget transition the request to `queued` and return
  `ErrRetryableStatus`.
- Failed or evicted monitored statuses without retry budget transition the request to `failed` and return
  `ErrTerminalStatus`.
- The sender records an initial attempt after `Add` and updates the same attempt number as monitored results arrive;
  storage currently upserts by `(request_key, attempt_number)`.
