# SPEC: aggsender/validator

## Summary

The validator subsystem is the second-opinion gate between the aggsender (which proposes a certificate) and the agglayer (which settles it). For a candidate certificate produced by the aggsender, the subsystem independently rebuilds what that certificate should look like from ground-truth sources (storage, L1 info tree, flow-specific queriers), compares the proposer's certificate to the rebuilt one, verifies the cryptographic proofs carried inside it, and — in the remote case — returns a signature that attests the proposer's certificate was accepted.

Two deployment shapes are provided behind a single validate-and-sign contract: a local validator that runs the checks in-process and returns an empty signature, and a remote validator that ships the certificate over gRPC to a separately-operated validator service and returns the remote's signature after verifying it recovers to the expected address. The gRPC wire format is owned by `aggsender/validator/proto/SPEC.md`; this directory owns the semantics layered on top of it.

Any new certificate is expected to chain onto the previously-settled one: heights increment by one, its prev-local-exit-root matches the previous new-local-exit-root, its last-L2-block extends past the previous last-L2-block, and (for the first certificate) prev-local-exit-root equals a configured initial LER. A mismatch on any of these, on any rebuilt field, on the certificate hash, on a claim proof, or on flow-specific checks, is a rejection.

## Requirements

- **1.** A validate-and-sign operation on a proposer-supplied certificate MUST return successfully only if every check below passes, and MUST return an error otherwise; it MUST NOT return a success with a partial-pass.
- **2.** The validator MUST reject a nil certificate.
- **3.** For any certificate with height greater than zero, the validator MUST load the immediately-preceding settled certificate header (height − 1, same network) from local storage and use it as the chaining reference.
- **4.** The validator MUST reject the certificate if its height is not exactly one greater than the chaining reference's height.
- **5.** The validator MUST reject the certificate if its prev-local-exit-root is not equal to the chaining reference's new-local-exit-root.
- **6.** For a certificate with height zero (no chaining reference), the validator MUST reject it if its height is non-zero or if its prev-local-exit-root is not equal to the configured initial local-exit-root.
- **7.** The validator MUST reject the certificate if the chaining reference exists and is not in a settled status.
- **8.** The validator MUST reject the certificate if the caller-supplied last-L2-block-in-certificate is not strictly greater than the last-L2-block covered by the chaining reference.
- **9.** The validator MUST reject the certificate if the actual last-L2-block it would cover exceeds the caller-supplied last-L2-block-in-certificate.
- **10.** The validator MUST reconstruct what the certificate should be by invoking a flow-specific build pipeline with the block range `(prev_last_L2_block+1, last_L2_block_in_cert)`, the L1-info-tree root resolved from the proposer's `L1InfoTreeLeafCount - 1`, the computed certificate type, and the chaining reference; the proposer's certificate MUST be rejected if this pipeline fails.
- **11.** The validator MUST reject the certificate if any field compared by the structural diff (network ID, height, prev-local-exit-root, new-local-exit-root, L1-info-tree leaf count, bridge-exit hashes element-wise, imported-bridge-exit global-index hashes element-wise) does not match the rebuilt certificate, or if the rebuilt certificate ID does not equal the proposer's certificate ID.
- **12.** The validator MUST reject the certificate if any imported-bridge-exit's claim proof does not verify against the L1-info-tree root chosen by the build pipeline.
- **13.** The validator MUST invoke the flow-specific post-build verification on the proposer's certificate and MUST reject if it fails.
- **14.** The canonical hash to sign for a certificate MUST be a keccak256 over the concatenation `(new_local_exit_root || keccak256(concat over imported bridge exits of (global_index as little-endian bytes || bridge_exit_hash)) || height as little-endian 8-byte little-endian || aggchain_params || certificate_id)`; the certificate MUST first pass its own internal validation, and hashing MUST fail if it does not.
- **15.** When the validator operates in local mode, a successful validation MUST return a fixed empty-signature sentinel; local mode MUST NOT produce a real signature.
- **16.** When the validator operates in remote mode, it MUST compute the canonical hash before sending the RPC and MUST reject the remote's response if the recovered signer address does not equal the configured remote address for the validator.
- **17.** In remote mode the validator MUST NOT accept legacy Ethereum `v+27` signatures (signature recovery MUST use the raw 65-byte form).
- **18.** The server side of the remote path MUST reject requests whose body or certificate payload is absent, and MUST reject requests whose `previous_certificate_id` references a header the agglayer cannot return.
- **19.** The server side MUST only sign when validation succeeds, and MUST return the signature of the canonical hash computed by the same rule as requirement #14 under the server-configured signer.
- **20.** The server side MUST NOT sign if no signer is configured.
- **21.** Configuration validation MUST reject any configuration whose mode is not one of the supported modes, MUST reject an AggchainProof mode configuration that does not set a non-zero sovereign-rollup address, MUST reject an invalid embedded agglayer-client config, and MUST reject an invalid L1-info-tree block-finality selector.

## Invariants

- **22.** For any fixed certificate content, the canonical hash is deterministic: two evaluations of requirement #14 on identical certificate bytes MUST produce byte-identical hashes.
- **23.** For any two certificates `A`, `B`, if the structural diff returns an empty slice and their certificate IDs are equal, then every field compared by that diff MUST hold pairwise-equal on `A` and `B` (contrapositive: any observable difference MUST surface as at least one diff entry or a certificate-ID mismatch).
- **24.** A validator configured in local mode MUST return the same empty-signature sentinel on every success, so downstream code MAY treat a signature byte-equal to that sentinel as "locally validated, not remote-signed".

## External interface

- A validate-and-sign operation, callable by the aggsender, with input `(context, certificate, last_L2_block_in_cert)` and output `(signature_bytes, error)`. Implementations advertise an identifier string, a URL, an Ethereum address, and a signer index.
- A health-check operation returning a status/version pair.
- A certificate-validator operation, callable by the remote server handler, with input `(context, VerifyIncomingRequest{Certificate, PreviousCertificate, LastL2BlockInCert})` and output `error`.
- A diff helper returning a slice of human-readable strings describing field-level differences between two certificates; an empty slice means structurally equal under the fields listed in requirement #11.
- A canonical-hash helper implementing requirement #14.
- A gRPC service surface defined by `aggsender/validator/proto/v1/SPEC.md#1` (see `aggsender/validator/proto/SPEC.md#6` for version selection).
- Configuration keys, mapstructure names as exposed: `EnableRPC`, `Signer`, `ServerConfig`, `MaxCertSize`, `MaxL2BlockNumber`, `DelayBetweenRetries`, `LerQuerierConfig`, `PPConfig{RequireOneBridgeInPPCertificate}`, `FEPConfig{SovereignRollupAddr, RequireNoBlockGap, OpNodeURL}`, `AgglayerClient`, `Mode` (`PessimisticProof|AggchainProof|Auto`), `RequireCommitteeMembershipCheck`, `AgglayerBridgeL2Addr`, `UnsetClaimsMaxLogBlockRange`, `GlobalExitRootL1Addr`, `BlockFinalityForL1InfoTree`.

## Error modes

- **25.** Every error crossing the validate-and-sign boundary MUST identify the failed check by wrapping the underlying error with an operation-tagged message (which check failed, not just that something did).
- **26.** The remote server MUST map errors to gRPC status codes as follows: missing request body or missing certificate → `NotFound`; previous certificate header cannot be fetched or is absent → `NotFound`; certificate payload fails to deserialize from the wire → `InvalidArgument`; validation or signing failure → `Internal`.
- **27.** The hashing helper MUST propagate the certificate's own validation error unwrapped and MUST NOT silently zero-hash an invalid certificate.

## Out of scope

- Ordering of validators, quorum collection across multiple validators, and committee-membership enforcement on-chain — owned by the aggsender multisig orchestration, not this directory.
- Retry, timeout, and transport configuration of the gRPC client and server — owned by the generic grpc package.
- Storage of previously-settled certificates — consumed via the aggsender storage interface, not defined here.
- The proto wire shapes and method signatures — owned by `aggsender/validator/proto/SPEC.md` and its versioned children.
- Flow-specific semantic checks on `aggchain_params`, FEP vs pessimistic-proof branch logic — owned by whichever verifier flow the caller injects.

## Children

- `proto/` — versioned gRPC wire contract used by the remote path; see `aggsender/validator/proto/SPEC.md#1`.
