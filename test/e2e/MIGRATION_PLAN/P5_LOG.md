# P5 Log — Migrate `claim-reetrancy.bats` → reentrancy-protection tests

**Step:** P5 — Migrate `claim-reetrancy.bats` → reentrancy-protection tests

**Final outcome:** Completed (validator THUMBS_UP, attempt 1). Live verification deferred to the P10b full-suite gate.

## Work done

- Generated the `BridgeMessageReceiverMock` Go binding under `test/contracts/bridgemessagereceivermock/` (committed `.abi`/`.bin` + abigen-generated `.go` + `.sol` source), mirroring the `mintableerc20` package layout.
- Added the gen entry to `test/contracts/bind.sh` (`gen bridgemessagereceivermock`) and a `solc 0.8.20-alpine --via-ir` provenance block to `test/contracts/compile.sh`.
- Added `test/e2e/claim_reentrancy_test.go` with top-level `TestClaimReentrancy` and 2 subtests:
  - `PreventDoubleClaim` — reentrant `onMessageReceived` path; settles bridge #1 via reentrancy and asserts the duplicate direct claim is rejected by the already-claimed guard.
  - `TestClaimInternalReentrancyAndBridgeAsset` — `testClaim` exercising two valid claims + an internal invalid claim + an internal `bridgeAsset`.
- Deploys the mock once (mirroring the bats shared `setup()`), reuses P1/bridge_utils claim helpers and the L2Bridge bindings, returns pooled keys, and ends with `assertNetworkHealthy`. Includes a `testing.Short()` skip guard.

## Validation

- THUMBS_UP, attempt 1. All 8 per-check items PASS.
- `go build` (e2e package + `bridgemessagereceivermock` binding package), `go vet ./test/e2e/...`, and scoped `golangci-lint run ./test/e2e/...` (v2.10.1) all clean (`0 issues.`).
- ABI tuple encoding cross-checked byte-for-byte against the legacy bats `cast abi-encode` output; validator also confirmed the committed `.abi` is byte-identical to the authoritative source artifact.

## Deviations (validator-approved)

- Native token encoded as the zero address (`common.Address{}`) for the internal `bridgeAsset` — canonical L2 native-token representation, functionally equivalent to the bats `native_token_addr`.
- STEP-13 internal-bridgeAsset assertion done by parsing the `testClaim` receipt's L2 `BridgeEvent` (`agglayerbridgel2.ParseBridgeEvent`; checks destNetwork=2, destination=receiver, amount=0.0004 ETH, originAddress=mock) instead of the bridge-service `get_bridge`-by-txhash. Avoids helper edits and is strictly stronger (adds origin + network checks).
- Per-field `get_claim` proof equality (bats STEP 9) not re-asserted field-by-field; structurally covered by proof-derived params + `IsClaimed` + exact balance deltas. No reentrancy-intent coverage lost.
- Live `go test -run TestClaimReentrancy` not run — deferred to P10b per the verification strategy.

## Change-request count: 0

## Changed files

New (in scope):
- `test/contracts/bridgemessagereceivermock/BridgeMessageReceiverMock.sol` (provenance source)
- `test/contracts/bridgemessagereceivermock/Interfaces.sol` (provenance dependency)
- `test/contracts/bridgemessagereceivermock/bridgemessagereceivermock.go` (generated binding)
- `test/contracts/abi/bridgemessagereceivermock.abi` (committed ABI)
- `test/contracts/bin/bridgemessagereceivermock.bin` (committed bytecode, no `0x` prefix)
- `test/e2e/claim_reentrancy_test.go` (the migrated test)

Modified (in scope):
- `test/contracts/bind.sh` — additive `gen bridgemessagereceivermock` line
- `test/contracts/compile.sh` — additive `solc 0.8.20-alpine --via-ir` provenance block

No production / helper / `bridge_utils.go` / `mintableerc20` files touched.

## Commands run

- `abigen` (binding generation, v1.17.0-stable) from the committed `.abi`/`.bin`.
- `go build ./test/e2e/...`, `go build ./test/contracts/bridgemessagereceivermock/...`, `go vet ./test/e2e/...`, `golangci-lint run ./test/e2e/...` — all clean (executor + validator).
- ABI encoding equivalence cross-check vs `cast abi-encode` (scratch test, removed afterward).

## Blockers / notes for future steps

- None for P5.
- **P6 (internal-claims)** uses the SAME binding recipe with `InternalClaims.json` (source `e2e/core/contracts/bridgeAsset/`): abigen is available, solc is not — use the committed Foundry artifact, extract `abi`/`bin` via `jq`, then abigen. Mirror this package's layout, append `gen internalclaims` to `bind.sh` and a solc block to `compile.sh`, and reference `Deploy<Pkgcap>`/`New<Pkgcap>` so `go build ./test/e2e/...` compiles it in.
- Reusable from `claim_reentrancy_test.go` (same `e2e` package; do not modify, add new if signatures differ): `claimParams` struct, `bridgeMessageL1ToL2GetParams`, `encodeClaimDataTuple`/`encodeBridgeAssetTuple` (flat `abi.Arguments` list, NOT a wrapped single-tuple type), `assertDuplicateClaimMessageRejected`, `assertClaimed`, and the deploy-once-in-top-level-test pattern.
