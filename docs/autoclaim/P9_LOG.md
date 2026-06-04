# P9 Step Log

## Summary

P9 added the Auto Claim L1-to-L2 watchdog under `autoclaim/watchdog`. The watchdog polls L1 bridge sync progress,
reads bridge exits from `BridgeSource.GetBridges` over bounded block windows, filters to L1-origin deposits, resolves
the destination claimer through the P8 registry, and enqueues matching exits into the existing claimer path.

The implementation added durable cursor storage so watchdog progress survives restarts, plus overlap-safe handling
that skips already processed cursor positions and de-duplicates request keys within each polling window. Durable
request idempotency remains in the existing storage/claimer request path through `INSERT OR IGNORE`, primary request
keys, and the unique `(origin_network, destination_network, deposit_count)` constraint.

Focused tests cover polling windows, cursor persistence, duplicate overlap handling, destination filtering, unknown
destinations, bridge sync errors, restart from cursor, correct claimer routing, and cursor non-advancement when enqueue
fails.

## Decisions And Deviations

- Implemented watchdog discovery only. Proof preparation, target readiness checks, L1 info tree inclusion checks, and
  claim transaction sending remain delegated to the existing claimer/sender path.
- Filtered discoveries to `OriginNetwork == autoclaimtypes.L1OriginNetwork` before destination routing.
- Ignored unknown destination networks without enqueueing, using `ClaimerRegistry.ClaimerForDestination` as the routing
  boundary.
- Persisted bridge cursor state through concrete Auto Claim storage methods and a new `autoclaim_bridge_cursor`
  migration.
- Kept runtime startup wiring out of P9; that remains future work for P11.
- No deviations from the P9 acceptance criteria were reported.

## Final Validation

- Final outcome: THUMBS_UP
- Change-request count: 0
- Validator summary: THUMBS_UP
- Failed acceptance criteria: none
- Requested changes: none

## Changed Files

- `autoclaim/watchdog/l1_to_l2.go`
- `autoclaim/watchdog/l1_to_l2_test.go`
- `autoclaim/storage/migrations/autoclaim0002.sql`
- `autoclaim/storage/migrations/migrations.go`
- `autoclaim/storage/storage.go`
- `autoclaim/storage/storage_test.go`

## Commands Run

Worker implementation commands:

- `gofmt -w ...` - passed for:
  - `autoclaim/storage/migrations/migrations.go`
  - `autoclaim/storage/storage.go`
  - `autoclaim/storage/storage_test.go`
  - `autoclaim/watchdog/l1_to_l2.go`
  - `autoclaim/watchdog/l1_to_l2_test.go`
- `go test ./autoclaim/watchdog/...` - passed.
- `go test ./autoclaim/storage` - passed.
- `go test ./autoclaim/...` - passed.
- `awk 'length($0) > 120 { print FILENAME ":" FNR ":" length($0) ":" $0 }' ...` - passed with no
  output for:
  - `autoclaim/watchdog/l1_to_l2.go`
  - `autoclaim/watchdog/l1_to_l2_test.go`
  - `autoclaim/storage/storage.go`
  - `autoclaim/storage/storage_test.go`
  - `autoclaim/storage/migrations/migrations.go`
- `go test -count=1 ./autoclaim/watchdog/...` - passed.
- `go test -count=1 ./autoclaim/storage` - passed.
- `go test -count=1 ./autoclaim/...` - passed.

Validator commands/evidence:

- Inspected the P9 execution deliverable and validation result.
- Inspected `autoclaim/watchdog/l1_to_l2.go`, `autoclaim/watchdog/l1_to_l2_test.go`,
  `autoclaim/storage/storage.go`, `autoclaim/storage/storage_test.go`,
  `autoclaim/storage/migrations/autoclaim0001.sql`,
  `autoclaim/storage/migrations/autoclaim0002.sql`,
  `autoclaim/storage/migrations/migrations.go`, `autoclaim/types/interfaces.go`,
  `autoclaim/types/types.go`, and `autoclaim/claimer/claimer.go`.
- Confirmed targeted tests passed:
  - `go test ./autoclaim/watchdog/...`
  - `go test ./autoclaim/...`
  - `go test -count=1 ./autoclaim/watchdog/...`
  - `go test -count=1 ./autoclaim/storage`

## Validation Evidence

The validator confirmed that `L1ToL2.PollOnce`:

- Reads L1 bridge sync progress and polls bridge records through
  `BridgeSource.GetBridges(ctx, fromBlock, toBlock)`.
- Uses bounded polling windows through `WithBlockWindow`.
- Restarts from the persisted cursor and handles overlap-safe polling through cursor-position skipping.
- Filters for L1-origin deposits before routing.
- Routes known destinations through `ClaimerRegistry.ClaimerForDestination`.
- Ignores unknown destinations without enqueueing.
- Returns before cursor persistence on bridge sync errors and claimer enqueue errors, so cursor state is not advanced
  past failed work.
- Enqueues matching exits to the resolved destination claimer through `Claimer.Enqueue`.

Fresh validation test results included:

- `ok github.com/agglayer/aggkit/autoclaim/watchdog 0.003s`
- `ok github.com/agglayer/aggkit/autoclaim/storage 0.271s`

Fresh worker broad Auto Claim test results included:

- `ok github.com/agglayer/aggkit/autoclaim/claimer 0.030s`
- `ok github.com/agglayer/aggkit/autoclaim/policy 0.015s`
- `ok github.com/agglayer/aggkit/autoclaim/proof 0.046s`
- `ok github.com/agglayer/aggkit/autoclaim/sender 0.015s`
- `ok github.com/agglayer/aggkit/autoclaim/storage 0.173s`
- `ok github.com/agglayer/aggkit/autoclaim/types 0.019s`
- `ok github.com/agglayer/aggkit/autoclaim/watchdog 0.009s`

## Blockers

None.

## Future-Step Updates

- P11 runtime startup wiring should construct this watchdog with `l1BridgeSync`, Auto Claim storage, the P8 claimer
  registry, configured poll period, and an appropriate block window/overlap.
- Future work should continue to keep proof preparation, L1 info tree inclusion checks, target readiness checks, and
  claim transaction sending in the claimer path rather than duplicating that behavior in the watchdog.
