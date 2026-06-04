# P4 Step Log

## Summary

P4 added the Auto Claim SQLite storage layer and embedded migrations. The worker created the
`autoclaim/storage` repository with DB opening and migration helpers, idempotent request enqueueing, request retrieval,
filtered listing, restart-recovery queries, policy/manual decision persistence, proof persistence, transaction-attempt
recording, atomic lifecycle transitions, and last-error updates.

The worker also made a narrow P3 contract extension by adding `ListRecoverableRequests` and `RecoveryFilter` to support
restart recovery. Focused storage tests were added for schema creation, idempotent enqueue, duplicate prevention,
filters and pagination, transition preconditions, transaction-attempt recording, recovery queries, missing-row behavior,
timestamp updates, and temporary DB paths.

## Decisions And Deviations

- Added `ListRecoverableRequests(ctx context.Context, filter RecoveryFilter) (*RequestPage, error)` because the existing
  P3 storage contract only exposed single-status `ListRequests` filtering and did not provide an explicit restart
  recovery query.
- Stored bridge, proof, decision, and attempt payloads as JSON while also persisting normalized columns needed for
  uniqueness, API filters, and recovery queries.
- Kept the implementation scoped to storage and storage-specific type additions. The validator did not find transaction
  sending, policy evaluation, proof network clients, API handlers, watchdogs, or runtime startup in P4-owned files.

## Final Validation

- Final outcome: THUMBS_UP
- Change-request count: 0
- Validator summary: THUMBS_UP
- Failed acceptance criteria: none
- Requested changes: none

## Changed Files

- `autoclaim/types/types.go`
- `autoclaim/types/interfaces.go`
- `autoclaim/storage/migrations/autoclaim0001.sql`
- `autoclaim/storage/migrations/migrations.go`
- `autoclaim/storage/storage.go`
- `autoclaim/storage/storage_test.go`

## Commands Run

Worker implementation commands:

- `gofmt -w autoclaim/types/types.go autoclaim/types/interfaces.go autoclaim/storage/migrations/migrations.go autoclaim/storage/storage.go autoclaim/storage/storage_test.go`
- `go test -v ./autoclaim/types ./autoclaim/storage/...`
- `gofmt -w autoclaim/storage/storage.go autoclaim/storage/storage_test.go && go test -v ./autoclaim/types ./autoclaim/storage/...`
- `awk 'length($0) > 120 { print FILENAME ":" FNR ":" length($0) ":" $0 }' autoclaim/types/types.go autoclaim/types/interfaces.go autoclaim/storage/storage.go autoclaim/storage/storage_test.go autoclaim/storage/migrations/migrations.go autoclaim/storage/migrations/autoclaim0001.sql`

Validator commands:

- `sed -n '1,220p' /home/aigent/.codex/skills/follow-plan-parallel/SKILL.md`
- `sed -n '1,240p' /tmp/follow-plan/autoclaim-20260603T000000Z/P4/validation_prompt.md`
- `sed -n '1,240p' /tmp/follow-plan/autoclaim-20260603T000000Z/P4/execution_deliverable.md`
- `git status --short`
- `sed -n '1,220p' docs/autoclaim.md`
- `rg --files autoclaim | sort`
- `git diff --stat`
- Targeted `sed` inspection of `autoclaim/types`, `autoclaim/storage`, and storage migration files.
- Targeted `rg` inspection for request filters, recovery, manual decisions, proof persistence, transition handling,
  transaction-attempt handling, and out-of-scope runtime/network behavior.
- `go test -v ./autoclaim/types ./autoclaim/storage/...`
- `go test -run TestMigrationCreatesExpectedSchema -count=1 -v ./autoclaim/storage`
- `go test -count=1 -v ./autoclaim/types ./autoclaim/storage/...`
- `awk 'length($0) > 120 { print FILENAME ":" FNR ":" length($0) ":" $0 }' autoclaim/types/types.go autoclaim/types/interfaces.go autoclaim/storage/storage.go autoclaim/storage/storage_test.go autoclaim/storage/migrations/migrations.go autoclaim/storage/migrations/autoclaim0001.sql`
- `ls -l /tmp/follow-plan/autoclaim-20260603T000000Z/P4/validation_result_1.md`

## Validation Evidence

The validator confirmed that `autoclaim/storage` implements the P3 storage contract with
`var _ autoclaimtypes.Storage = (*Storage)(nil)`. Embedded migrations create `autoclaim_request` and
`autoclaim_transaction_attempt`, run through the existing DB migration helper, and enforce uniqueness on
`origin_network`, `destination_network`, and `deposit_count`.

The validator also confirmed that stored request data includes bridge fields, lifecycle status, policy/manual decision
JSON, proof JSON and reference fields, transaction-attempt metadata reflected on the request, claim transaction hash,
transaction-manager ID, retry counts, timestamps, and last error. `ListRequests` supports the expected origin network,
destination network, status, policy result, bridge transaction hash, claim transaction hash, block-range, and pagination
filters.

Final targeted validation passed:

- `go test -v ./autoclaim/types ./autoclaim/storage/...`
- `go test -run TestMigrationCreatesExpectedSchema -count=1 -v ./autoclaim/storage`
- `go test -count=1 -v ./autoclaim/types ./autoclaim/storage/...`
- The line-length scan over P4 files produced no output.

## Blockers

None.

## Future-Step Updates

- Later claimer and runtime steps can use `ListRecoverableRequests` with default statuses `queued`, `sending`, and
  `sent`, optionally scoped by destination network.
- Manual decisions are persisted separately but round-trip through `AutoClaimRequest.PolicyDecision` when no automatic
  policy decision is stored, because the P3 domain type has a single decision field.
