# SPEC: aggsender/optimistic

## Summary

This directory provides the aggsender's "optimistic mode" surface: (a) a construction-time wiring that assembles the two primitives the aggsender needs in optimistic mode, (b) a querier that reports whether a sovereign rollup has optimistic mode enabled on-chain, and (c) a signature calculator that derives a per-certificate commitment digest and signs it with the configured trusted-sequencer key. Optimistic mode is a protocol mode in which the chain's aggchain proof is replaced by a signature from a designated trusted signer attesting to the aggregation-proof public values, the new local exit root, and the commitment over imported bridge exits.

The directory is the integration point: it pulls the AggchainFEP contract binding, the go-signer abstraction, the opnode client, the aggregation-proof public-values query, and the hash primitives in `optimistichash/` into two public types the aggsender can drive. The actual digest layout is delegated to `optimistichash/` and MUST NOT be redefined here.

## Requirements

- **1.** Constructing the optimistic subsystem MUST produce two independent objects — a signature calculator and an optimistic-mode querier — that share no mutable state; failure to build either MUST fail the whole construction and MUST NOT expose a partially-initialised subsystem.
- **2.** Construction MUST reject a configuration whose sovereign rollup address is the zero address.
- **3.** Construction MUST reject a configuration whose OpNode URL is empty.
- **4.** Construction MUST instantiate the configured signer and complete its initialisation before returning the signature calculator; a signer that fails to instantiate or initialise MUST fail construction.
- **5.** Construction MUST read the trusted-signer address from the AggchainFEP contract and MUST compare it to the configured signer's public address.
- **6.** If the configured-signer-vs-contract comparison is disabled (opt-in flag unset), a read failure or a mismatch MUST be logged as a warning and MUST NOT fail construction.
- **7.** If the configured-signer-vs-contract comparison is enabled, a read failure MUST fail construction, and a mismatch between the configured signer address and the contract's trusted-signer address MUST fail construction.
- **8.** The optimistic-mode querier MUST report the current value of the on-chain `optimisticMode` flag of the configured AggchainFEP contract on every call; it MUST NOT cache the value across calls.
- **9.** If the on-chain read of the optimistic-mode flag fails, the querier MUST surface the underlying error to the caller and MUST NOT return a default boolean as a successful result.
- **10.** Signing a certificate MUST fetch the aggregation-proof public values for the triple (last proven block, requested end block, previous-block hash of the L1 info tree leaf) from the configured source before producing any digest; a fetch failure MUST abort signing and surface the error.
- **11.** Signing MUST derive the per-certificate commitment digest from exactly three inputs — the hash of the fetched aggregation-proof public values, the caller-supplied new local exit root, and the commitment over the caller-supplied ordered claim list — using the layout defined in `aggsender/optimistic/optimistichash/SPEC.md#1` and `#3`.
- **12.** Signing MUST invoke the signer on the derived commitment digest and MUST return the signer's output bytes unchanged on success.
- **13.** On success, signing MUST return a diagnostic string that includes the rendered aggregation-proof public values, the rendered signature input triple, the number of claims hashed, and the hex of the signed commitment digest; the string is diagnostic only and MUST NOT be re-parsed as protocol data.
- **14.** Signing MUST produce identical commitment digests and identical signer inputs for identical `(aggchainProofRequest, newLocalExitRoot, claims)` inputs when the fetched aggregation-proof public values are equal, so signatures over the same certificate are reproducible.
- **15.** The signer-address used by the aggregation-proof public-values query MUST be the public address of the same signer that will later sign the commitment digest.

## External interface

- Package-level constructor that takes a logger, an L1 Ethereum client, a chain id, and a `Config`, and returns the signature calculator, the optimistic-mode querier, and an error.
- `Config` — mapstructure-tagged struct with keys `SovereignRollupAddr` (L1 address of the AggchainFEP contract), `TrustedSequencerKey` (signer config consumed by `go_signer`), `OpNodeURL` (URL of the OpNode service), and `RequireKeyMatchTrustedSequencer` (bool). `Validate()` enforces #2 and #3.
- Optimistic-mode querier — implements `aggsender/types.OptimisticModeQuerier` (`IsOptimisticModeOn() (bool, error)`) against the AggchainFEP contract at the configured sovereign rollup address.
- Signature calculator — implements `aggsender/types.OptimisticSigner` (`Sign(ctx, AggchainProofRequest, newLocalExitRoot common.Hash, []claimsynctypes.Claim) ([]byte, string, error)`).

## Error modes

- **16.** Every error returned from this package MUST wrap the underlying cause with `%w` so callers can unwrap; construction errors MUST be tagged so they are identifiable as originating in the optimistic subsystem.
- **17.** A signer-vs-contract mismatch error MUST include both the configured signer address and the contract's trusted-signer address in hex.

## Out of scope

- The byte layout and algorithm of the commitment digest; delegated to `aggsender/optimistic/optimistichash/SPEC.md#1`, `#3`, `#4`, `#5`, `#7`, `#8`.
- Computing the aggregation-proof public values themselves; this directory consumes them from `aggsender/query` and `opnode`.
- Key storage, HSM integration, or signing algorithm selection; delegated to `go_signer`.
- Deciding, from the caller's side, whether optimistic mode SHOULD be used for a given certificate; this directory only answers the on-chain question and produces signatures on demand.
- Validating claim content; malformed claims produce a digest per `optimistichash/SPEC.md` and the caller is responsible for well-formed inputs.

## Children

- `optimistichash/` — pure hash primitives for the optimistic signature digest and the imported-bridge-exits commitment; see `optimistichash/SPEC.md#1`, `#3`.
