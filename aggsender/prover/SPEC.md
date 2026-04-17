# SPEC: aggsender/prover

## Summary

The prover tool is a standalone RPC service that generates an Aggchain SP1 STARK proof for a given L2 block range, on demand. It is a development / operations utility: callers supply the last already-proven L2 block and the maximum requested end block, and the tool drives the same proof-generation flow that the full aggsender would use, but without certificate submission, storage, or optimistic signing. Proofs are returned synchronously to the caller over a JSON-RPC method.

Mental model: the tool is a thin orchestrator that (1) verifies the requested range is within what the local L2 syncer has observed, (2) collects imported-bridge-exit claims for that range, and (3) delegates to the Aggchain proof generation flow, returning only the opaque SP1 STARK proof portion of the result.

## Requirements

- **1.** The tool MUST expose a JSON-RPC method under the `aggkit` service namespace that accepts two unsigned integer parameters, the last already-proven L2 block and the maximum requested end block, and returns a generated SP1 STARK proof for that range.
- **2.** The tool MUST refuse to generate a proof when the local L2 bridge syncer has not yet processed any block, reporting an error to the caller.
- **3.** The tool MUST refuse to generate a proof when the local L2 bridge syncer's last processed block is strictly less than the caller-supplied last-proven block, reporting an error that identifies both values.
- **4.** The proof range sent to the underlying Aggchain proof flow MUST begin at `lastProvenBlock + 1` and end at the caller-supplied maximum end block.
- **5.** The tool MUST include all L2 bridge claims whose block falls in the range `[lastProvenBlock + 1, maxEndBlock]` in the parameters passed to the Aggchain proof flow.
- **6.** The tool MUST return only the SP1 STARK proof component of the flow's result to the RPC caller; other fields of the flow's output MUST NOT be exposed through this RPC.
- **7.** Construction of the tool MUST fail if the prover client configuration is not valid.
- **8.** Construction of the tool MUST fail if any of the L1 info-tree querier, the L2 global-exit-root reader, or the L2 sovereign-bridge reader cannot be initialised from the supplied addresses and clients.
- **9.** When the tool is used outside a full aggsender, the optimistic-mode querier it exposes to proof generation MUST always report optimistic mode as disabled.

## External interface

- JSON-RPC method: `aggkit_generateAggchainProof(lastProvenBlock uint64, requestedEndBlock uint64)` — returns the generated SP1 STARK proof on success, or a JSON-RPC error whose message carries the underlying failure reason.
- Go package exports:
  - `AggchainProofGeneration` — interface with `GenerateAggchainProof(ctx, fromBlock, toBlock) (*types.SP1StarkProof, error)`.
  - `AggchainProofFlow` — interface the tool depends on to produce proofs, with `GenerateAggchainProof(ctx, lastProvenBlock, toBlock, *types.CertificateBuildParams) (*types.AggchainProof, error)`.
  - `Config` — mapstructure keys `GlobalExitRootL1Addr`, `AggkitProverClient`, `GlobalExitRootL2` (mapped to `GlobalExitRootL2Addr`), `SovereignRollupAddr`, `AgglayerBridgeL2Addr`.
  - `NewAggchainProofGenerationTool(ctx, logger, cfg, l1Client, l2Client, l2Syncer, l2ClaimSyncer, l1InfoTreeSyncer)` constructor.
  - `AggchainProofGenerationTool.GetRPCServices()` — returns a single RPC service named `aggkit`.
  - `OptimisticModeQuerierAlwaysOff` — struct whose `IsOptimisticModeOn()` always returns `(false, nil)`.

## Error modes

- **10.** Every error surfaced by the tool's `GenerateAggchainProof` MUST be wrapped with a description of the failing stage (last-processed-block lookup, claim retrieval, or proof generation) so callers can distinguish them by substring.
- **11.** Errors returned over JSON-RPC MUST carry the default RPC error code; the tool MUST NOT invent domain-specific error codes.

## Out of scope

- Persisting proofs, certificates, or state. The tool is stateless across calls.
- Submitting certificates to the agglayer, signing, or any aggsender lifecycle beyond proof generation.
- Optimistic-mode proof generation: the tool hard-wires optimistic mode to off (see #9).
- Retrying or queuing proof generation: a failed call surfaces the error and leaves no background work.
