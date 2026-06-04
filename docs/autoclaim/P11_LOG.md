# P11 Step Log

## Summary

P11 added Auto Claim runtime startup wiring and integrated it into `cmd/run.go`.

The runtime now has injectable construction seams for storage, target RPC clients, one `EthTxManager` per enabled
claimer, policies, target claim readers, senders, claimers, registry, L1-to-L2 watchdog, optional API construction,
and background goroutine starters. Startup is a disabled no-op when `AutoClaim.Enabled` is false.

`cmd/run.go` now starts Auto Claim only when the `autoclaim` component is selected and `AutoClaim.Enabled` is true.
It passes Auto Claim config, log config, DB timeout, REST config, L1 bridge sync, and L1 info tree sync into the
runtime startup path.

The command dependency predicates were updated so Auto Claim selects the L1 info tree sync, L1 bridge sync, and L1
reorg detector dependencies it needs, without implying aggsender, aggoracle, L2 bridge sync, L2 claim sync, bridge
service REST, or aggchain-proof-gen.

Runtime startup tests now cover disabled behavior, missing and typed-nil dependencies, invalid claimer config,
per-enabled-claimer transaction-manager construction, cancellation propagation, and API disabled behavior. Command
tests cover Auto Claim component/config selection and L1-only dependency selection.

## Decisions And Deviations

- Kept Auto Claim startup behind both component selection and `AutoClaim.Enabled`, preserving disabled no-op behavior.
- Added explicit dependency checks for missing and typed-nil L1 bridge sync and L1 info tree sync values so startup
  fails with clear Auto Claim errors before constructing runtime dependencies.
- Constructed target RPC clients and transaction managers only for enabled claimers.
- Kept Auto Claim dependency selection limited to L1 runtime dependencies; it does not imply aggsender, aggoracle,
  aggchain-proof-gen, L2 bridge sync, L2 claim sync, or bridge service REST.
- No deviations from the P11 acceptance criteria were reported.

## Final Validation

- Final outcome: THUMBS_UP
- Change-request count: 0
- Validator summary: THUMBS_UP
- Failed acceptance criteria: none
- Requested changes: none

## Changed Files

- `cmd/run.go`
- `cmd/run_autoclaim_test.go`
- `autoclaim/runtime/runtime.go`
- `autoclaim/runtime/runtime_test.go`

## Commands Run

Worker implementation commands:

- `gofmt -w autoclaim/runtime/runtime.go cmd/run.go` - passed.
- `go test -run TestDoesNotExist ./autoclaim/runtime ./cmd` - passed.
- `gofmt -w autoclaim/runtime/runtime_test.go && go test -v ./autoclaim/runtime` - passed.
- `gofmt -w cmd/run_autoclaim_test.go && go test -v ./cmd` - passed.
- `go test -v ./autoclaim/...` - passed.
- `awk 'length($0) > 120 {print FILENAME ":" FNR ":" length($0) ":" $0}' autoclaim/runtime/runtime.go autoclaim/runtime/runtime_test.go cmd/run.go cmd/run_autoclaim_test.go` - passed with no output after wrapping long lines.
- `gofmt -w autoclaim/runtime/runtime.go autoclaim/runtime/runtime_test.go && go test -v ./autoclaim/...` - passed.
- `go test -v ./cmd` - passed.
- `gofmt -w autoclaim/runtime/runtime_test.go && go test -v ./autoclaim/runtime ./cmd` - passed.
- `go test -v ./autoclaim/... && go test -v ./cmd` - passed.

Validator commands/evidence:

- `go test -v ./autoclaim/...` - passed.
- `go test -v ./cmd` - passed.
- Reviewed `cmd/run.go` startup integration and dependency predicates.
- Reviewed `autoclaim/runtime.Start` construction, validation, dependency checks, background starters, and optional API
  startup behavior.
- Reviewed `autoclaim/runtime/runtime_test.go` and `cmd/run_autoclaim_test.go` coverage against acceptance criteria.

## Validation Evidence

The validator confirmed that:

- `cmd/run.go` imports `autoclaim/runtime`, calls `autoclaimruntime.Start` only through
  `shouldRunAutoClaim(components, cfg.AutoClaim.Enabled)`, and passes the required config and L1 dependencies.
- Auto Claim is included in L1 info tree sync, L1 bridge sync, and L1 reorg detector predicates only.
- `autoclaim/runtime.Start` returns nil without side effects when disabled, validates enabled config, rejects missing
  and typed-nil L1 dependencies, opens storage, creates enabled-claimer RPC clients and transaction managers, builds
  policy, reader, sender, claimer, registry, watchdog, and starts the optional API only when configured.
- Runtime tests cover disabled no-op behavior, missing dependencies, invalid config, enabled-only transaction-manager
  construction, cancellation behavior, and API-disabled behavior.
- Command tests cover Auto Claim requiring both component selection and enabled config, plus L1-only dependency
  selection without unrelated components.

Fresh validation test results included:

- `go test -v ./autoclaim/...` - passed.
- `go test -v ./cmd` - passed.

## Blockers

None.

## Future-Step Updates

- Future P12/P13/P14 work can reuse the runtime factory seams for integration tests or wiring without creating network
  or transaction-manager side effects.
