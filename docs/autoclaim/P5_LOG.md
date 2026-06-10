# P5 Step Log

## Summary

P5 added the Auto Claim policy registry and concrete policy implementations for `allow-all`, `api-approve`,
`no-message`, and `basic-filter`. The registry resolves named policies and rejects invalid policy names. The static
policies now return deterministic names, results, and reason strings.

The worker also added `PolicyConfig.MaxGas` parsing and focused policy tests. `basic-filter` uses a bounded
`TargetSimulator` abstraction so later claimer or sender work can provide target-chain simulation without changing the
policy API. Its behavior is conservative: it rejects gas usage above configured `MaxGas`, rejects detected nested bridge
calls, and now returns blocking policy errors when simulation is unavailable, fails, returns nil, or cannot safely
determine nested bridge status.

## Decisions And Deviations

- Added a local `TargetSimulator` abstraction for `basic-filter`; this keeps policy evaluation independent from later
  proof, RPC, claimer, and sender implementations.
- Treated unknown or unsafe nested bridge inspection as a blocking policy error rather than approval or manual review.
- Defined `PolicyConfig.MaxGas == 0` as disabling the gas ceiling check; nonzero values reject only when simulated gas is
  greater than the configured value.
- The validator noted one narrow non-`autoclaim/*` touch: a repository-level config parsing test assertion for `MaxGas`,
  directly tied to the P5 config parsing acceptance criterion.
- No deviations from the P5 acceptance criteria were reported.

## Final Validation

- Final outcome: THUMBS_UP
- Change-request count: 0
- Validator summary: THUMBS_UP
- Failed acceptance criteria: none
- Requested changes: none

## Changed Files

- `autoclaim/config/config.go`
- `autoclaim/policy/registry.go`
- `autoclaim/policy/policy.go`
- `autoclaim/policy/basic_filter.go`
- `autoclaim/policy/policy_test.go`
- `config/config_test.go`

## Commands Run

Worker implementation commands:

- `which goimports` - failed because `goimports` is not installed in this environment.
- `gofmt -w autoclaim/config/config.go autoclaim/policy/registry.go autoclaim/policy/policy.go autoclaim/policy/basic_filter.go autoclaim/policy/policy_test.go config/config_test.go`
- `gci write --skip-generated -s standard -s default -s 'prefix(github.com/agglayer/aggkit)' autoclaim/config/config.go autoclaim/policy/registry.go autoclaim/policy/policy.go autoclaim/policy/basic_filter.go autoclaim/policy/policy_test.go config/config_test.go`
- `go test -v ./autoclaim/policy`
- `go test -v ./autoclaim/config ./autoclaim/types`
- `go test -v ./config`
- `go test -v ./autoclaim/...`

Validator commands/evidence:

- Inspected `autoclaim/policy/registry.go`, `autoclaim/policy/policy.go`, `autoclaim/policy/basic_filter.go`,
  `autoclaim/config/config.go`, `autoclaim/policy/policy_test.go`, and `config/config_test.go`.
- Confirmed targeted tests passed:
  - `go test -v ./autoclaim/policy`
  - `go test -v ./autoclaim/config ./autoclaim/types`
  - `go test -v ./config`
  - `go test -v ./autoclaim/...`

## Validation Evidence

The validator confirmed that `Registry.NewPolicy` resolves `allow-all`, `api-approve`, `no-message`, and
`basic-filter`, and returns an unknown-policy error for invalid names. Invalid policy names are covered by
`TestRegistryRejectsInvalidPolicyName`, and config-level unknown policy validation is covered by
`TestLoadConfigWithInvalidAutoClaim/unknown_policy`.

Policy tests cover all required outcomes:

- Approved: `TestAllowAllApproves`, `TestNoMessageApprovesAssetClaims`,
  `TestBasicFilterApprovesWhenGasAndNestedBridgeChecksPass`.
- Rejected: `TestNoMessageRejectsMessageClaims`, `TestBasicFilterRejectsGasOverMaxGas`,
  `TestBasicFilterRejectsDetectedNestedBridgeCalls`.
- Manual: `TestAPIApproveRequiresManualDecision`.
- Blocking errors: `TestBasicFilterReturnsErrorWhenTargetSimulationUnavailable`,
  `TestBasicFilterReturnsErrorWhenTargetSimulationFails`,
  `TestBasicFilterReturnsErrorWhenNestedBridgeInspectionIsUnsafe`.

The validator also confirmed that `config/config_test.go` parses `MaxGas = 500000` and asserts
`claimer.Policy.MaxGas == 500000`.

## Blockers

None.

## Future-Step Updates

- Later claimer or runtime code can construct policies with `policy.NewRegistry(policy.WithTargetSimulator(simulator))`.
- `basic-filter` expects a simulator implementing `SimulateClaim(ctx, request)` and returning
  `SimulationResult{GasUsed, NestedBridgeCall, Metadata}`.
- `NestedBridgeCallUnknown` or any unrecognized nested-call status causes a blocking policy error.
- `PolicyConfig.MaxGas == 0` disables the gas ceiling check; nonzero `MaxGas` rejects only when simulated gas is greater
  than the configured value.
