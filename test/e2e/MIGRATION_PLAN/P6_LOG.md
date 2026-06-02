# P6 Log — Migrate `internal-claims.bats` → triple internal-claim combinations

**Step:** P6 — Migrate `internal-claims.bats` → triple internal-claim combinations

**Final outcome:** Completed (validator THUMBS_UP, attempt 1). Live verification deferred to the P10b full-suite gate.

## Work done
- Generated the `InternalClaims` Go binding under `test/contracts/internalclaims/` — committed `abi`/`bin` extracted from the authoritative Foundry artifact (`InternalClaims.json`, `.bytecode.object`) plus the `abigen`-generated `.go` and provenance `.sol` files — mirroring the P5 custom-contract recipe.
- Additive entries only: appended `gen internalclaims` to `bind.sh` and a `solc 0.8.20-alpine --via-ir` provenance block to `compile.sh` (compile.sh block document-only, not executed).
- Added `test/e2e/internal_claims_test.go` with top-level `TestInternalClaims` and 4 subtests: `ThreeSuccess`, `SuccessFailSuccess`, `FailSuccessFail`, `SameGlobalIndexFailSuccessFail`.
- ASSET claim flow (`claimAsset` / WETH); deploys the contract once on L2 (constructor arg = L2 bridge address) and shares it across subtests.
- Added test-local helpers `bridgeAssetL1ToL2GetParams` and `assertNotClaimed`; reused shared `claimParams` / `assertClaimed` / `waitFor*` / proof helpers unmodified.
- Per-claim success/failure asserted via `IsClaimed` + exact WETH balance deltas (all legs to a single key-less receiver EOA for clean deltas). Returns pooled keys (Checkout/Return) and ends with `assertNetworkHealthy`.

## Validation
- THUMBS_UP on attempt 1.
- `go build` (binding + e2e), `go vet`, and scoped `golangci-lint run ./test/e2e/...` all clean (`0 issues.`, exit 0).
- Committed ABI matches the Foundry artifact byte-for-byte (canonical-sorted `jq` comparison); committed BIN content identical to `.bytecode.object` (only delta was a trailing newline marker).
- Scenario 4 confirmed genuinely distinct from scenario 3: slot 1 is armed with leg-2's global index (`p1mal.globalIndex = new(big.Int).Set(p2.globalIndex)`) while staying malformed — the load-bearing difference. Malformed slots revert inside the contract try/catch and are swallowed; `onMessageReceived` itself still succeeds (asserted via receipt status).

## Deviations (validator-approved)
- On-chain `IsClaimed` + exact WETH balance deltas substitute for the bats bridge-service `/bridge/v1/claims` presence/absence check — faithful (arguably stronger), no shared helper edits.
- Single shared key-less receiver EOA for clean balance-delta assertions (`IsClaimed` keyed by `(depositCount, originNetwork)`, so destination is irrelevant).
- Go-side proof/root corruption mirrors the bats junk values verbatim.
- Live run deferred to P10b.

## Change-request count
0

## Changed files
New:
- `test/contracts/internalclaims/InternalClaims.sol`
- `test/contracts/internalclaims/Interfaces.sol`
- `test/contracts/internalclaims/internalclaims.go`
- `test/contracts/abi/internalclaims.abi`
- `test/contracts/bin/internalclaims.bin`
- `test/e2e/internal_claims_test.go`

Modified (additive only):
- `test/contracts/bind.sh` (appended `gen internalclaims`)
- `test/contracts/compile.sh` (appended `solc 0.8.20-alpine --via-ir` provenance block)

No production or shared-helper files touched.

## Commands run
- `abigen` (binding generation, from artifact-extracted `.abi`/`.bin`)
- `go build ./test/contracts/internalclaims/...`, `go build ./test/e2e/...`, `go vet ./test/e2e/...`, `golangci-lint run ./test/e2e/...` — all clean, run by both executor and validator.

## Blockers / notes for future steps
- None. The custom-contract-binding recipe is now proven twice (P5, P6).
- P7 (nightly ordering combinations) needs no new contract — it reuses the ERC20/message helpers plus ordering of bridge/claim.
- If a later step needs the asset-bridge or not-claimed helpers across multiple files, consider promoting `bridgeAssetL1ToL2GetParams` / `assertNotClaimed` to a shared helper file (out of P6 scope).
