# ARCH: aggsender/optimistic/optimistichash

## Overview

Two small, independent hashers. `OptimisticSignatureData.Hash` concatenates three 32-byte fields and keccak256s them (upholds SPEC #1, #2, #11, #13). `CalculateCommitImportedBrdigeExitsHashFromClaims` builds an intermediate `optimisticCommitImportedBrigesData` — one entry per claim with the claim's `GlobalIndex` and the bridge-exit hash obtained by delegating to `converters.ConvertBridgeExitFromClaim(...).Hash()` — then keccak256s the concatenation of `(LE32(globalIndex) || bridgeExitHash)` records (upholds SPEC #3–#7, #9, #12, #13).

Keccak256 is go-ethereum's `crypto.Keccak256Hash`. The 32-byte little-endian encoding of the global index uses `aggkitcommon.BigIntToLittleEndianBytes`, which caps output at 32 bytes (upholds SPEC #5). Claim-to-BridgeExit conversion is delegated to `aggsender/converters` so the bridge-exit hash definition stays in a single place shared with the rest of aggsender and the interop Rust crate.

## Notable decisions

- **1.** Global indices are encoded little-endian, not big-endian. This is non-obvious for a Solidity/EVM-adjacent codebase that defaults to big-endian; the encoding is fixed by the interop Rust reference (`ImportedBridgeExitCommitmentValues`) cited in the source file, and changing it would silently desync Go signers from Rust verifiers. Any refactor that reaches for `common.BigToHash` or `.FillBytes` must be rejected.
- **2.** The commit digest has no length prefix, no domain separator, and no per-record separator. An empty claim list hashes to `keccak256("")`. This matches the Rust reference exactly; adding framing "for safety" is a breaking protocol change.
- **3.** Bridge-exit hashing is delegated to `converters.ConvertBridgeExitFromClaim(...).Hash()` rather than reimplemented here, so this package stays free of bridge-exit schema knowledge and cannot drift from the canonical definition.
- **4.** `String()` on `OptimisticSignatureData` is diagnostic only and deliberately renders the already-computed input hashes rather than re-hashing; it MUST NOT be wired into any signing path.
