# SPEC: aggsender/query

## Summary

This package exposes the read-side data sources the aggsender uses to build, classify, and settle certificates. It groups a set of focused queriers, each covering one external concern: bridge and claim data from L2 syncers, L1 Info tree proofs and finalisation, Global Exit Root (GER) injections and removals, rollup Local Exit Root (LER) bootstrap, the aggchain FEP rollup contract (FEP start block / last settled L2 block / optimistic mode), aggregation proof public values fed to the prover, a multisig committee reader from the sovereign rollup contract, aggchain-proof assembly and dispatch to the aggkit-prover, certificate classification (PP vs FEP) and "last settled block" resolution across three independent settlement sources, and a one-shot setter that primes the L2 claim syncer's starting block from the latest settled certificate.

Queriers are stateless or near-stateless façades over contracts, RPC clients, and syncers. They do not own persistence; every method either reads from an upstream source or performs pure transformation on inputs. Error wrapping is the main cross-cutting contract: every failure crossing a querier boundary carries the originating operation and context.

The `SettledBlocks` value type is the unit of exchange between the certificate querier and its callers for multi-source settlement resolution: each of the three settlement sources (bridge exit block, imported bridge exit block, FEP L2 block) is carried with its own error so callers can decide per-source whether to fail, fall back, or reduce to a min/max.

## Requirements

### Aggchain FEP rollup querier (contract-backed)

- **1.** Construction of the aggchain FEP rollup querier MUST return a no-op implementation when either the configured contract address is the zero address or the configured aggsender mode is pessimistic-proof mode.
- **2.** Construction MUST fail if a non-zero contract address is provided and the contract binding cannot be instantiated, or if reading the contract's starting L2 block number fails.
- **3.** The no-op querier MUST report `IsFEP() == false`, `StartL2Block() == 0`, `GetLastSettledL2Block() == (0, nil)`, and MUST return a zero-valued aggregation-proof public values struct without error.
- **4.** The real querier MUST report `IsFEP() == true` and MUST return the contract-reported starting L2 block number (captured at construction) from `StartL2Block()`.
- **5.** `GetLastSettledL2Block` on the real querier MUST return the contract's latest block number, or a wrapped error if the contract call fails.

### Aggchain proof query (prover dispatch)

- **6.** Generating an aggchain proof MUST first obtain the finalized L1 Info tree leaf and merkle proof that prove the configured L1 info tree leaf count against the build-params-supplied L1 info tree root; failure to do so MUST abort the operation with a wrapped error.
- **7.** The query range for injected GERs proofs and removed GERs MUST be `[lastProvenBlock + 1, toBlock]` inclusive.
- **8.** The assembled request to the prover MUST include: last proven block, requested end block, finalized L1 info tree root hash, finalized L1 info tree leaf, merkle proof anchored at that root, injected GERs proofs for the range, imported bridge exits derived from certificate-build-params claims, removed GERs for the range, and converted unclaims.
- **9.** Imported bridge exits passed to the prover MUST carry the claim's `BlockNum` and `BlockPos`, and MUST NOT include claim data or claim-side merkle proofs.
- **10.** Unclaim conversion MUST decode each `GlobalIndex` big-int into its `{MainnetFlag, RollupIndex, LeafIndex}` components; a decoding failure for any unclaim MUST abort the operation with a wrapped error.
- **11.** If the certificate build params designates an optimistic certificate type, the proof request MUST be dispatched through the optimistic path; otherwise the standard prover path MUST be used.
- **12.** The optimistic path MUST: compute a new local exit root for the build params, sign the proof request together with that new LER and the certificate claims via the configured optimistic signer, write the signer-returned extra data back onto the certificate build params, and call the prover's optimistic endpoint with the signature.
- **13.** The optimistic path MUST refuse to proceed if the certificate build params reference is nil.
- **14.** Prover-side errors in either path MUST be surfaced to the caller wrapped with the mode (optimistic or not), last-proven-block, requested-end-block, and the stringified request.

### FEP inputs querier

- **15.** Getting aggchain parameters MUST combine the aggregation-proof public values for `(lastProvenBlock, requestedEndBlock, l1InfoTreeLeafHash)` with the FEP contract's current optimistic-mode flag, and MUST wrap errors from either source.

### Aggregation-proof public values querier

- **16.** Building aggregation-proof public values MUST query the op-node for the L2 output root at `lastProvenBlock` (as `L2PreRoot`) and at `requestedEndBlock` (as `ClaimRoot`); either call failing MUST wrap and abort.
- **17.** The public values MUST embed the currently selected op-succinct config's `RollupConfigHash`, `RangeVkeyCommitment` (as `MultiBlockVKey`), and `AggregationVkey` (as `AggregationVKeyHash`), read from the FEP contract.
- **18.** When the caller-supplied prover address is the zero address, the trusted signer in the emitted public values MUST be the first address returned by the FEP contract's `GetAggchainSigners`; when the list is empty, construction MUST fail with a sentinel "no signers" error.
- **19.** When the caller-supplied prover address is non-zero, it MUST be used as the trusted signer verbatim.
- **20.** The emitted `L2BlockNumber` field MUST equal the `requestedEndBlock` argument, and `L1Head` MUST equal the `l1InfoTreeLeafHash` argument.

### Bridge data querier

- **21.** `GetBridgesAndClaims` MUST return bridges from the L2 bridge syncer and claims from the L2 claim syncer for `[fromBlock, toBlock]`; either upstream error MUST be wrapped and returned.
- **22.** `GetLastProcessedBlock` MUST return the minimum of the bridge syncer's and (if configured) claim syncer's last processed block, and MUST report `found == false` if either upstream reports not found.
- **23.** `GetLastProcessedBlock` MUST return only the bridge syncer's value when no claim syncer is configured.
- **24.** `WaitForSyncerToCatchUp(block)` MUST block until both the bridge syncer and (if configured) the claim syncer have processed a block `>= block`, or until the context is cancelled, in which case it MUST return the context error.
- **25.** When the claim syncer is not configured, `WaitForSyncerToCatchUp` MUST treat the claim side as always caught up.
- **26.** `OriginNetwork()` MUST return the origin network id captured from the bridge syncer at construction.
- **27.** `GetUnsetClaimsForBlockRange` MUST delegate directly to the agglayer bridge L2 reader for the given range, returning its result unchanged.

### GER data querier

- **28.** `GetInjectedGERsProofs` MUST produce, for each injected GER in `[fromBlock, toBlock]`, a proof entry keyed by the GER hash and anchored against the supplied finalized L1 info tree root.
- **29.** For a removed injected GER, the returned entry MUST carry a placeholder L1 info tree leaf with `L1InfoTreeIndex = MaxUint32`, zeroed roots/hash/timestamp, and an empty proof; it MUST NOT query the L1 info tree syncer.
- **30.** For a non-removed injected GER, the returned entry MUST carry the leaf data and merkle proof resolved via the L1 info tree querier against the supplied root; a proof lookup failure MUST abort with a wrapped error.
- **31.** Each entry MUST carry the block number and block position of the injection event.
- **32.** `GetRemovedGERsForRange` MUST delegate to the chain GER reader for the given range; errors MUST be wrapped.

### L1 Info tree data querier

- **33.** Construction MUST fail if the configured target block finality is strictly less final than the L1 info tree syncer's configured finality (misconfiguration that would never be satisfiable).
- **34.** `GetTargetL1InfoRoot` MUST resolve the most recent L1 info tree leaf/root at or before the target L1 block number.
- **35.** The target L1 block number MUST equal the L1 node's block at the configured finality, capped at the L1 info tree syncer's last processed block if the syncer lags.
- **36.** If the L1 info tree syncer has processed no blocks, `GetTargetL1InfoRoot` (via `getTargetL1BlockNumber`) MUST return an error.
- **37.** If the L1 info tree syncer reports a block hash that disagrees with the L1 node's hash for the same block number and the reported hash is non-zero, `getTargetL1BlockNumber` MUST return an error indicating a possibly-unprocessed reorg; a zero syncer hash MUST be treated as an old pre-hash-tracking block and accepted.
- **38.** `GetL1InfoRootByLeafIndex` MUST return an error if the resolved root's hash is zero (no leaves).
- **39.** `GetFinalizedL1InfoTreeData` MUST compute the last leaf index as `finalizedL1InfoTreeLeafCount - 1` and return that leaf together with a merkle proof that anchors it to the supplied finalized root.
- **40.** `GetProofForGER` MUST return the L1 info tree leaf for the GER together with a merkle proof anchored at `rootFromWhichToProve`, and MUST verify that proof; if verification fails, the error MUST wrap a sentinel indicating the GER is not provable against the selected root.
- **41.** `GetInfoByIndex` MUST return a non-nil leaf or a wrapped error; a nil upstream result MUST be converted to an error.
- **42.** `IsGERFinalized` MUST return true iff the GER's L1 info tree index is less than or equal to `finalizedL1InfoLeafCount - 1`.
- **43.** `DoesGERExistsOnL1` MUST return true iff the L1 GER manager contract's index for the GER is strictly greater than zero.

### LER data querier (initial local exit root)

- **44.** `GetInitialLocalExitRoot` MUST read the rollup data at the configured L1 genesis block and return its `LastLocalExitRoot`.
- **45.** When `LastLocalExitRoot` is the zero hash, `GetInitialLocalExitRoot` MUST return the canonical empty-LER constant.
- **46.** Contract read failures MUST be wrapped.

### Multisig committee querier

- **47.** `GetMultisigCommittee(ctx, blockNum)` MUST read the committee threshold and signer infos from the sovereign rollup contract at the given block, MUST apply the configured URL override (if any) to signer URLs, and MUST return a committee constructed from the (possibly-remapped) infos and the threshold.
- **48.** If the contract-reported threshold does not fit in uint64, `GetMultisigCommittee` MUST fail.
- **49.** `ContractMode` MUST fail unless the contract's `CONSENSUSTYPE` equals the single supported consensus type (multi-ECDSA + SP1).
- **50.** `ContractMode` MUST return pessimistic-proof mode for aggchain type `{0,0}` (ECDSA-multisig) and aggchain-proof mode for aggchain type `{0,1}` (FEP); any other aggchain type MUST produce an error.
- **51.** `ResolveAutoMode` MUST return the configured mode unchanged for non-auto inputs, and MUST delegate to `ContractMode` when the input is auto.

### Certificate querier (PP vs FEP classification and settlement)

- **52.** `GetSettledBlocksFromCertHeader` MUST query all three settlement sources independently; a failure in one source MUST be recorded only in that source's error field, without short-circuiting the others.
- **53.** When the certificate header is nil, the per-certificate sources (bridge exit block, imported bridge exit block) MUST be skipped; only the FEP last-settled-L2-block source MUST be populated.
- **54.** When the agglayer network state reports a non-nil settled imported bridge exit, the returned `SettledBlocks` MUST carry that settled-IBE reference and MUST include the resolved block number (or the resolver's error) for it.
- **55.** Resolving a block number from a local exit root MUST return zero without error when the root equals the configured initial LER.
- **56.** Resolving a block number from a global index + bridge exit hash MUST return the block number of the matching claim whose converted imported-bridge-exit hash equals the supplied bridge exit hash; absence of a match MUST produce an error.
- **57.** `GetLastSettledCertificateToBlock` MUST reject a non-nil certificate whose status is not Settled.
- **58.** `GetLastSettledCertificateToBlock` MUST return the maximum of all three settlement-source blocks (via `SettledBlocks.LatestBlock`).
- **59.** The FEP last-settled-L2-block source MUST fall back to the FEP start L2 block when the FEP contract reports zero.
- **60.** `GetNewCertificateToBlock` MUST resolve the bridge exit block from the certificate's `NewLocalExitRoot`, the imported bridge exit block from the last imported bridge exit (when any), and MUST return the maximum of the two.
- **61.** `CalculateCertificateType` MUST return PP when the certificate's `AggchainData` is a signature or a multisig (without proof), and FEP when it is a proof or multisig-with-proof.
- **62.** When no `AggchainData` is set, `CalculateCertificateType` MUST fall back to block-based classification.
- **63.** Block-based classification MUST return PP when the current network is not FEP, or when it is FEP and the certificate's to-block is strictly less than the FEP start L2 block; otherwise FEP.

### Initial-block-for-claim-syncer setter

- **64.** Setting the claim syncer's next required block MUST be a no-op when the claim syncer is nil or has already processed at least one block.
- **65.** When no retry handler is supplied, the setter MUST use a retry handler with a 1-second base delay and infinite attempts.
- **66.** The computed next-required block MUST be the earliest of the three settlement sources derived from agglayer's latest settled certificate header (`SettledBlocks.EarliestBlock`).
- **67.** When the imported-bridge-exit block resolution fails but a settled imported bridge exit is present, the setter MUST retry via the claim syncer's RPC-based lookup by global index; absence of a claim for that global index MUST surface as an error.

### Cross-cutting

- **68.** Every error returned to a caller from any querier MUST be wrapped with operation context using Go's `%w` error-wrapping idiom.

## Invariants

- **69.** For any `SettledBlocks` returned by the certificate querier, if all three source errors are nil then `EarliestBlock() <= LatestBlock()` (with source-exclusion rules below).
- **70.** `SettledBlocks.EarliestBlock` MUST exclude `LastImportedBridgeExitBlock` from the minimum when `SettledImportedBridgeExit` is nil, and MUST exclude `LastSettledL2BlockNum` when it is zero.
- **71.** `SettledBlocks.EarliestBlock` and `SettledBlocks.LatestBlock` MUST return the first non-nil source error, if any, without computing the result.
- **72.** The no-op FEP querier is observationally a stand-in for "not a FEP network": `IsFEP() == false` and `StartL2Block() == 0` must both hold for it.

## External interface

- Constructors (package-exported, each returning an interface or a struct pointer implementing an interface defined in `aggsender/types`):
  - `NewAggchainFEPQuerier(logger, aggsenderMode, aggchainFEPAddr, l1Client)`
  - `NewAggchainProofQuery(log, aggchainProofClient, l1InfoTreeDataQuerier, optimisticSigner, lerQuerier, gerQuerier, bridgeQuerier)`
  - `NewFEPInputsQuery(aggchainFEPContract, aggchainFEPAddr, opNodeClient)`
  - `NewAggProofPublicValuesQuery(aggchainFEPContract, aggchainFEPAddr, opNodeClient, proverAddress)`
  - `NewBridgeDataQuerier(log, bridgeSyncer, claimSyncer, delayBetweenRetries, agglayerBridgeL2Reader)`
  - `NewCertificateQuerier(bridgeSyncer, l2ClaimSyncer, aggchainFEPQuerier, agglayerClient, initialLER)`
  - `NewGERDataQuerier(l1InfoTreeQuerier, chainGERReader)`
  - `NewL1InfoTreeDataQuerier(l1Client, l1GERAddr, l1InfoTreeSyncer, blockFinalityForL1InfoTree)`
  - `NewLERDataQuerier(l1GenesisBlock, rollupDataQuerier)`
  - `NewBaseMultisigCommitteeQuery(sovereignRollupAddr, l1Client, overrideURL)`
  - `NewSetInitialBlockToClaimSyncer(certQuerier, agglayerClient, l2OriginNetwork, logger)`
  - `GetTrustedSignerAddr(aggchainFEPContract)` — exported helper.
- Exported types: `AggProofPublicValuesQuery`, `FEPInputsQuery`, `L1InfoTreeDataQuerier`, `BaseMultisigCommitteeQuery`, `CommitteeOverride`, `SetInitialBlockToClaimSyncer`.
- Exported sentinel errors: `ErrNoProofBuiltYet` (gRPC `Unavailable`), `ErrGERNotProvableAgainstRoot`.
- The interfaces these constructors satisfy (behavioral contract) live in `aggsender/types`: `AggchainFEPRollupQuerier`, `AggchainProofQuerier`, `FEPInputsQuerier`, `AggProofPublicValuesQuerier`, `BridgeQuerier`, `CertificateQuerier`, `GERQuerier`, `L1InfoTreeDataQuerier`, `LERQuerier`, `MultisigQuerier`.

## Error modes

- **73.** A construction-time misconfiguration (e.g. invalid block finality, unreachable contract, empty signer list when needed) MUST fail closed — the caller MUST NOT receive a partially-initialised querier.
- **74.** `ErrGERNotProvableAgainstRoot` MUST be returned (wrapped) iff a GER's merkle proof verification against a user-supplied root fails — independent of other lookup failures.
- **75.** `ErrNoProofBuiltYet` encodes the prover-unavailable state for callers that distinguish "no proof yet" from hard failures; it MUST NOT be used for ordinary errors.

## Out of scope

- Writing to any upstream. No querier mutates contract state, syncer state, or the agglayer.
- Certificate construction, signing, submission. This package only classifies certificates and assembles request payloads for a prover; the orchestration lives in the parent `aggsender` flow.
- Caching. Queriers are thin passes-through; if callers need caching they layer it above.
- Reorg recovery. Queriers report a reorg-shaped error when detected (e.g. invariant #37) but do not resolve it.
