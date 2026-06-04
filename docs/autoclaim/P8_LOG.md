# P8 Step Log

## Summary

P8 added the Auto Claim claimer engine under `autoclaim/claimer`. The claimer owns one destination network and
orchestrates request enqueue, policy evaluation, proof readiness, queueing, sending, retry handling, confirmation,
failure, and restart recovery through the existing Auto Claim storage, policy, proof, and sender abstractions.

The implementation keeps duplicate enqueue calls idempotent, leaves manual-approval requests idle until an external
storage/API decision changes their status, routes requests by destination network, and guards disabled claimers and
wrong-destination requests from unsafe processing.

The first validation requested a recovery fix because mutable `OFFSET` pagination could skip recoverable rows after
earlier rows were advanced out of the recoverable status filter. The correction snapshots all recoverable request keys
across pages before advancing any request, and adds multi-page regression coverage over `policy-approved`, `queued`,
`sending`, and `sent` statuses.

## Decisions And Deviations

- Implemented a destination-network `Registry` in `autoclaim/claimer/registry.go` so later watchdog/runtime work can
  resolve the correct claimer for each bridge destination.
- Kept the claimer scoped to orchestration only. It does not discover bridges, add REST/API handlers, wire runtime
  startup, implement watchdog behavior, or submit transactions outside the P7 sender abstraction.
- Restart recovery uses a stable key snapshot before status mutation instead of repeatedly querying and advancing
  mutable recovery pages.
- No deviations from the P8 acceptance criteria remained after the validation correction.

## Final Validation

- Final outcome: THUMBS_UP
- Change-request count: 1
- Validator summary: First validation returned CHANGE_REQUEST for restart-recovery pagination skip risk; second
  validation returned THUMBS_UP after the stable-snapshot fix and multi-page regression test.
- Failed acceptance criteria after correction: none
- Requested changes after correction: none

## Changed Files

- `autoclaim/claimer/claimer.go`
- `autoclaim/claimer/registry.go`
- `autoclaim/claimer/claimer_test.go`

## Commands Run

Worker implementation commands:

- `gofmt -w autoclaim/claimer/claimer.go autoclaim/claimer/registry.go autoclaim/claimer/claimer_test.go` - passed.
- `go test -v ./autoclaim/claimer` - passed.
- `go test -v ./autoclaim/...` - passed.
- `go test ./...` - failed due to unrelated local environment failures in existing `claimsync` tests
  (`127.0.0.1:8545` already allocated and one insufficient-funds error) and e2e bridge-service startup timeout.
  P8 and all Auto Claim packages passed before those unrelated failures.
- `awk 'length($0) > 120 { print FILENAME ":" FNR ":" length($0) ":" $0 }' autoclaim/claimer/claimer.go autoclaim/claimer/registry.go autoclaim/claimer/claimer_test.go` - passed.
- `sed -n '1,220p' /tmp/follow-plan/autoclaim-20260603T000000Z/P8/validation_result_1.md` - passed; read the
  validation change request identifying mutable `OFFSET` pagination during restart recovery.
- `gofmt -w autoclaim/claimer/claimer.go autoclaim/claimer/claimer_test.go && go test -v ./autoclaim/claimer` -
  passed after the recovery snapshot fix and multi-page regression test.
- `go test -v ./autoclaim/...` - passed after the correction.
- `awk 'length($0) > 120 { print FILENAME ":" FNR ":" length($0) ":" $0 }' autoclaim/claimer/claimer.go autoclaim/claimer/claimer_test.go` - passed after the correction.

Validator commands/evidence:

- Inspected the P8 execution deliverable and both validation result files.
- Inspected `autoclaim/claimer/claimer.go`, `autoclaim/claimer/claimer_test.go`, and
  `autoclaim/claimer/registry.go`.
- Inspected P8-relevant Auto Claim type, storage, and sender contracts in `autoclaim/types`, `autoclaim/storage`,
  and `autoclaim/sender`.
- Confirmed no out-of-scope bridge discovery, L1-to-L2 watchdog, REST/API handler, runtime startup wiring, or direct
  transaction-manager implementation was added under `autoclaim/claimer`.
- Confirmed targeted tests passed:
  - `go test -v ./autoclaim/claimer`
  - `go test -v ./autoclaim/...`
- Confirmed the P8 claimer files have no lines over 120 characters.

## Validation Evidence

The final validator confirmed that the claimer:

- Constructs one claimer for one configured destination network.
- Rejects disabled and wrong-destination enqueue calls.
- Upserts requests through storage and skips terminal rows.
- Drives detected, manual, approved, queued, sending, and sent requests through the expected lifecycle.
- Gates sending on proof readiness and submits claims only through `c.sender.SubmitClaim`.
- Snapshots recoverable keys before mutating statuses during restart recovery.
- Provides destination-network routing through `claimer.NewRegistry`.

Focused tests cover:

- `TestEnqueueIsIdempotent`
- `TestPolicyApprovedFlowSendsAndConfirms`
- `TestPolicyRejectedFlowDoesNotSend`
- `TestManualFlowStaysIdleUntilApproved`
- `TestRetryExhaustionFailsRequest`
- `TestRestartRecoveryUsesStableSnapshotAcrossPages`
- `TestDisabledClaimerDoesNotEnqueueOrSend`
- `TestDestinationNetworkRouting`
- `TestStartRunsWithAPIDisabled`

## Blockers

None for P8.

Full-repository `go test ./...` remains blocked by unrelated local environment issues in existing `claimsync` and e2e
tests, while all P8 and Auto Claim package tests passed.

## Future-Step Updates

- P9 watchdog work can resolve claimers through `claimer.NewRegistry` and enqueue matching L1-to-L2 bridge exits by
  destination network.
- API/manual approval work can unblock manual requests by moving storage state from `manual-approval-required` to
  `policy-approved` or `policy-rejected`; the claimer keeps manual requests idle until that state changes.
- Runtime wiring can construct one `claimer.Claimer` per enabled destination and register them by destination network.
