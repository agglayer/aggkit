# SPEC: aggsender/aggchainproofclient

## Summary

This directory provides the client-side adapter that lets the aggsender request aggchain proofs from a remote prover service. The adapter exposes an in-process API (request aggchain proof / request optimistic aggchain proof) to aggsender callers and translates those calls into the prover's remote RPC protocol, reshaping the returned proof data into the aggsender's domain types.

The adapter is a pure protocol translator: it owns connection configuration and request-lifetime bounds, but it does not carry aggsender business logic, does not persist state, and does not retry. The only proof shape it understands is an SP1 STARK proof; any other proof variant returned by the prover is rejected.

## Requirements

- **1.** The client MUST expose an operation that, given an aggchain-proof request, obtains an aggchain proof from the configured remote prover service and returns it to the caller.
- **2.** The client MUST expose a separate operation that, given an aggchain-proof request and an opaque signature byte slice, obtains an optimistic-mode aggchain proof from the remote prover and returns it to the caller.
- **3.** Each request to the remote prover MUST be bounded by the request-timeout duration configured on the client; the bound MUST be applied independently per call and MUST cancel the in-flight call when exceeded.
- **4.** The client MUST reject a response whose proof payload is not an SP1 STARK proof by returning a distinguishable "proof is not SP1Stark" error and MUST NOT return a partially populated aggchain proof in that case.
- **5.** On a successful non-optimistic call, the returned aggchain proof MUST carry the SP1 STARK proof bytes, verification key, and version exactly as supplied by the prover, together with the prover-reported last-proven block, end block, local exit root, custom chain data, aggchain params, and context map.
- **6.** On a successful optimistic call, the returned aggchain proof's last-proven-block and end-block fields MUST reflect the values sent in the request (respectively the caller-supplied last-proven block and the caller-supplied requested end block), not any value echoed by the prover.
- **7.** When the caller supplies the request's `L1InfoTreeMerkleProof.Proof` and each GER-leaf proof, the client MUST forward exactly the first `treetypes.DefaultHeight` sibling hashes to the prover, preserving their order.
- **8.** The client MUST translate each imported bridge exit's `(MainnetFlag, RollupIndex, LeafIndex)` triple into the 32-byte canonical global-index representation used by the prover protocol before sending.
- **9.** The client MUST translate each unclaim's `(MainnetFlag, RollupIndex, LeafIndex)` triple into the same 32-byte canonical global-index representation before sending.
- **10.** For each imported bridge exit, the client MUST forward the keccak hash of the bridge-exit structure as the request's bridge-exit-hash field.
- **11.** The client MUST forward the full removed-GER and unclaim lists supplied by the caller, preserving order and count.
- **12.** The client MUST key the transmitted GER-leaves map by the string form of the caller-supplied map key; keys MUST NOT be dropped, merged, or renamed.
- **13.** Construction of the client MUST fail with the underlying connection error, without returning a usable client instance, if the gRPC connection cannot be established from the supplied client configuration.

## External interface

Exported Go API (package `aggchainproofclient`):

- `NewAggchainProofClient(cfg *aggkitgrpc.ClientConfig) (*AggchainProofClient, error)` — constructs a client bound to the supplied gRPC client configuration.
- `(*AggchainProofClient).GenerateAggchainProof(ctx context.Context, req *types.AggchainProofRequest) (*types.AggchainProof, error)` — implements the aggsender-side `AggchainProofClient` interface defined in `aggsender/types/aggchain_proof_client_interface.go`.
- `(*AggchainProofClient).GenerateOptimisticAggchainProof(req *types.AggchainProofRequest, signature []byte) (*types.AggchainProof, error)` — same interface, optimistic variant.

Wire protocol: the aggkit prover gRPC service `aggkit.prover.v1.AggchainProofService` (methods `GenerateAggchainProof` and `GenerateOptimisticAggchainProof`). Request/response schemas are owned by the `buf.build/gen/go/agglayer/provers/...` generated stubs and by `agglayer/interop/types/v1`; this client MUST conform to those schemas.

## Error modes

- **14.** Any transport or service error returned by the remote prover MUST be propagated to the caller after passing through the project-standard gRPC error repack, so that server-side status details are preserved across the package boundary.
- **15.** Returned errors MUST NOT be accompanied by a non-nil aggchain-proof result.

## Out of scope

- Retrying failed requests, circuit-breaking, or backoff — the client performs a single attempt per call.
- Caching, deduplication, or batching of requests.
- Validation of proof correctness beyond the SP1-STARK type check; cryptographic verification is the caller's responsibility.
- Managing the lifecycle of the underlying gRPC connection beyond what `aggkitgrpc.NewClient` provides; this directory does not expose a `Close` operation.
- Persisting requests, responses, or proofs.
