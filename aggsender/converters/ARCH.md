# ARCH: aggsender/converters

## Overview

Three files, each a family of pure conversion functions over a different input type:

- `bridge_exit_converter.go` — `ConvertToBridgeExit` / `ConvertToBridgeExits` map `bridgesync.Bridge` to `agglayertypes.BridgeExit`. A private helper `convertBridgeMetadata` wraps `crypto.Keccak256` and is shared with the claim path. Upholds SPEC #1–#5, #28.
- `imported_bridge_exit_converter.go` — `ConvertBridgeExitFromClaim`, `ConvertToImportedBridgeExitWithoutClaimData` / `…s`, and `ConvertToImportedBridgeExit` / `…s` map `claimsynctypes.Claim` into `agglayertypes.BridgeExit` / `ImportedBridgeExit`. The with-claim-data path calls `types.L1InfoTreeDataQuerier.GetProofForGER` and `tree.CalculateRoot`; the mainnet-vs-rollup branch chooses between `ClaimFromMainnet` and `ClaimFromRollup`. Upholds SPEC #6–#20, #29–#31.
- `cert_header_converter.go` — `ConvertAgglayerCertHeaderToAggsender` and `ConvertAggsenderCertHeaderToAgglayer` translate `CertificateHeader` in both directions. Upholds SPEC #21–#27.

All exported functions are stateless and safe for concurrent use; the only runtime dependency injected into the package is the `L1InfoTreeDataQuerier`, passed per call.

## Patterns

- **1.** Conversion helpers MUST stay pure — any new dependency that requires I/O or state SHOULD be passed as a per-call parameter (as `L1InfoTreeDataQuerier` is today), not held in a package-level variable or a receiver.
- **2.** Metadata hashing for both `Bridge` and `Claim` paths MUST go through the shared `convertBridgeMetadata` helper so the `Keccak256(nil)` behavior for empty metadata stays consistent across both conversion families (SPEC #3, #8, #28).
- **3.** Batch converters (`ConvertToBridgeExits`, `ConvertToImportedBridgeExits`, `ConvertToImportedBridgeExitsWithoutClaimData`) SHOULD delegate element-wise to their single-item counterpart and preserve order; do not inline divergent logic in the batch path.

## Notable decisions

- **4.** Empty bridge/claim metadata is deliberately hashed rather than short-circuited to zero. The agglayer contract expects `Keccak256(nil)` as the canonical "no metadata" marker, and any refactor that treats empty input as a special case (returning nil, zero bytes, or a sentinel) would silently break SPEC #3 / #28 without a type-level signal.
- **5.** The `ClaimFromRollup` branch computes `ProofLeafLER.Root` via `tree.CalculateRoot(bridgeExit.Hash(), claim.ProofLocalExitRoot, ibe.GlobalIndex.LeafIndex)` rather than trusting a value supplied by the claim. This is a defensive recomputation — if a refactor replaces it with `claim.LocalExitRoot` or similar, the output will silently diverge from the mainnet branch's convention and SPEC #17 no longer holds.
- **6.** `ConvertAgglayerCertHeaderToAggsender` returns `(nil, nil)` for a nil input (and the reverse returns `nil` alone), treating nil as a pass-through rather than an error. Callers rely on this — converting a nil header is not an exceptional condition; it's how "no header" flows through the pipeline.
- **7.** `ConvertAggsenderCertHeaderToAgglayer` forces `Metadata = common.ZeroHash`. Metadata is deprecated on the agglayer side; a future resurrection of the field MUST update SPEC #26 before this line, because the zero-hash invariant is now relied on by downstream readers.
- **8.** `ConvertAgglayerCertHeaderToAggsender` has a `TODO: ??` for `RetryCount` and sets the time and block fields to zero. The SPEC documents these as reconstructed elsewhere (SPEC #23); anyone tempted to fill them in here must first check whether the source header actually carries that information (it does not).
