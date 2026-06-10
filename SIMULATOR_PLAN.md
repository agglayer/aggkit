# Auto Claim Basic Filter Simulator Plan

## Scope

This plan is constrained to the Auto Claim code added on the current branch versus `develop`. It does not propose
unrelated refactors outside the modified Auto Claim startup, policy, claimer, sender, proof, storage, and type
boundaries.

The implementation must work with a normal JSON-RPC node. It must not require an archive node, `debug_*`, `trace_*`,
`debug_traceTransaction`, historical state replay, or internal call traces.

Nested bridge detection is intentionally skipped for this implementation. The simulator will not inspect or reject
nested bridge calls. It will document the limitation and leave room for a later implementation.

## Basic-Filter Behavior

`basic-filter` will support asset claims and optionally message claims:

1. Asset claims are always eligible for simulation.
2. Message claims are controlled by `Policy.AllowMessageClaims`.
   - `false`: reject message claims with `ReasonMessageClaimsRejected`.
   - `true`: simulate message claims and apply the same gas limit as asset claims.
3. `Policy.MaxGas` remains enforced for every simulated eligible claim.
   - `MaxGas == 0` keeps the gas ceiling disabled.
   - `GasUsed > MaxGas` rejects the request with `ReasonGasLimitExceeded`.
4. `Policy.AllowedOrigins` rejects requests whose origin network is not in the configured list when the list is
   non-empty.
5. `Policy.AllowedTokens` rejects asset claims whose origin token is not in the configured list when the list is
   non-empty.
6. Unsupported leaf types and simulator/RPC failures are errors, not policy rejections.
   - They must persist `last_error`.
   - They must stop the claimer from progressing until the process is restarted or the issue is fixed.

Because nested bridge detection is skipped, eligible asset and message simulations return
`NestedBridgeCallNotDetected`. Add explicit metadata such as `nested_bridge_detection = "skipped"` so operators do not
mistake this for real nested-call inspection.

## Implementation Steps

1. Add a shared claim transaction helper.
   - Create a small package such as `autoclaim/claimtx`.
   - Move the sender's ABI packing logic into `claimtx.PackClaim(request, proof)`.
   - Move `sender.claimGlobalIndex` into `claimtx.GlobalIndex`.
   - Keep `sender.packClaim` as a thin wrapper if that minimizes sender test churn.
   - Preserve existing sender tests for asset, message, and pre-Etrog global-index packing.

2. Add the production simulator package.
   - Create `autoclaim/simulator`.
   - Define a narrow client interface with `EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error)`.
   - Do not include `CallContext`, `debug_traceTransaction`, batch calls, trace APIs, or historical block parameters.
   - The simulator dependencies are:
     - destination RPC client
     - `autoclaimtypes.ProofPreparer`
     - `autoclaimtypes.ClaimerTarget`
     - transaction sender address from `EthTxManager.From()`

3. Make proof readiness explicit before simulation.
   - The simulator needs the exact proof used to pack the claim calldata.
   - For `basic-filter`, prepare the proof before policy evaluation if the request has no stored proof.
   - If proof preparation returns `nil, nil`, leave the request in `detected` and return no policy error. This is a
     pending condition, not a policy failure.
   - If proof preparation returns an error, store `last_error` and return a blocking error.
   - If a valid proof is prepared, save it before running simulation so the later sender path uses the same proof.

4. Apply static basic-filter checks before RPC simulation.
   - Reject message claims when `Policy.AllowMessageClaims` is false.
   - Reject origins not listed in `Policy.AllowedOrigins` when the list is non-empty.
   - Reject asset origin tokens not listed in `Policy.AllowedTokens` when the list is non-empty.
   - Return an error for unsupported leaf types.
   - Ignore `Policy.ManualFallback`; `basic-filter` must not convert errors to manual review.

5. Estimate gas for eligible asset and message claims.
   - Pack the claim calldata through the shared `claimtx.PackClaim`.
   - Build `ethereum.CallMsg` with:
     - `From`: `EthTxManager.From()`
     - `To`: configured target bridge address
     - `Value`: `0`
     - `Data`: packed claim calldata
   - Call `EstimateGas` against latest state only.
   - Return `SimulationResult{GasUsed: gas, NestedBridgeCall: NestedBridgeCallNotDetected}`.
   - Include metadata that nested bridge detection was skipped.
   - If `EstimateGas` fails, return a blocking policy error with enough context for `last_error`.

6. Keep deterministic policy decisions separate from operational errors.
   - Gas greater than `Policy.MaxGas`: rejected decision.
   - Message leaf while `Policy.AllowMessageClaims` is false: rejected decision.
   - Disallowed origin/token: rejected decision.
   - RPC failure, packing failure, unsupported leaf type, nil proof after a supposedly ready proof path, or invalid
     simulator wiring: error.

7. Wire the simulator through runtime.
   - Extend `runtime.Factories` with `NewTargetSimulator`.
   - In `DefaultFactories`, build the simulator from the claimer RPC client, proof preparer, claimer target, and
     transaction manager.
   - Construct `basic-filter` with `policy.WithTargetSimulator(simulator)`.
   - Do not require simulator construction for `allow-all`, `api-approve`, or `no-message`.
   - Startup must fail if a `basic-filter` claimer cannot construct its simulator.

8. Make policy errors stop claimer progression.
   - Add a sentinel or typed error such as `ErrPolicyBlocked`.
   - Wrap every `Policy.Evaluate` error with that sentinel after persisting `last_error`.
   - `Advance` returns the blocking error.
   - `Recover` stops immediately on the first blocking policy error and returns it instead of logging and continuing to
     later requests.
   - `Start` exits the claimer goroutine when `Recover` returns a blocking policy error.
   - The process supervisor or operator restart is then required after the underlying error is fixed.
   - Deterministic rejected policy decisions remain terminal request outcomes and do not stop the claimer.

9. Update policy behavior consistently.
   - `allow-all`: still approves and should not normally error.
   - `api-approve`: still returns manual approval and should not normally error.
   - `no-message`: unsupported leaf type remains an error and now blocks claimer progression through the common
     policy-error path.
   - `basic-filter`: errors from proof preparation, simulator wiring, calldata packing, or gas estimation block claimer
     progression through the common policy-error path.

## Test Plan

1. `autoclaim/claimtx`
   - Packs asset claim calldata exactly like current sender tests.
   - Packs message claim calldata exactly like current sender tests.
   - Preserves pre-Etrog global-index behavior.

2. `autoclaim/simulator`
   - Asset claim with ready proof calls `EstimateGas` and returns gas plus `NestedBridgeCallNotDetected`.
   - Message claim with `Policy.AllowMessageClaims = false` is rejected before `EstimateGas`.
   - Message claim with `Policy.AllowMessageClaims = true` calls `EstimateGas`.
   - Eligible asset and message simulations include metadata showing nested bridge detection was skipped.
   - Unsupported leaf type returns an error.
   - Proof not ready returns a pending result to the claimer path without recording a policy decision.
   - Proof-preparer error returns a blocking error.
   - `EstimateGas` error returns a blocking error.

3. `autoclaim/policy`
   - `basic-filter` rejects gas above `MaxGas`.
   - `basic-filter` approves asset claim when gas is within `MaxGas`.
   - `basic-filter` rejects message claim with `ReasonMessageClaimsRejected` when `AllowMessageClaims` is false.
   - `basic-filter` applies gas simulation to message claims when `AllowMessageClaims` is true.
   - `basic-filter` rejects disallowed origin/token.
   - `basic-filter` returns errors for simulator failures and unsupported leaf types.

4. `autoclaim/claimer`
   - Policy error persists `last_error`, returns `ErrPolicyBlocked`, and leaves the request in `detected`.
   - `Recover` stops at the first blocking policy error and does not advance later recoverable requests.
   - `Start` exits after a blocking policy error instead of retrying on the next tick.
   - A deterministic `basic-filter` message rejection from `AllowMessageClaims = false` does not stop the claimer.
   - A proof-not-ready pending condition does not stop the claimer and does not create a policy decision.

5. `autoclaim/runtime`
   - `basic-filter` claimer receives a non-nil simulator.
   - non-`basic-filter` claimers do not require simulator construction.
   - simulator construction failure fails startup only for the affected `basic-filter` claimer path.

6. Validation commands:
   - `go test -v ./autoclaim/claimtx ./autoclaim/simulator ./autoclaim/policy ./autoclaim/claimer ./autoclaim/sender ./autoclaim/runtime`
   - `go test -v ./autoclaim/...`
   - `make lint`

## Documentation Updates

1. Update `docs/autoclaim.md` so `basic-filter` is described as:
   - normal-RPC gas simulation for asset and configured message claims
   - `Policy.AllowMessageClaims` controls whether message claims are rejected or simulated
   - nested bridge detection is skipped in this implementation
   - policy errors block claimer progression rather than falling back to manual review
2. Remove or correct stale wording in `docs/autoclaim/P5_LOG.md` that says unsafe simulation becomes manual review.
3. Document that `Policy.ManualFallback` is not honored by `basic-filter` errors.
4. Document the normal-RPC limitation: indirect or direct nested bridge calls are not inspected until a later explicit
   nested-bridge detector is implemented.
