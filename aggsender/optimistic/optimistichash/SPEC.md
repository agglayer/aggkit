# SPEC: aggsender/optimistic/optimistichash

## Summary

This directory computes two deterministic 32-byte hash digests used by the optimistic proof signing flow: (1) the digest over the three public values an optimistic signature commits to, and (2) the commitment digest over an ordered list of imported-bridge-exit claims. The commitment-of-imported-bridge-exits digest is then fed into (1) as one of its inputs. Both digests are protocol-level identifiers that are signed by the trusted signer and verified by on-chain / interop logic, so their byte layout and hashing algorithm are part of the wire contract, not implementation details.

The second digest is the Go implementation of the `ImportedBridgeExitCommitmentValues` commitment defined in the interop repository (`crates/unified-bridge/src/imported_bridge_exit.rs`); interop and this directory MUST agree byte-for-byte.

## Requirements

- **1.** Computing the optimistic signature digest MUST produce the keccak256 of the concatenation, in this order, of exactly three 32-byte values: the aggregation-proof public-values hash, the new local exit root, and the commit-imported-bridge-exits hash.
- **2.** The optimistic signature digest MUST be deterministic: identical input triples MUST yield identical outputs on every call and every host.
- **3.** Computing the commit-imported-bridge-exits digest MUST produce the keccak256 of the concatenation of one record per input claim, in the order the claims were supplied by the caller.
- **4.** Each per-claim record contributed to the commit-imported-bridge-exits digest MUST consist of exactly 64 bytes: the claim's global index encoded as a 32-byte little-endian unsigned integer, followed by the 32-byte bridge-exit hash derived from the claim.
- **5.** The 32-byte little-endian encoding of the global index MUST be left-aligned with least-significant byte first and zero-padded on the right; global indices whose magnitude does not fit in 32 bytes MUST be truncated to the 32 lowest-order bytes (input callers are responsible for supplying values that fit).
- **6.** The bridge-exit hash of a claim MUST be the hash of the `BridgeExit` value produced by the canonical conversion from a claim (the same conversion used elsewhere in aggsender / interop); two equal claims MUST produce equal bridge-exit hashes.
- **7.** Supplying an empty claim list to the commit-imported-bridge-exits digest MUST return the keccak256 of the empty byte string (no domain separator, no length prefix).
- **8.** Neither digest MUST apply any domain-separation tag, length prefix, framing, or padding beyond what Requirements 1, 3, and 4 describe; callers and verifiers rely on the exact concatenation.
- **9.** Reordering claims, inserting, duplicating, or omitting a claim MUST change the commit-imported-bridge-exits digest.
- **10.** A human-readable rendering of the optimistic signature data MUST expose the three input hashes as hex strings and MUST NOT be used as a signing input (it is for diagnostics only).

## Invariants

- **11.** For any fixed triple `(a, n, c)` of 32-byte values, the optimistic signature digest equals `keccak256(a || n || c)` where `||` denotes byte concatenation.
- **12.** For any ordered list of claims `[C_0, C_1, ..., C_{k-1}]`, the commit-imported-bridge-exits digest equals `keccak256( LE32(G_0) || H_0 || LE32(G_1) || H_1 || ... || LE32(G_{k-1}) || H_{k-1} )` where `LE32(G_i)` is the 32-byte little-endian encoding of claim `i`'s global index and `H_i` is claim `i`'s bridge-exit hash.
- **13.** The output of either digest is always exactly 32 bytes.

## External interface

- `OptimisticSignatureData` — struct with three 32-byte fields: `AggregationProofPublicValuesHash`, `NewLocalExitRoot`, `CommitImportedBridgeExits`. Exposes `Hash() common.Hash` (Requirement 1) and `String() string` (Requirement 10).
- `CalculateCommitImportedBrdigeExitsHashFromClaims(claims []claimsynctypes.Claim) common.Hash` — returns the digest defined by Requirements 3–7 for the given ordered claims.
- Compatibility reference: the commit-imported-bridge-exits digest MUST match the Rust `ImportedBridgeExitCommitmentValues` implementation in the interop repo (see file header citation); any change that alters its byte layout is a breaking protocol change.

## Out of scope

- Signature production, key management, and signature verification. This directory only produces the digests that are signed / verified elsewhere.
- Computing `AggregationProofPublicValuesHash` and `NewLocalExitRoot`; both are supplied by the caller.
- Validating claims. Malformed claims produce a digest; the caller is responsible for ensuring claims are well-formed before hashing.
