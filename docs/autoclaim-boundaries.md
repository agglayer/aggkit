# Auto Claim Implementation Boundaries

This note records the P1 code-boundary decisions for the first L1 to L2 Auto Claim scope.

## Package Layout

Use a new top-level `autoclaim` component. The first implementation should create these packages:

- `autoclaim/config`: Auto Claim config structs, defaults, validation helpers, and policy-specific config.
- `autoclaim/types`: request lifecycle enums, policy result enums, bridge request data, proof data,
  claim calldata data, claimer target data, and interfaces shared across subpackages.
- `autoclaim/storage`: migrations and repository implementation for requests, policy decisions, manual decisions,
  transaction attempts, cursors, and state transitions.
- `autoclaim/policy`: named policy registry and implementations for `allow-all`, `api-approve`, `no-message`,
  and `basic-filter`.
- `autoclaim/claimer`: per-destination engine that evaluates policy, prepares proofs, encodes claim calldata,
  submits through `EthTxManager`, and tracks transaction status.
- `autoclaim/watchdog`: L1 to L2 bridge discovery using `l1bridgesync`; the L2 to Lx watchdog remains disabled
  and unimplemented in first scope.
- `autoclaim/api`: optional REST handlers and response mapping for status, approve, and reject operations.

Add `common.AUTOCLAIM = "autoclaim"` as a normal component selector in P2. `AutoClaim.Enabled` remains a separate
config gate and defaults to false. Runtime wiring should start Auto Claim only when the `autoclaim` component is
selected and `AutoClaim.Enabled` is true.

## API Prefix

Use `/autoclaim/v1` for the optional REST API. This does not collide with the existing bridge service prefix
`/bridge/v1` from `bridgeservice.BridgeV1Prefix`.

Initial endpoints should be scoped under this prefix:

- `GET /autoclaim/v1/bridges`
- `GET /autoclaim/v1/bridges/{id}`
- `POST /autoclaim/v1/bridges/{id}/approve`
- `POST /autoclaim/v1/bridges/{id}/reject`

The API is optional. Claiming must continue to work when `AutoClaim.API.Enabled` is false.

## Proof Logic Boundary

Extract the L1-origin proof path from `bridgeservice` into shared, testable logic behind Auto Claim interfaces. Do not
call the bridge service REST API from Auto Claim.

The current reusable sequence is:

1. Find the first L1 info tree index containing the L1 bridge. The existing logic is
   `BridgeService.getFirstL1InfoTreeIndexForL1Bridge` in `bridgeservice/bridge.go`.
2. Load the selected L1 info tree leaf with `L1InfoTreeSyncer.GetInfoByIndex`.
3. Get the L1 local exit root proof with `bridgeL1.GetProof(ctx, depositCount, info.MainnetExitRoot)`.
4. Get the rollup exit root proof with `l1InfoTree.GetRollupExitTreeMerkleProof(ctx, 0, info.RollupExitRoot)`.
5. Convert both proofs from `tree.Proof` into `[32][32]byte` for bridge contract ABI packing.

For first scope, implement only `origin_network == 0` claims. If `getFirstL1InfoTreeIndexForL1Bridge` would return
`bridgeservice.ErrNotOnL1Info`, keep the request pending rather than marking it failed.

Do not make Auto Claim depend on `bridgeservice.BridgeService` itself. The proof preparer should depend on narrow
interfaces matching the methods already in `bridgeservice/bridge_interfaces.go`:

- `bridgeL1.GetRootByLER`, `bridgeL1.GetLastRoot`, and `bridgeL1.GetProof`.
- `l1InfoTree.GetLastInfo`, `GetFirstInfo`, `GetFirstInfoAfterBlock`, `GetInfoByIndex`, and
  `GetRollupExitTreeMerkleProof`.

This gives unit tests the same mocking seam as bridge service tests and avoids coupling a background claimer to HTTP
handler state.

## Public Interfaces

Define interfaces in `autoclaim/types` for the cross-package contracts:

- `BridgeSource`: lists or pages L1 bridge exits from `l1BridgeSync.GetBridges(ctx, fromBlock, toBlock)` and exposes
  enough cursor data for overlap-safe polling.
- `ProofPreparer`: returns L1 info tree index, selected leaf roots, local proof, rollup proof, and converted ABI proofs
  for an L1 bridge request.
- `Policy`: evaluates a request and returns approved, rejected, or manual with a stable reason.
- `Storage`: owns idempotent enqueue, request lookup, paginated list filters, manual decisions, proof persistence,
  transaction attempts, and atomic lifecycle transitions.
- `ClaimSender`: packs `claimAsset` or `claimMessage` calldata and submits it through `EthTxManager.Add`.
- `TargetClaimReader`: checks whether the target bridge has already claimed the global index before submitting.
- `Claimer`: accepts discovered requests for one destination network and advances stored requests through policy,
  proof readiness, sending, and confirmation.

Reuse `aggoracle/types.EthTxManager` for transaction submission. Its `Add`, `Result`, `ResultsByStatus`, `Remove`, and
`From` methods already match the required transaction manager boundary.

## Manual L1 To L2 Claim Path Findings

The manual e2e flow in `test/e2e/bridge_utils.go` does the following:

- Finds the L1 bridge through bridge service `GetBridges` and stores `DepositCount`.
- Polls `GetL1InfoTreeIndex(ctx, 0, depositCount)` until the bridge is included in the L1 info tree.
- Polls `GetInjectedL1InfoLeaf(ctx, l2NetworkID, l1InfoTreeIndex)` to verify the destination L2 has received the GER.
- Calls `GetClaimProof(ctx, 0, l1InfoTreeIndex, depositCount)`.
- Converts proof hex entries into `[32][32]byte`.
- Sends `ClaimAsset` on the L2 bridge binding.

Auto Claim should replace the HTTP calls in that path with in-process interfaces and should replace the direct
`bind.TransactOpts` send with `EthTxManager.Add`.

## Claim Encoding

Use bridge ABI packing for `claimAsset` when the bridge leaf type is `bridgesync/types.LeafTypeAsset`, and
`claimMessage` when it is `LeafTypeMessage`. The argument order is the same for both:

1. local exit root proof `[32][32]byte`
2. rollup exit root proof `[32][32]byte`
3. global index
4. mainnet exit root
5. rollup exit root
6. origin network
7. origin token address for assets, origin address for messages
8. destination network
9. destination address
10. amount
11. metadata

Use `bridgesync.GenerateGlobalIndexForNetworkID(0, depositCount)` for L1-origin requests. For L1 to L2, origin network
is always `0`.

## Future-Step Facts

- P2 should add an `autoclaim` component constant and validation entry, while keeping Auto Claim absent from the
  default component list.
- P3 should test global index derivation against `bridgesync.GenerateGlobalIndexForNetworkID`.
- P6 should share or extract the L1-origin proof logic instead of duplicating bridge-service HTTP handlers.
- P7 should encode calldata and submit to the configured target bridge address through `EthTxManager.Add`; do not use
  direct generated binding transactors in production Auto Claim code.
- P9 should discover only `OriginNetwork == 0` bridges and route by enabled claimer `DestinationNetwork`.
- P10 should not modify `/bridge/v1`; use `/autoclaim/v1`.
