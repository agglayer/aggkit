# SPEC: aggsender/converters

## Summary

Converters translate between the aggsender's internal / upstream domain types (`bridgesync.Bridge`, `claimsynctypes.Claim`, `aggsender/types.CertificateHeader`) and the agglayer wire types (`agglayertypes.BridgeExit`, `agglayertypes.ImportedBridgeExit`, `agglayertypes.CertificateHeader`). The package is stateless: every exported function is a pure-ish mapping plus, in one case, a lookup of L1 info tree proofs through an injected querier.

The directory has no persistence, no goroutines, and no caller-visible ordering concerns. Its entire contract is the shape of the output for a given input.

## Requirements

### Bridge exit conversion (from `bridgesync.Bridge`)

- **1.** A bridge-to-bridge-exit conversion MUST produce an output whose `LeafType`, origin network, origin token address, destination network, destination address, and amount equal the corresponding fields of the input bridge.
- **2.** A bridge-to-bridge-exit conversion MUST set the output `Metadata` to the Keccak-256 hash of the input bridge's raw metadata bytes.
- **3.** When the input metadata byte slice is empty or nil, the output `Metadata` MUST equal `Keccak256(nil) = 0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470` (i.e., conversion MUST NOT short-circuit empty metadata to an all-zero value).
- **4.** Batch conversion of a slice of bridges MUST produce an output slice of the same length whose `i`-th element equals the single-bridge conversion of the `i`-th input, preserving order.
- **5.** Batch conversion MUST accept an empty or nil input slice and return a non-nil, zero-length output slice.

### Bridge exit conversion (from `claimsynctypes.Claim`)

- **6.** A claim-to-bridge-exit conversion MUST set `LeafType` to the asset leaf type when the claim is not a message, and to the message leaf type when the claim's `IsMessage` field is true.
- **7.** A claim-to-bridge-exit conversion MUST set the origin network, origin token address, destination network, destination address, and amount to the corresponding fields of the input claim.
- **8.** A claim-to-bridge-exit conversion MUST set `Metadata` to the Keccak-256 hash of the claim's raw metadata bytes (same rule as #2–#3).

### Imported bridge exit conversion — without claim data

- **9.** Converting a claim to an imported bridge exit without claim data MUST embed a bridge exit equal to the claim-to-bridge-exit conversion of the same claim (per #6–#8).
- **10.** The resulting `GlobalIndex` MUST be populated by decoding the claim's `GlobalIndex` big-integer into `(MainnetFlag, RollupIndex, LeafIndex)` using the canonical bridgesync decoding.
- **11.** If the claim's `GlobalIndex` cannot be decoded, conversion MUST return a wrapped error and MUST NOT return a partially-populated imported bridge exit.
- **12.** Claim data MUST NOT be populated by the without-claim-data conversion path.

### Imported bridge exit conversion — with claim data

- **13.** Converting a claim to an imported bridge exit with claim data MUST populate the bridge exit and global index fields according to #9–#10.
- **14.** Conversion with claim data MUST obtain an L1 info tree leaf and a Merkle proof from the claim's global exit root to the supplied root-from-which-to-prove, by invoking the injected L1-info-tree querier exactly once per claim.
- **15.** If the L1-info-tree querier returns an error, conversion MUST return a wrapped error that identifies the global exit root and the target root, and MUST NOT return a partially-populated imported bridge exit.
- **16.** When the decoded global index has `MainnetFlag == true`, the claim data MUST be of the mainnet variant and MUST contain:
  - an L1 leaf carrying the querier's `L1InfoTreeIndex`, the claim's `RollupExitRoot`, the claim's `MainnetExitRoot`, and an inner leaf carrying the querier's `GlobalExitRoot`, `Timestamp`, and `PreviousBlockHash` (as `BlockHash`);
  - a Merkle proof from the bridge exit to the mainnet exit root, with `Root` equal to the claim's `MainnetExitRoot` and `Proof` equal to the claim's local-exit-root proof;
  - a Merkle proof from the global exit root to the L1 root, with `Root` equal to `rootFromWhichToProve` and `Proof` equal to the proof returned by the querier.
- **17.** When the decoded global index has `MainnetFlag == false`, the claim data MUST be of the rollup variant and MUST contain:
  - an L1 leaf populated identically to #16;
  - a proof from the bridge exit to the local exit root (`ProofLeafLER`), whose `Root` equals the Merkle root computed from the bridge exit's hash, the claim's local-exit-root proof, and the decoded `LeafIndex`, and whose `Proof` equals the claim's local-exit-root proof;
  - a proof from the local exit root to the rollup exit root (`ProofLERToRER`), with `Root` equal to the claim's `RollupExitRoot` and `Proof` equal to the claim's rollup-exit-root proof;
  - a proof from the global exit root to the L1 root (`ProofGERToL1Root`), with `Root` equal to `rootFromWhichToProve` and `Proof` equal to the proof returned by the querier.
- **18.** Batch conversion of a slice of claims (with or without claim data) MUST produce an output slice of the same length whose `i`-th element equals the single-claim conversion of the `i`-th input, preserving order.
- **19.** Batch conversion MUST accept an empty or nil input slice and return a non-nil, zero-length output slice.
- **20.** If any claim in a batch fails to convert, batch conversion MUST return a wrapped error and MUST NOT return a partial result slice.

### Certificate header conversion

- **21.** Converting an agglayer certificate header to an aggsender certificate header MUST copy `Height`, `CertificateID`, `PreviousLocalExitRoot`, `NewLocalExitRoot`, and `Status` verbatim from the input to the output.
- **22.** The resulting aggsender certificate header MUST have `CertSource` set to the `AggLayer` source marker, marking the header as originating from the agglayer (not locally generated).
- **23.** The resulting aggsender certificate header MUST have `RetryCount`, `FromBlock`, `ToBlock`, `CreatedAt`, and `UpdatedAt` set to zero, and `FinalizedL1InfoTreeRoot` set to nil, because these values are not recoverable from an agglayer header and MUST be reconstructed by the caller.
- **24.** Converting a nil agglayer certificate header MUST return a nil output and a nil error.
- **25.** Converting an aggsender certificate header to an agglayer certificate header MUST copy `Height`, `CertificateID`, `PreviousLocalExitRoot`, `NewLocalExitRoot`, and `Status` verbatim, and MUST set `NetworkID` to the supplied network id.
- **26.** The resulting agglayer certificate header's `Metadata` MUST be the zero hash (metadata is deprecated and forced to zero).
- **27.** Converting a nil aggsender certificate header MUST return a nil output.

## Invariants

- **28.** For any bridge `b` with empty or nil metadata, the bridge-to-bridge-exit conversion's `Metadata` field equals `Keccak256(nil)` — never an all-zero or nil slice.
- **29.** For any two claims that differ only in `IsMessage`, the resulting bridge exits differ only in `LeafType`.
- **30.** For any claim `c`, the bridge exit embedded in an imported-bridge-exit conversion of `c` equals the direct claim-to-bridge-exit conversion of `c`, field for field.

## Error modes

- **31.** Errors from global-index decoding and from the L1-info-tree querier MUST be surfaced to the caller with `%w` wrapping so the original error is retrievable via `errors.Is` / `errors.As`.

## Out of scope

- Certificate header conversion does not populate `RetryCount`, `FromBlock`, `ToBlock`, `CreatedAt`, `UpdatedAt`, or `FinalizedL1InfoTreeRoot`; callers that need these fields MUST compute them elsewhere.
- This package does not validate any input field (e.g., it does not check that `Amount` is non-negative, that addresses are well-formed, or that metadata is canonical).
- This package does not perform any network or storage I/O directly; the only external call is the injected L1-info-tree querier.
