## Summary

Shared types and interfaces for the `aggsender` subsystem. The package defines the data shapes exchanged across `aggsender` components (flows, validators, queriers, triggers, the status/health surface) and the abstractions those components implement so they can be composed, mocked, and persisted. It carries no orchestration logic of its own; its contracts are the shape and invariants of the values that flow through the rest of the subsystem.

The central domain concepts are: certificates and the parameters used to build them (`CertificateHeader`, `Certificate`, `CertificateBuildParams`, `CertificatePreBuildParams`), the proof artefacts a flow produces (`AggchainProof`, `SP1StarkProof`, `AggregationProofPublicValues`, `AggchainParams`), the operating modes and kinds of certificates (`AggsenderMode`, `CertificateType`, `CertificateSource`, `CertificateSendTriggerMode`), the multisig committee abstraction (`MultisigCommittee`, `SignerInfo`, `ValidationRequest`), and the liveness/status surface (`AggsenderStatus`, `AggsenderStatusType`, `AggsenderInfo`, `HealthCheckResponse`, `CertStatus`, `SettledBlocks`).

## Requirements

- **1.** `AggsenderMode` MUST be one of `PessimisticProof`, `AggchainProof`, `PreconfPP`, `Auto`; construction from an arbitrary string MUST succeed case-insensitively for those four values and MUST fail for anything else.
- **2.** `AggsenderMode` values MUST round-trip through a string form that is accepted by both database deserialisation and programmatic construction, so a persisted mode can be read back without loss.
- **3.** `CertificateType` MUST have a total mapping between its integer form (`0..4`) and a canonical string form (`""`, `"pp"`, `"fep"`, `"optimistic"`, `"preconf_pp"`); both directions MUST be supported, with the empty/zero value representing "unknown".
- **4.** Deserialising an unknown `CertificateType` string MUST produce a typed error and MUST NOT silently coerce to a real type.
- **5.** `CertificateSendTriggerMode` MUST be one of `NewBridge`, `EpochBased`, `ASAP`, `Auto`; validation MUST reject any other value.
- **6.** `HealthCheckResponse` is healthy iff its `Status` equals the `"OK"` sentinel; a nil response MUST NOT be reported as healthy.

### Multisig committee

- **7.** Constructing a multisig committee MUST fail if the signer set is empty.
- **8.** Constructing a multisig committee MUST fail if the signature threshold is zero.
- **9.** Constructing a multisig committee MUST fail if the committee size is smaller than the threshold.
- **10.** Adding a signer whose address already belongs to the committee MUST fail; each committee address is unique.
- **11.** Membership lookup by address MUST be answerable in constant time.
- **12.** Returning the committee's signer list to callers MUST NOT expose the internal slice; external mutation of the returned value MUST NOT affect the committee.

### Certificate build parameters

- **13.** A `CertificateBuildParams` is "empty" iff it has zero bridges and zero claims (unclaims alone do not make it non-empty).
- **14.** A `CertificateBuildParams` is "a retry" iff its retry count is greater than zero AND a previous sent certificate header is attached.
- **15.** The block count of a `CertificateBuildParams` MUST equal `ToBlock - FromBlock + 1`, saturated at `math.MaxInt` when the arithmetic would overflow a Go `int`.
- **16.** The max deposit count of a `CertificateBuildParams` MUST be the deposit count of the last bridge in the `Bridges` slice, or zero when no bridges are present.
- **17.** Filtering claims by unclaims MUST remove at most one claim per unclaim, matching on equal non-nil `GlobalIndex`; each unclaim entry MUST consume at most one matching claim.
- **18.** Filtering claims MUST NOT remove a claim whose `GlobalIndex` is nil, and MUST NOT match against an unclaim whose `GlobalIndex` is nil.
- **19.** When the unclaim list is empty, the filtered result MUST be a fresh slice with the same contents and order as the original claims.
- **20.** `EstimatedSize` MUST account for every bridge and every claim, and MUST add an aggchain-data contribution that depends on certificate type: for `CertificateTypeFEP` the contribution MUST grow with the number of claims; for every other type it MUST be a fixed signature-sized constant.

### Proof public values

- **21.** `AggregationProofPublicValues.Hash` MUST be the SHA-256 of the ABI-encoded tuple `(bytes32 L1Head, bytes32 L2PreRoot, bytes32 ClaimRoot, uint64 L2BlockNumber, bytes32 RollupConfigHash, bytes32 MultiBlockVKey, address TrustedSigner)`; the `AggregationVKeyHash` field MUST NOT be included in the hash pre-image.
- **22.** `AggchainParams.Hash` MUST be the Keccak-256 of the packed encoding, in order, of: `L2PreRoot` (32B), `ClaimRoot` (32B), `L2BlockNumber` as big-endian 32B, `RollupConfigHash` (32B), `OptimisticMode` as a single `0x00`/`0x01` byte, `TrustedSigner` (20B), `MultiBlockVKey` (32B), `AggregationVKeyHash` (32B).
- **23.** Both hash functions MUST be deterministic: equal inputs MUST produce equal outputs, and any change to a byte of any included field MUST change the output.

### Settled blocks

- **24.** `SettledBlocks.EarliestBlock` MUST return the first non-nil source error when any source errored, and MUST NOT return a partial minimum in that case.
- **25.** `SettledBlocks.EarliestBlock` MUST exclude `LastImportedBridgeExitBlock` from the minimum when `SettledImportedBridgeExit` is nil (no IBE was settled).
- **26.** `SettledBlocks.EarliestBlock` MUST exclude `LastSettledL2BlockNum` from the minimum when it equals zero (no FEP data resolved).
- **27.** `SettledBlocks.LatestBlock` MUST return the first non-nil source error when any source errored, and otherwise MUST return the maximum of the three block fields unconditionally.

## External interface

This package's exported names, shapes, and interfaces are the contract for every other package under `aggsender/`. Consumers depend on:

- **Mode / kind enumerations** — `AggsenderMode` (+ `NewAggsenderMode`, `Validate`, `String`, `Scan`), `CertificateType` (+ `NewCertificateTypeFromInt`, `NewCertificateTypeFromStr`, `String`, `ToInt`, `Value`, `Scan`), `CertificateSource`, `CertificateSendTriggerMode` (+ `Validate`, `String`), `AggsenderStatusType`.
- **Core value types** — `CertificateHeader` (and its `meddler` DB tags), `Certificate`, `AggchainProof`, `SP1StarkProof`, `AggregationProofPublicValues`, `AggchainParams`, `CertificateBuildParams`, `CertificatePreBuildParams`, `CertificateL1InfoTreeData`, `BlockRangeAdjustmentOptions`, `SignerInfo`, `MultisigCommittee`, `ValidationRequest`, `VerifyIncomingRequest`, `AggchainProofRequest`, `AggsenderStatus`, `AggsenderInfo`, `HealthCheckResponse`, `HealthCheckStatus`, `CertStatus`, `SettledBlocks`.
- **Constructors** — `NewSignerInfo`, `NewMultisigCommittee`, `NewAggchainProofRequest`, `NewAggsenderMode`, `NewCertificateTypeFromInt`, `NewCertificateTypeFromStr`.
- **Sentinel strings** — `NilStr = "nil"`, `NAStr = "N/A"`, `HealthCheckStatusOK = "OK"`.
- **Interfaces implemented elsewhere and consumed here** (behavioural contracts in the interface names rather than bodies): `AggsenderBuilderFlow`, `AggsenderVerifierFlow`, `AggsenderFlowBaser`, `L1InfoTreeSyncer`, `L2BridgeSyncer`, `BridgeQuerier`, `ChainGERReader`, `AgglayerBridgeL2Reader`, `L1InfoTreeDataQuerier`, `GERQuerier`, `Logger`, `CertificateStatusChecker`, `RollupDataQuerier`, `LERQuerier`, `CertificateValidator`, `CertificateValidateAndSigner`, `ValidatorClient`, `LocalExitRootQuery`, `AggchainProofQuerier`, `MultisigContract`, `MultisigQuerier`, `ValidatorPoller`, `AggchainFEPRollupQuerier`, `CertificateQuerier`, `FEPContractQuerier`, `OpNodeClienter`, `AggProofPublicValuesQuerier`, `FEPInputsQuerier`, `CertificateTriggerEvent`, `CertificateSendTrigger`, `InitialBlockClaimSyncerSetter`, `OptimisticModeQuerier`, `OptimisticSigner`, `AggchainProofClientInterface`, `EmitLogFunc`.
- **DB schema coupling** — `CertificateHeader` and `Certificate` carry `meddler` tags naming DB columns (`height`, `retry_count`, `certificate_id`, `previous_local_exit_root`, `new_local_exit_root`, `from_block`, `to_block`, `status`, `created_at`, `updated_at`, `finalized_l1_info_tree_root`, `l1_info_tree_leaf_count`, `cert_type`, `cert_source`, `signed_certificate`, `aggchain_proof`, `extra_data`). Renaming these tags or changing their types is a storage-format break.

## Invariants

- **28.** For any `mode` accepted by `NewAggsenderMode`, `NewAggsenderMode(mode.String())` MUST return `mode` with no error.
- **29.** For any `CertificateType` `t` built via `NewCertificateTypeFromStr(s)`, `t.String()` MUST equal the canonical `s` (i.e. the round-trip is stable).
- **30.** For any `MultisigCommittee` `c` and address `a`, `c.IsMember(a)` is true iff some element of `c.Signers()` has `Address == a`.
- **31.** For any `CertificateBuildParams` `p`, `p.GetClaimsFilteringUnclaims()` MUST return a slice whose length equals `len(p.Claims) - k`, where `k` is the number of `(claim, unclaim)` pairs with equal non-nil `GlobalIndex` matched under the at-most-one-to-one rule of #17.

## Error modes

- **32.** String-based `Scan` methods on `AggsenderMode` and `CertificateType` MUST reject non-string database values with a typed error that identifies the incoming Go type, so storage-layer corruption is observable at the boundary rather than silently defaulted.
- **33.** `String()` methods on pointer receivers defined in this package MUST tolerate nil receivers and return the `NilStr` sentinel or an equivalent marker rather than panicking.

## Out of scope

- No persistence logic. DB tags on `CertificateHeader` / `Certificate` describe column mappings; reading, writing, and migrations live in `aggsender/db`.
- No network I/O. Interfaces that appear to do I/O (syncers, queriers, RPC clients, validators) are only declarations; concrete implementations live outside this package.
- No orchestration, scheduling, or retry logic. The types carry enough state (e.g. `RetryCount`, `CreatedAt`, `LastSentCertificate`) for callers to implement those policies; this package does not implement them.
- No cryptographic signing. `AggregationProofPublicValues.Hash` and `AggchainParams.Hash` compute digests but do not sign; signer construction lives behind the `OptimisticSigner` / `signertypes.Signer` interfaces.
