# SPEC: aggsender/flows

## Summary

Flows encapsulate the mode-specific logic for turning L2 bridge/claim activity into an agglayer certificate, plus the mirror logic that validates an incoming certificate. Two prover modes exist (pessimistic proof — PP, and aggchain/FEP prover), each expressed as a *builder* flow (used by the proposer to assemble a certificate and select its block range) and a *verifier* flow (used by the validator to re-check a certificate). Both modes share a common base that handles block-range selection, bridge/claim fetching, size/retry/GER adjustments, and certificate assembly; the prover-specific code layers additional rules on top.

Flows are stateless per call: each `GetCertificateBuildParams` resolves "what is the next certificate?" from storage + queriers every time. A flow is selected at construction time via a factory keyed on `cfg.Mode`; changing modes at runtime is not supported.

## Requirements

- **1.** Exactly one flow implementation MUST be returned by the builder factory for each supported mode; unsupported modes MUST be rejected with an error rather than silently defaulting.
- **2.** Every builder flow MUST, given the current persisted certificate state and L2/L1 sync state, either return one build-params value describing the next certificate, or return nil with no error to signal "nothing to send right now", or return an error.
- **3.** The block range chosen for the next certificate MUST start at one block after the highest block already covered by a closed (settled) certificate, or at the configured starting L2 block when no prior certificate exists.
- **4.** When the most recent persisted certificate is in an error state, a retry certificate MUST reuse the same `FromBlock` as that errored certificate (i.e. the same starting boundary). Build-parameter verification MUST reject any retry whose `FromBlock` does not match.
- **5.** If the highest block covered by the last settled certificate is already ≥ the highest L2 block available, no certificate MUST be produced and the caller MUST be told so without raising an error condition.
- **6.** For a settled previous certificate, when a canonical settled-boundary source is configured, the `FromBlock` MUST be derived from that source rather than solely trusting the locally stored `ToBlock`, and a mismatch between derived and stored boundaries MUST be logged.
- **7.** A built certificate MUST carry the originating L2 network ID, the previous local exit root, the new local exit root, the bridge exits for the range, the imported bridge exits for the range, a height one greater than the previous closed certificate's height (or zero when there is none), and the L1 info tree leaf count for the root being proven against.
- **8.** If the previous certificate is not closed, a new certificate MUST NOT be built; the flow MUST surface an error that names the offending status.
- **9.** If the previous certificate is settled, the new certificate's height MUST be previous-height+1 and its previous local exit root MUST equal the previous certificate's new local exit root.
- **10.** If the previous certificate is in error and a prior settled certificate exists, the retry's height MUST equal the errored certificate's height and its previous local exit root MUST equal the prior settled certificate's new local exit root.
- **11.** If no certificate has ever been sent, the first certificate's height MUST be 0 and its previous local exit root MUST be the configured initial LER.
- **12.** When the set of bridges in a certificate is non-empty, its new local exit root MUST equal the exit root reported by the L2 bridge source at the certificate's maximum deposit count; when empty, the new local exit root MUST equal the previous local exit root.
- **13.** Every claim included in a certificate MUST have its global exit root equal to the GER derived from its mainnet-exit-root and rollup-exit-root pair; verification MUST fail otherwise.
- **14.** Claims whose GER cannot be proven against the L1 info tree root chosen for the certificate MUST NOT be silently included; when such a claim exists on L1 but cannot be proven against the chosen root, certificate generation MUST fail.
- **15.** A claim whose GER is not present on L1 MAY remain in the certificate if a matching *unclaim* (same global index) exists at a strictly later event position in the same block range; otherwise the certificate's `ToBlock` MUST be trimmed to the block preceding that claim, or generation MUST fail if trimming would move before `FromBlock`.
- **16.** When a claim and an unclaim in the same range share a global index, exactly one unclaim MUST be consumed to cancel exactly one claim before the remaining claims become imported bridge exits. Ordering of matching is deterministic: earliest claim matches earliest still-unused unclaim.
- **17.** A configured maximum L2 block number MUST cap the certificate's `ToBlock`. When the certificate's starting block is already beyond that maximum, or when the current next range exactly follows the maximum, the flow MUST report "nothing more to send" rather than producing a certificate.
- **18.** Retry certificates MUST NOT be resized by the max-L2-block cap unless the flow explicitly permits it; when resizing is disallowed the flow MUST surface an error.
- **19.** A configured max certificate size MUST cap the estimated size of a certificate by reducing `ToBlock` one block at a time; when reduced to a single block the flow MUST accept the oversize and log a warning rather than fail. A zero max MUST be interpreted as no limit.
- **20.** When the L1 info tree root a certificate is to be proven against is required to be finalized, the flow MUST reject the certificate if that root's leaf index exceeds the currently finalized root's leaf index, or if the root does not match the leaf count's canonical root.
- **21.** When the flow is configured to require at least one bridge exit per certificate, a certificate with zero bridge exits MUST NOT be produced, and a reduction that leaves zero bridges but non-zero imported bridge exits MUST fail rather than truncating further.
- **22.** The aggchain/FEP flow MUST choose the certificate type from optimistic-mode state: `Optimistic` when optimistic mode is on, `FEP` otherwise.
- **23.** The aggchain/FEP flow, when the last sent certificate is in error *and* of the currently-targeted certificate type, MUST rebuild build-params over the same block range, bumping retry count by one, and MUST reuse the same L1 info tree root chosen originally.
- **24.** The aggchain/FEP flow MUST request an aggchain proof from the prover for the chosen range; if the prover reports "no proof built yet", the flow MUST return nil with no error.
- **25.** The aggchain/FEP flow MUST align the certificate's `ToBlock` to the end block reported by the prover when that end block differs from the requested end, and MUST reject any post-alignment range adjustment (i.e. the final range must be stable).
- **26.** The aggchain/FEP flow MAY reuse a previously cached retry proof only when the retry's range is unchanged, the cached proof's `LastProvenBlock` equals the expected last proven block, and the cached proof's `EndBlock` equals the retry's `ToBlock`; otherwise the prover MUST be called again.
- **27.** The aggchain/FEP flow's startup check MUST fail if, between its configured starting L2 block and the last sent certificate's range, there are bridges or claims that would not be covered by any certificate.
- **28.** The PP flow's startup check MUST succeed unconditionally (it has no mode-specific invariant to validate).
- **29.** Every built certificate that carries a non-nil multisig MUST have its aggchain-data field updated to reflect that multisig; for the PP flow this replaces the aggchain-data with a multisig descriptor, and for the aggchain/FEP flow it wraps the existing proof together with the multisig. A nil multisig MUST leave the certificate's aggchain-data unchanged.
- **30.** Verification of an aggchain/FEP certificate MUST reject the certificate if its `AggchainData` is nil, of an unknown type, or if the aggchain params hash derived from L1/L2 state (using the last settled block, the certificate's last block, and the L1 info leaf at the certificate's leaf count) does not equal the hash carried by the certificate.
- **31.** Verification of a PP certificate in this layer imposes no mode-specific checks beyond the shared base checks applied during build-params verification; multisig validation happens elsewhere.

## Invariants

- **32.** For any two successive closed certificates A (settled) and B, `B.height == A.height + 1` and `B.PrevLocalExitRoot == A.NewLocalExitRoot`.
- **33.** A certificate's final claim set MUST satisfy: for every claim in the certificate, either its GER is provable against the chosen L1 info tree root, or its GER is not present on L1 *and* a matching posterior unclaim exists in the certificate's block range.
- **34.** A retry certificate's `FromBlock` equals the errored certificate's `FromBlock`.
- **35.** A certificate's `NewLocalExitRoot` equals `PrevLocalExitRoot` iff the certificate has no bridge exits.

## External interface

The package exposes two factory entry points and two families of flow types.

Factories:
- Builder factory: given a mode-keyed config plus shared dependencies (storage, L1/L2 clients, syncers, queriers, initial LER), returns a builder flow whose concrete type is determined by the mode, or an error for unsupported modes.
- Verifier factory: given a validator-mode config plus shared dependencies, returns a verifier flow plus the set of shared components it was constructed from; a separate "local verifier" constructor wraps an already-constructed builder flow for in-process verification.

Builder flow contract (identical across modes):
- `CheckInitialStatus(ctx) error` — startup sanity check; PP is a no-op, aggchain/FEP checks bridge-gap invariants against the starting L2 block.
- `GetCertificateBuildParams(ctx) (*BuildParams, error)` — the main driver, per requirement 2.
- `GenerateBuildParams(ctx, *PreBuildParams) (*BuildParams, error)` — validator-side reconstruction of build params from pre-built inputs.
- `BuildCertificate(ctx, *BuildParams) (*Certificate, error)` — assemble the agglayer certificate from the params.
- `UpdateAggchainData(*Certificate, *Multisig) error` — per requirement 29.
- `Signer() Signer` — the signer configured for this flow.

Verifier flow contract adds:
- `VerifyCertificate(ctx, *Certificate, lastBlockInCert, lastSettledBlock) error` — per requirement 30/31.

Configuration surface (load-bearing, named here because they are user-facing config keys consumed by the factories): mode selector, max certificate size, max L2 block number, starting L2 block (aggchain/FEP only, read from the sovereign rollup contract), require-no-FEP-block-gap, require-one-bridge-in-PP-certificate, full-claims-needed, require-committee-membership-check, block-finality-for-L1-info-tree, delay-between-retries, signer config, and the addresses of the agglayer bridge L2 contract and global exit root L1/L2 contracts.

## Error modes

- **36.** "No new blocks to send" is a first-class non-error signal: the factory's builder flows MUST translate it into a nil build-params return, not an error.
- **37.** Prover errors are opaque except for "no proof built yet", which MUST be translated into a nil build-params return; all other prover errors MUST propagate as errors.
- **38.** Exceeding the configured max L2 block number in a non-resizable retry certificate MUST surface a distinguishable error so callers can stop retrying.
- **39.** "Everything permitted by max L2 block has been sent" MUST surface a distinguishable error so callers can gracefully stop.

## Out of scope

- Persisting certificates, driving the epoch loop, submitting certificates to agglayer, and handling agglayer responses — those are the aggsender service's concern, not this package.
- Multisig aggregation — this package only stamps a provided multisig into a certificate.
- Computing aggchain proofs — delegated to the configured aggchain prover client.
- Signing — delegated to the injected signer.
