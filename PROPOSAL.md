# Proposal: Proof-Carrying AggSender Validator Payload

## Goal

Reduce the operational complexity of running AggSender validators by replacing most local indexing work with a
proof-carrying payload produced by the AggSender.

The target shape is:

1. AggSender selects an L2 block and includes its block hash in the validator request.
2. AggSender includes Merkle proofs for the minimal contract state, bridge leaves, and claim data needed by the
   validator.
3. The validator retrieves or verifies the selected final L2 block header.
4. If local exit root or hash-chain replay is used, the validator also authenticates the previous settled L2 block header
   with a second L2 header query.
5. The validator verifies all included proofs and preserves the current validation guarantees.

This document is intentionally conservative. If a validation property is not proven by the current code or by a known
standard RPC proof, it is marked as **Needs research**.

## Current Validator Behavior

The current validator does not only check a certificate signature payload. It rebuilds the certificate from independent
data sources and compares the rebuilt certificate with the received one.

Current checks include:

- Fetch the previous certificate header from AggLayer when a previous certificate id is provided
  (`aggsender/validator/validator_service.go`).
- Determine the previous settled certificate boundary through `CertificateQuerier.GetLastSettledCertificateToBlock`
  (`aggsender/validator/validate_certificate.go`).
- Check the certificate block range and previous certificate continuity
  (`validateLastL2BlockInCert`, `checkContigousCertificates`, and `checkFirstCertificateBlocks`).
- Fetch L1 info root data with `GetL1InfoRootByLeafIndex`.
- Rebuild certificate inputs from bridge sync, claim sync, L1 info tree data, AggLayer status, and flow-specific sources.
- Build an expected certificate and require exact equality with the received certificate.
- Verify imported bridge exit proofs using `ImportedBridgeExit.VerifyProofs`.
- Run flow-specific checks. The pessimistic proof flow has no extra verifier check today. The current FEP/Aggchain proof
  flow reconstructs `AggchainParams` using L1 FEP contract data and OP node output roots.

The main data dependencies in the current code are:

- L2 bridge events and local exit tree state from `bridgesync`.
- L2 claim, unset-claim, and detailed claim events from `claimsync`.
- L1 info tree roots, leaves, GER proofs, and GER existence checks from `l1infotreesync`.
- AggLayer previous certificate status.
- FEP contract state and OP node outputs for the current FEP/Aggchain proof flow.

## RPC Node Requirement For Merkle Proof Generation

### State Proofs

For Ethereum-style account and storage proofs, the relevant RPC method is `eth_getProof`, standardized by EIP-1186:

- EIP-1186: <https://eips.ethereum.org/EIPS/eip-1186>

`eth_getProof` can prove account state and selected storage slots at a specific block. A validator can verify those
proofs only if it has the corresponding block header state root. Therefore, each L2 state proof must be paired with an
authenticated L2 block header, not only a bare block hash. The validator must check:

- The returned block hash equals the hash included in the AggSender payload.
- The returned block number equals the payload block number.
- The account and storage proofs verify against the block header `stateRoot`.

If the validator only verifies that a block hash exists, it cannot verify storage proofs, because the proof root is the
block header state root.

### Geth And Reth Compatibility

The proof generation path must support both Geth-based and Reth-based RPC nodes.

Client compatibility requirements:

- Use standard Ethereum JSON-RPC methods wherever possible: `eth_getBlockByHash` or `eth_getBlockByNumber` for the
  validator header query, and `eth_getProof` for account/storage proofs.
- Do not make the validator depend on client-specific `debug_*` or `trace_*` APIs.
- Feature-detect proof-generation support at startup and fail clearly if the configured RPC cannot produce the required
  proofs for the selected block.
- Run the same proof-generation and verification conformance tests against both Geth and Reth nodes.

Known client-specific facts:

- Geth documents multiple archive and history-retention modes. Historical `eth_getProof` support depends on historical
  trie-node retention configuration, not just on the word "archive". Geth's archive documentation states that historical
  Merkle proofs require explicit trie-node history retention:
  <https://geth.ethereum.org/docs/fundamentals/archive>
- Reth exposes an `eth_getProof` RPC API in its `EthApiClient` documentation:
  <https://reth.rs/docs/reth/rpc/api/trait.EthApiClient.html#method.get_proof>
- Reth exposes `--rpc.eth-proof-window`, `--prune.account-history.distance`, and
  `--prune.storage-history.distance` flags for proof and historical state retention:
  <https://reth.rs/cli/reth/node/>
- Reth pruning documentation describes default full-node pruning and RPC availability limits:
  <https://reth.rs/run/faq/pruning/>

The validator mode must define a required proof retention window `W` in L2 blocks. `W` must be at least the maximum
expected distance between the previous settled boundary block used for replay and the time validators finish checking the
certificate, including certificate construction delay, validator request delay, retry budget, and any finality/reorg
buffer.

Required L2 RPC configuration:

- Geth-based proof generation RPC:
  - must run a Geth version and L2 build that supports `eth_getProof`;
  - must expose the `eth` JSON-RPC namespace;
  - must retain historical trie nodes for at least `W` blocks;
  - for Geth path-based state, configure trie-node history retention with `--history.trienode=<W>` or the equivalent
    flag supported by the exact deployed Geth version;
  - must pass startup proof checks for all required bridge slots at both a recent block and a block at least `W` blocks
    behind the tip.
- Reth-based proof generation RPC:
  - must run a Reth version and L2 build that supports `eth_getProof`;
  - must expose the `eth` JSON-RPC namespace;
  - must set `--rpc.eth-proof-window=<W>` or larger;
  - must set `--prune.account-history.distance=<W>` and `--prune.storage-history.distance=<W>` or larger, unless the
    node is configured with stronger non-pruned history for those segments;
  - must pass startup proof checks for all required bridge slots at both a recent block and a block at least `W` blocks
    behind the tip.

The defaults are not sufficient as a protocol requirement. Reth's documented full-node pruning keeps account and storage
history for a finite recent window, and its proof window default must not be treated as covering `W` unless the deployed
version proves that behavior. Geth documents that historical trie data is not retained unless configured. Therefore, the
validator mode must require explicit proof-retention configuration and verify it empirically at startup.

### Full Nodes Versus Archive Nodes

The goal is to avoid archive nodes. That is only compatible with state proof generation if the proof block is inside the
state history retained by the selected L2 full node.

Geth's archive documentation distinguishes archive state from pruned full-node state:

- Geth archive nodes: <https://geth.ethereum.org/docs/fundamentals/archive>

The practical requirement is:

- AggSender proof generation must run against an L2 node that supports `eth_getProof`.
- The selected proof block must be recent enough that the full node still retains the needed state trie data.
- The required retention window must be larger than the maximum time between:
  - L2 block production,
  - bridge/claim indexing,
  - certificate construction,
  - validator request,
  - retries after transient failures,
  - any finality or reorg buffer used by AggSender.

Full-node compatibility is therefore conditional, but concrete:

- It is compatible with Geth-based full nodes if they retain historical trie nodes for the whole required proof window.
- It is compatible with Reth-based full nodes if their account history, storage history, and RPC proof window all cover
  the whole required proof window.
- It is not compatible with default pruned nodes unless the default retained window is proven to be at least `W` for the
  target deployment.

AggSender should trim or reject certificate ranges that would require a previous-boundary proof older than the configured
proof window.

### Event And Log Proofs

`eth_getProof` proves account and storage state. It does not prove that a specific event log occurred, and it does not
prove that no other matching logs occurred in a block range.

Current bridge and claim sync logic is event-driven. Some claim details are taken from `DetailedClaimEvent`, while legacy
claim data is reconstructed from transaction calldata/traces in `claimsync/downloader.go`.

**Needs research:** a standard or implementation-specific way to produce and verify receipt/log inclusion proofs on the
target L2. Without log or receipt proofs, final contract storage can prove current state, but it cannot by itself prove
block-range completeness or event ordering.

## Data Points From Smart Contracts To Prove

This section lists data that appears necessary to preserve existing checks. It does not claim the storage slots are known
yet. The exact storage layout must be derived from the deployed contract source and implementation version.

### L2 Bridge Contract

The generated bindings for the bridge contracts expose these relevant state methods and events:

- `getRoot()`
- `depositCount()`
- `lastUpdatedDepositCount()`
- `claimedBitMap(uint256)`
- `isClaimed(uint32 leafIndex, uint32 sourceBridgeNetwork)`
- `BridgeEvent`
- `ClaimEvent`
- For the L2 bridge variant: `DetailedClaimEvent`, `SetClaim`, `UpdatedClaimedGlobalIndexHashChain`,
  `UpdatedUnsetGlobalIndexHashChain`, `claimedGlobalIndexHashChain()`, and `unsetGlobalIndexHashChain()`.

The proof-carrying payload should prove, at the selected L2 block:

- The bridge contract account proof.
- The local exit root exposed by `getRoot()`.
- The deposit count or last updated deposit count used to bind the local exit tree boundary.
- For each imported claim, the relevant claimed/nullifier state:
  - `claimedBitMap(wordIndex)` for the claim global index, or
  - a proved hash-chain state if the sovereign L2 claim hash-chain semantics are used.

For sovereign L2 bridge deployments, the current bindings expose both `claimedGlobalIndexHashChain()` and
`unsetGlobalIndexHashChain()`. A verified BridgeL2SovereignChain source on KatanaScan documents these as append-only
hash-chain accumulators:

- `claimedGlobalIndexHashChain` is updated on every bridge claim.
- `unsetGlobalIndexHashChain` is updated every time the bridge manager unsets a claim.
- The source comments describe the public state variables as chains over global indexes, and the claim implementation
  shown in the verified source updates the claimed chain with the previous chain value and
  `keccak256(bytes32(globalIndex), leafValue)`.
- Source reference: <https://katanascan.com/address/0xe3e6f722b047ce3b1f733d9c5b0609da4e9d1fab>

That means the certificate payload can use state proofs for the hash-chain state at two L2 blocks:

- The previous settled L2 block boundary.
- The final L2 block covered by the certificate.

Then the validator can replay the claimed-chain transitions from the imported bridge exits in the certificate and compare
the reconstructed final chain value with the proven final `claimedGlobalIndexHashChain`.

This is stronger than a per-claim bitmap proof for ordering because changing, omitting, or reordering a claim changes the
reconstructed chain value.

Implementation requirement: pin the exact hash-chain update formula for every supported deployed bridge implementation
and version, not only the verified source linked above. The proposal uses formula ids so the validator can support
multiple pinned formulas without trusting the AggSender to define the formula.

The previous-boundary hash-chain proof requires the validator to authenticate the previous block state root. In this
proposal, the validator does that with a second L2 header query.

**Needs research:** the current bindings show a claimed bitmap and L2 hash-chain state. They do not show a Merkle
nullifier tree. If a nullifier tree exists in another contract or implementation version, it must be identified before
this design can rely on it.

### L2 Bridge And Claim Events

The current validator gets bridge and claim data from logs. A direct event-proof design would need proofs for:

- Every `BridgeEvent` or `ForwardLET` bridge leaf included in the certificate block range.
- Any `BackwardLET` event that affects the local exit tree for sovereign chains.
- Every `ClaimEvent` or `DetailedClaimEvent` included as an imported bridge exit, unless the L2 claimed hash-chain state
  is used and the certificate contains enough claim data to replay the chain exactly.
- Every `SetClaim` or `UpdatedUnsetGlobalIndexHashChain` event used by the current unset-claim filtering logic, unless
  the L2 unset hash-chain state is used and the certificate contains enough unset data to replay the chain exactly.

For each bridge leaf, the payload should include enough data to recompute the `BridgeExit.Hash()` value:

- Leaf type.
- Origin network.
- Origin token address.
- Destination network.
- Destination address.
- Amount.
- Metadata.
- Deposit count / leaf index.

For each imported bridge exit, the payload should include enough data to run the existing
`ImportedBridgeExit.VerifyProofs` logic:

- Bridge exit data.
- Global index.
- Mainnet or rollup source proof.
- L1 info tree leaf.
- L1 info tree proof.
- GER-to-L1-root proof.

The proposed new validator mode tries to avoid receipt/log proofs for the main bridge and claim ordering checks:

- Bridge completeness is handled by local exit root delta replay.
- Imported bridge exit ordering is handled by claimed hash-chain replay.
- Unset ordering is handled by unset hash-chain replay, if proven equivalent to current behavior.

Receipt/log proofs remain a fallback research path if local exit root replay or hash-chain replay cannot cover a required
validation property. Receipt proofs normally verify against the block receipt root, so any receipt-proof fallback would
require the validator to authenticate the relevant block header, including `receiptsRoot`.

### L2 Local Exit Root Delta Replay

For bridge exits, the preferred proposal is to avoid proving every bridge event log and instead prove the local exit tree
state transition.

Payload requirements:

- Previous boundary L2 block number and hash.
- Final L2 block number and hash.
- `eth_getProof` account/storage proof for the bridge local exit root at the previous boundary block.
- `eth_getProof` account/storage proof for the bridge local exit root at the final block.
- `eth_getProof` account/storage proof for the bridge deposit count or equivalent leaf-count state at both boundaries.
- Ordered bridge exits included in the certificate.
- Enough local exit tree append witness data to replay from the previous tree state to the final tree state.

Validator replay:

1. Authenticate the previous and final L2 block headers.
2. Verify previous and final bridge state proofs against their corresponding header state roots.
3. Require the previous proved local exit root to equal `certificate.PrevLocalExitRoot`.
4. Require the final proved local exit root to equal `certificate.NewLocalExitRoot`.
5. Require the number of bridge exits in the certificate to match the proved deposit-count delta.
6. Replay the ordered bridge exits from the previous local exit tree state to the final local exit root.

If replay succeeds, the validator has proven that the certificate bridge exits explain the whole local-exit-root delta
between the previous settled L2 block and the final L2 block.

This intentionally ignores `BackwardLET` and `ForwardLET` event semantics. Under this proposal, the validator only cares
about the final authenticated local exit tree state and whether the certificate's bridge leaves reconstruct the state
transition. That matches the stated AggLayer requirement: the AggLayer cares about the final local exit root, not the
specific corrective event sequence used by the L2 bridge contract to get there.

Important constraint:

- A Merkle root alone is not enough to append new leaves statelessly. The current append-only tree implementation
  maintains a left-frontier cache while inserting leaves.
- The proof-carrying payload should therefore include the previous local exit tree frontier as witness data.
- The L2 bridge syncer already maintains the full local exit tree, so the AggSender side should be able to derive this
  previous frontier from its bridge syncer state.

Concise frontier proposal:

- Add a bridge syncer helper similar to `GetRootByLER`, for example `GetAppendFrontierByLER(ctx, ler)`.
- The helper returns:
  - `leaf_count`: number of leaves represented by `ler`.
  - `root`: the local exit root hash.
  - `frontier[32]`: canonical append frontier for the next leaf.
- The bridge syncer can derive this from its local exit tree database. Roots already store `Index`, `BlockNum`, and
  `BlockPosition`; tree nodes are stored by hash; the existing append-only tree code already reconstructs equivalent
  cache state when appending from the latest root.
- The payload serializes `leaf_count` plus the 32 frontier hashes.
- The validator first checks that the frontier recomputes `certificate.PrevLocalExitRoot` for `leaf_count`.
- The validator then appends each certificate bridge leaf in deposit-count order using the same append-only tree hashing
  rules and checks the resulting root equals the proved final LER and `certificate.NewLocalExitRoot`.

This is a small syncer API addition, not a new indexing requirement.

Implementation note: the helper must support arbitrary historical roots, while the current cache reconstruction path is
oriented around the latest root. The stored root and tree-node tables appear to contain the needed data.

### Syncer Block Metadata For Proof Selection

The relevant syncers already attach block metadata to the data needed by the proof payload:

- Local exit tree roots store `Hash`, `Index`, `BlockNum`, and `BlockPosition` in `tree/types.Root`.
- `BridgeSync.GetRootByLER(ctx, ler)` returns the root metadata for a LER hash.
- `BridgeSync.GetExitRootByIndex(ctx, index)` returns the root at the moment a leaf index was added.
- Bridge events store `BlockNum`, `BlockPos`, and `DepositCount`.
- Claims store `BlockNum` and `BlockPos`.
- Unclaims returned by `GetUnsetClaimsForBlockRange` store `BlockNumber` and `LogIndex`.

AggSender can therefore derive the two L2 proof blocks from syncer data:

- Previous proof block:
  - query AggLayer for the previous settled certificate,
  - take the previous certificate's `NewLocalExitRoot`,
  - call `GetRootByLER` to recover the L2 block where that LER was observed by bridge sync.
- Final proof block:
  - use the certificate `last_l2_block_in_cert`,
  - cross-check it is at or after the max block among included bridges, claims, and unclaims,
  - generate final L2 state proofs at that block.

This avoids a separate event scan to discover block numbers for proof generation.

### Storage Slot Derivation For `eth_getProof`

`eth_getProof` needs raw storage keys, not ABI method names. The Go bindings generated by `abigen` are not enough to
derive those keys: they expose public getters and event bindings, but they do not include Solidity storage layout.

The correct source of truth is Solidity storage layout metadata. Solidity documents that the compiler can emit
`storageLayout` through the standard JSON interface, with entries containing `label`, `slot`, `offset`, and `type`:

- Solidity storage layout docs: <https://docs.solidity.org/en/latest/internals/layout_in_storage.html#json-output>

Proposal:

- Generate or vendor `storageLayout` metadata for every supported bridge contract implementation version.
- Convert the full compiler output into a small committed manifest used by Go:
  - contract family,
  - implementation version or code hash,
  - variable label,
  - slot,
  - offset,
  - type,
  - optional mapping/key encoding rule.
- Go loads this manifest at startup and selects the layout by configured bridge version, implementation address, or
  implementation code hash.
- Go derives the storage keys from the manifest and sends those keys to `eth_getProof`.
- Before using a layout in production, AggSender should cross-check derived storage reads against contract getters at the
  same block:
  - call `getRoot()` and compare it with `eth_getStorageAt` for the derived local exit root slot,
  - call `depositCount()` and compare it with the derived deposit count slot,
  - call `claimedGlobalIndexHashChain()` and compare it with the derived slot,
  - call `unsetGlobalIndexHashChain()` and compare it with the derived slot.

Go implementation sketch:

- Use `ethclient.StorageAt(ctx, bridgeAddr, storageKey, blockNumber)` for layout self-checks.
- Use generated bindings with `bind.CallOpts{BlockNumber: blockNumber}` for the public getter side of the self-check.
- Use the underlying RPC client for `eth_getProof`, because this repo does not currently wrap a typed proof API:
  - request params: bridge contract address, derived storage keys, block number/tag,
  - decode `accountProof` and `storageProof`.
- For fixed scalar variables, the storage key is the 32-byte slot number from `storageLayout`.
- For mappings, compute `keccak256(abi.encode(key, slot))` using go-ethereum encoding and `crypto.Keccak256Hash`.
  The current core proposal relies mostly on scalar public state, but this rule is needed if bitmap words remain in scope.

Proxy handling:

- `eth_getProof` must be requested against the proxy address, because delegatecall stores implementation state in proxy
  storage.
- The layout must correspond to the active implementation code.
- The implementation should be authenticated by chain config, implementation address, implementation code hash, or the
  proxy's implementation slot if the deployment uses an EIP-1967-compatible proxy.

Validator trust model:

- Validators must not trust storage slot keys supplied by AggSender.
- Validators ship or configure their own trusted storage-layout manifest.
- AggSender may identify a bridge contract family/version, but the validator independently maps that version to expected
  storage keys.
- The validator rejects a payload if any `eth_getProof` storage key is not exactly one of the locally expected keys for
  the selected contract/version.
- The validator verifies the account proof for the bridge proxy address and the storage proofs for the expected keys
  against the L2 block header state root.
- For proxy deployments, the validator must also authenticate that the proxy is using the expected implementation. Options:
  - prove the proxy implementation slot and compare it with the configured implementation address,
  - compare the implementation code hash with a trusted code hash from validator config,
  - or pin the exact proxy and implementation addresses in chain config.
- Runtime getter-versus-storage self-checks are defense in depth. They catch manifest mistakes and deployment drift, but
  the core trust anchor is the validator-owned manifest plus implementation authentication.

This keeps slot derivation deterministic and testable in Go without relying on ABI guessing or client-specific debug
methods.

### Validator Setup Tooling

This proposal does not require building the setup tooling before approving the design. The production implementation of
this validator mode must include setup tooling, because validators cannot safely rely on operators manually calculating
storage slots or copying proof keys.

Required eventual setup scripts:

- Generate storage-layout manifests from Solidity compiler output or vendored contract artifacts.
- Reduce full `storageLayout` output into the small manifest consumed by Go.
- Verify a manifest against a live chain by comparing derived storage slots with public getters at a chosen block.
- Verify proxy implementation identity using configured proxy/implementation addresses, implementation slot proof, or
  implementation code hash.
- Register supported hash-chain formula ids for the target bridge version.
- Run a dry-run proof generation and validation flow against a recent finalized certificate range.
- Produce a setup report containing:
  - bridge proxy address,
  - implementation address or code hash,
  - selected storage-layout manifest version,
  - expected storage keys,
  - formula ids,
  - L1 GER manager address,
  - L2 RPC proof support result,
  - L1 finalized-call support result.

Initial validator setup:

1. Install or build the setup scripts.
2. Select the target chain configuration.
3. Generate or fetch the trusted storage-layout manifest for that chain's bridge implementation.
4. Run manifest verification against L2 RPC.
5. Run proxy/implementation verification.
6. Run L1 finalized-read verification for `l1InfoRootMap` and `globalExitRootMap`.
7. Run Geth/Reth proof support checks, depending on the validator's RPC backend.
8. Run a dry-run proof-carrying validation over a recent certificate range.
9. Persist the approved manifest, formula registry, and setup report in validator configuration.

The validator should refuse to start in this mode unless setup has completed successfully for the selected chain.

### L2 Claim Hash-Chain Replay

For imported bridge exits on sovereign L2 bridge contracts, the preferred proposal is to avoid per-claim receipt proofs
for ordering and completeness by using hash-chain state proofs.

Payload requirements:

- Previous boundary L2 block number and hash.
- Final L2 block number and hash.
- Hash-chain formula id for the claimed chain.
- `eth_getProof` account/storage proof for `claimedGlobalIndexHashChain` at the previous boundary block.
- `eth_getProof` account/storage proof for `claimedGlobalIndexHashChain` at the final block.
- If unset claims are in scope, equivalent proofs and formula id for `unsetGlobalIndexHashChain`.
- The imported bridge exits in the exact order the contract uses to update the claimed chain.
- For every imported bridge exit, enough data to compute the bridge leaf hash and to run the existing
  `ImportedBridgeExit.VerifyProofs` checks.
- For unset claims, the unset global indexes in exact contract update order.

Validator replay:

1. Authenticate the previous and final L2 block headers.
2. Verify the previous and final hash-chain state proofs against their corresponding header state roots.
3. Start from the proved previous `claimedGlobalIndexHashChain`.
4. For each imported bridge exit in certificate order:
   - recompute the bridge leaf hash using the same `BridgeExit.Hash()` semantics used by the contracts and AggLayer
     types,
   - compute the hash-chain input for that claim using the formula selected by the payload formula id,
   - update the local accumulator.
5. Require the reconstructed accumulator to equal the proved final `claimedGlobalIndexHashChain`.
6. If unset claims are included, repeat the same boundary-proof and replay process for `unsetGlobalIndexHashChain`.

This would prove that the certificate's imported bridge exits exactly account for the claimed-chain delta between the
previous settled L2 block and the final L2 block. It also catches reordering because hash-chain accumulation is
order-sensitive.

Limits:

- This only applies to bridge implementations that expose and update the hash-chain state.
- This validator mode only supports chains whose bridge contracts expose the required hash-chain state. Older chains keep
  using the current validator mode.
- Legacy `ClaimEvent` paths where full claim data is reconstructed from transaction calldata/traces are out of scope for
  this validator mode. Those chains keep using the current validator mode.
- It does not remove the need to verify source-chain bridge exit Merkle proofs for the imported bridge exits.
- It requires exact agreement on claim ordering. The order should be `(block number, transaction index, log index)` unless
  the contract source proves a different ordering is used.

Formula selection:

- The validator should register a map of supported hash-chain formula ids to formula implementations.
- AggSender should include the formula id used for the certificate in the validator request.
- The validator rejects unknown formula ids.
- AggSender must trim the certificate block range if claims in the candidate range would require more than one formula id.
  A single certificate in the new validator mode must use one formula for the claimed chain and one formula for the unset
  chain.

Concise formula-id spec:

- Use small stable integer ids, not free-form strings, in the validator request.
- Keep separate ids for claimed-chain and unset-chain formulas.
- Each formula implementation defines:
  - required input fields,
  - byte encoding,
  - hash function,
  - accumulator update rule,
  - supported bridge contract family/version.
- AggSender chooses the formula id from chain config or bridge contract version metadata for the candidate block range.
- If a candidate certificate range crosses a formula change, AggSender closes the certificate before the change and starts
  a new one after it.
- The validator does not infer formulas from contract state. It only verifies that the declared ids are registered and
  replays with those formulas.

Unset-chain replay:

- Unset claims should be validated the same way as imported bridge exits:
  - prove previous `unsetGlobalIndexHashChain`,
  - prove final `unsetGlobalIndexHashChain`,
  - replay the unset items included in the certificate using the selected unset formula id,
  - require the replayed final hash to match the proved final hash.

This should preserve ordering and completeness for unset items within the proved boundary range, assuming the formula is
pinned correctly.

Implementation requirement: confirm the exact unset-chain input data for every supported formula id and prove it covers
the current invalid-claim/unclaim trimming behavior in `aggsender/flows/adjust_block_range.go`.

### L1 Info Tree And GER Manager

Current AggSender validation relies on L1 data:

- `GetL1InfoRootByLeafIndex`
- `GetTargetL1InfoRoot`
- `GetProofForGER`
- `DoesGERExistsOnL1`

The imported bridge exit proof verifier already checks Merkle proofs under a selected L1 info root. However, the
selected L1 info root itself must be authenticated if the validator no longer runs an L1 info tree syncer.

For this proposal, assume validators have L1 RPC access and may query L1 on the fly. Under that assumption, the generated
AggLayer GER manager binding exposes the direct read calls needed to avoid syncing L1 events for these checks:

- `l1InfoRootMap(uint32 leafCount) returns (bytes32 l1InfoRoot)`
- `globalExitRootMap(bytes32 globalExitRoot) returns (uint256)`
- `getRoot() returns (bytes32)`
- `getLastGlobalExitRoot() returns (bytes32)`

The validator can therefore:

- Read `l1InfoRootMap(leafCount)` from L1 and require it to equal the selected L1 info root in the payload.
- Treat a non-zero `globalExitRootMap(GER)` value as GER existence, matching the current
  `DoesGERExistsOnL1` implementation.
- Verify the payload's L1 info tree leaf and Merkle proof locally against the selected L1 info root.
- Recompute GER from `mainnetExitRoot` and `rollupExitRoot` for every imported bridge exit.

This removes the need for validators to run an L1 info tree event syncer for existence checks, but it does not remove all
L1 trust and availability requirements. Validators still need reliable L1 RPC access and must query at an L1 block
finality level consistent with the existing configuration.

The intended finality rule is to query L1 at a finalized block tag or finalized block number. Ethereum JSON-RPC block
parameters include the `finalized` tag for state reads such as `eth_call`; the Ethereum execution API defines
`finalized` as the most recent crypto-economically secure block:

- Execution API `eth_call`: <https://ethereum.github.io/execution-apis/api/methods/eth_call/>
- Ethereum JSON-RPC block parameters: <https://ethereum.org/developers/docs/apis/json-rpc/>

Therefore, this is not a special archive-node requirement for L1. It is an L1 RPC/provider capability requirement:

- Preferred path: call `l1InfoRootMap(leafCount)` and `globalExitRootMap(GER)` with `bind.CallOpts` using the
  `finalized` block tag, if the Go RPC stack and provider support it.
- Fallback path: call `eth_getBlockByNumber("finalized", false)` on L1, resolve the finalized block number, then call
  the same contract getters at that explicit finalized block number.
- Setup must verify this by reading known finalized `l1InfoRootMap` and `globalExitRootMap` entries before enabling the
  validator mode.

In the current Go dependency, go-ethereum supports the finalized tag through `rpc.FinalizedBlockNumber`. `ethclient`
serializes negative `BlockNumber` constants as JSON-RPC tags, and `bind.CallOpts.BlockNumber` is a `*big.Int`, so the
direct Go shape is:

- fetch the finalized header with `HeaderByNumber(ctx, big.NewInt(int64(rpc.FinalizedBlockNumber)))`, or
- call generated bindings with `bind.CallOpts{BlockNumber: big.NewInt(int64(rpc.FinalizedBlockNumber))}` and fall back to
  the explicit finalized header number if a provider rejects tag-based `eth_call`.

If the value returned by `l1InfoRootMap(leafCount)` or `globalExitRootMap(GER)` is present at finalized L1 state, the
validator can assert the root or GER is finalized enough for this validation path.

`GetTargetL1InfoRoot` currently chooses the latest processed finalized L1 info root from the local syncer. Without
syncing events, the payload must provide the target `leafCount`, and the validator must validate that
`l1InfoRootMap(leafCount)` is non-zero and equal to the supplied root at finalized L1 state. This proves finalized
existence of that root. The validator does not need to prove it is the latest finalized root unless the certificate rules
explicitly require latest-root selection rather than finalized-root existence.

### Aggchain FEP Scope

The FEP/Aggchain proof verifier reconstructs `AggchainParams` using:

- Aggchain FEP contract state, including `startingBlockNumber`, `latestBlockNumber`, `optimisticMode`,
  selected OP Succinct config, OP Succinct configs, and signer configuration.
- OP node output roots for the last settled block and requested end block.

These are not proven by an L2 bridge contract state proof.

Concession for this proposal:

- The new proof-carrying validator mode will not preserve FEP checks that require OP node access.
- The validator will still perform the same L2 block-hash-dependent checks as the pessimistic proof path:
  - L2 header authentication,
  - L2 bridge state proof verification,
  - local exit root delta replay,
  - claim and unset hash-chain replay where supported,
  - imported bridge exit proof verification.
- The validator will still validate L1-dependent FEP data by querying finalized L1 state:
  - FEP contract identity and configured address,
  - `startingBlockNumber`,
  - `latestBlockNumber`,
  - `optimisticMode`,
  - `selectedOpSuccinctConfigName`,
  - `opSuccinctConfigs(configName)`,
  - signer configuration or trusted signer data used by the selected mode.
- The validator will not query the OP node and will not reconstruct or compare the current full `AggchainParams` hash if
  that reconstruction depends on OP node output roots.

This is an intentional validation-scope reduction for FEP in the new mode. Chains that require the current full FEP
`AggchainParams` equivalence, including OP node output-root checks, must keep using the current validator path until a
separate proof-equivalent design is added.

### AggLayer Previous Certificate State

The validator currently uses previous certificate information to enforce continuity:

- Certificate height increments by one.
- `PrevLocalExitRoot` equals the previous certificate's `NewLocalExitRoot`.
- The first certificate uses the configured initial local exit root.
- The certificate block range starts after the previous settled certificate boundary.

This data is not in the L2 bridge contract state.

For this proposal, validators may query AggLayer on the fly. The new validator mode does not need to remove the existing
AggLayer dependency for previous certificate headers or previous settled block boundaries.

The validator should keep the current continuity checks:

- Previous certificate header lookup from AggLayer.
- Certificate height continuity.
- `PrevLocalExitRoot` equals the previous certificate's `NewLocalExitRoot`.
- The certificate block range starts after the previous settled certificate boundary.

The previous-boundary L2 header used for LER and hash-chain replay must be consistent with the previous settled boundary
returned by AggLayer.

## Validator Checks In The New Path

A proof-carrying validator path should perform these checks.

1. Fetch the selected L2 block header.

   The validator queries L2 by block number or hash for the final L2 block. It rejects the payload if the returned header
   hash does not match the payload block hash. If the security model requires finalized L2 blocks, the validator must also
   enforce the configured finality rule.

   In the new validator mode, the validator also queries L2 for the previous boundary block header. This second header is
   required to authenticate previous local exit root and hash-chain state proofs.

2. Verify L2 account and storage proofs.

   The validator verifies bridge contract account and storage proofs against the L2 block header state root. This binds
   the proved local exit root, deposit count, claimed bitmap words, and any hash-chain state to the selected L2 block.

3. Verify the certificate local exit root.

   The validator checks that the proved bridge `getRoot()` value equals `certificate.NewLocalExitRoot`.

4. Verify bridge leaf inclusion.

   For every bridge exit in the certificate, the validator recomputes the bridge leaf hash and verifies its Merkle proof
   to `certificate.NewLocalExitRoot`.

5. Verify local exit root delta completeness.

   The validator verifies previous and final local exit root state proofs, then replays the ordered bridge exits in the
   certificate from the previous local exit tree state to the final local exit root.

   It also checks that the bridge count matches the proved deposit-count delta, with no duplicate or skipped deposit
   counts.

   This replaces event-log completeness for bridge exits in the new validator mode: if the certificate bridges transform
   the proved previous LER into the proved final LER, the validator has the final-state property AggLayer needs.

   The payload carries `leaf_count` and the previous local exit tree frontier from the bridge syncer. The validator first
   checks the frontier reconstructs the proved previous LER, then uses it to replay append operations statelessly.

6. Verify imported bridge exit proofs.

   The validator keeps the existing `ImportedBridgeExit.VerifyProofs(rootFromWhichToProve)` semantics. This preserves
   proof checks for mainnet and rollup claims, including bridge leaf inclusion under mainnet or rollup roots and L1 info
   tree leaf inclusion under the selected L1 info root.

7. Verify claim occurrence or nullifier state.

   For each imported bridge exit, the validator verifies that the destination-chain claim really exists by the selected
   L2 block.

   For bridge versions with `claimedGlobalIndexHashChain`, the validator should prefer boundary hash-chain replay:

   - verify the previous boundary and final `claimedGlobalIndexHashChain` state proofs,
   - verify the claimed hash-chain formula id is registered locally,
   - replay the certificate's imported bridge exits in contract order,
   - require the replayed final chain to match the proved final chain.

   A bitmap proof remains useful for point membership, but the hash-chain replay is the proposed mechanism for ordering
   and completeness of imported bridge exits between the two proved L2 states.

   Bridge versions that expose only `claimedBitMap` and not the hash-chain state are out of scope for this validator mode
   and keep using the current validator.

8. Verify unset-claim filtering.

   The validator must preserve the current filtering around unset claims and invalid claims.

   For bridge versions with `unsetGlobalIndexHashChain`, the validator should use the same boundary-proof and replay
   approach:

   - verify previous and final `unsetGlobalIndexHashChain` state proofs,
   - verify the unset hash-chain formula id is registered locally,
   - replay unset global indexes in contract order,
   - require the replayed final unset chain to match the proved final chain.

   AggSender must trim block ranges so all unset items in one certificate use the same unset formula id.

   The exact unset-chain input data is pinned by the registered unset formula id.

9. Verify GER and L1 info root behavior.

   The validator recomputes each claim GER from `mainnetExitRoot` and `rollupExitRoot`, verifies the L1 info tree proof,
   and authenticates the selected L1 info root by querying L1.

   With L1 RPC access, the validator can query:

   - `l1InfoRootMap(leafCount)` to assert the selected L1 info root exists in the L1 GER manager contract.
   - `globalExitRootMap(GER)` to assert GER existence where the current code calls `DoesGERExistsOnL1`.

   The validator does not need to sync L1 events for those existence checks.

10. Verify certificate continuity.

    The validator enforces height, previous local exit root, and previous settled block boundary exactly as today.

    For this proposal, querying AggLayer for the previous certificate header and previous settled block boundary is
    acceptable.

11. Run flow-specific checks.

    The pessimistic proof flow has no extra check today.

    For FEP/Aggchain certificates in this new validator mode, the validator must validate only:

    - the same L2 block-hash-dependent proof checks as the pessimistic proof path,
    - finalized L1 reads for FEP contract state used by the certificate mode.

    It must not query the OP node. It also must not claim full equivalence with the current FEP verifier when the current
    verifier's `AggchainParams` hash depends on OP node output roots.

## Can All Validation Be Preserved With Only A New Payload And One L2 Block Query?

### Without A New Payload

No.

The current validator rebuilds the certificate from local bridge, claim, L1 info tree, AggLayer, and flow-specific data.
A certificate plus one L2 block hash query does not contain enough information to reproduce those checks.

### With A New Payload And L2 Header Queries

Partially, but not for all current validation items unless the new payload contains more than L2 storage proofs.

The likely possible parts are:

- Prove selected L2 bridge contract state at the final L2 block.
- Prove `certificate.NewLocalExitRoot` matches the bridge contract root at that block.
- Prove previous and final L2 local exit root state, then replay ordered bridge leaves to validate the local-exit-root
  delta using a frontier witness from the bridge syncer.
- Prove selected claimed bitmap words or, for supported bridge versions, prove previous and final L2 hash-chain state and
  replay the imported bridge exits using the payload's registered formula id.
- Authenticate L1 info root and GER existence through direct L1 contract calls instead of an L1 event syncer.
- Query AggLayer for previous certificate state and settled boundaries.
- Verify bridge leaf Merkle proofs and imported bridge exit proofs locally.

The parts that are not currently proven by one L2 block header plus ordinary `eth_getProof` storage proofs are:

- Historical frontier export from the bridge syncer. The proposal is straightforward, but it requires adding and testing
  a helper that derives the append frontier for an arbitrary previous LER.
- Previous-boundary L2 state without a second L2 query. The new validator mode should make two L2 header queries:
  previous boundary and final block.
- Claim ordering and completeness for bridge versions that do not expose the hash-chain state. Those chains are out of
  scope for this validator mode and keep using the current validator.
- Formula transitions inside one certificate. AggSender must trim the block range so a single certificate does not span
  two different claimed-chain or unset-chain formulas.
- Unset-chain input data until the exact formula inputs are pinned for every supported formula id.
- `ForwardLET` and `BackwardLET` event semantics. This proposal intentionally ignores those event-level checks and
  validates only final local exit tree state.
- Full claim data for legacy `ClaimEvent` paths where data is reconstructed from calldata/traces. Those chains are out of
  scope for this validator mode and keep using the current validator.
- FEP/Aggchain proof checks that depend on OP node output roots. This is an accepted scope reduction for the new mode.

Therefore, the factual conclusion is:

- A proof-carrying payload can reduce validator infrastructure, especially for the pessimistic proof flow.
- It cannot be claimed to preserve all current checks with only a block hash existence query.
- The new validator mode needs two L2 header queries: previous boundary and final block.
- At minimum, the validator needs a payload containing verifiable state proofs, Merkle proofs, hash-chain formula ids, and
  the local exit tree `leaf_count + frontier` witness.
- If local exit root delta replay is supplied with enough append witness data, bridge event completeness can be replaced
  by final-state transition verification.
- If claim hash-chain replay is validated for all supported bridge versions, imported bridge exit ordering can be
  preserved without per-claim receipt proofs.
- With L1 RPC access, L1 info root and GER existence can be validated through smart contract reads instead of local event
  syncing.
- With AggLayer RPC access, previous certificate headers and settled boundaries can keep using the existing dependency.
- To preserve the scoped checks in the new validator mode, the design still needs the bridge syncer frontier helper,
  hash-chain formula pinning, unset-chain formula pinning, and finalized L1 reads for L1-dependent FEP contract state.
  Older bridge versions remain on the current validator. Chains requiring full current FEP OP-node-root validation also
  remain on the current validator.

## Remaining Main Issues

1. Validate the exact production Geth and Reth versions and L2 builds with the required proof-retention settings:
   - Geth: `eth_getProof` plus trie-node history retention for `W`.
   - Reth: `eth_getProof`, `--rpc.eth-proof-window>=W`,
     `--prune.account-history.distance>=W`, and `--prune.storage-history.distance>=W`.
   This is a deployment validation item, not an unresolved design item.
2. Decide the concrete value of the proof retention window `W` for each chain.
3. Generate or vendor Solidity `storageLayout` metadata for supported bridge implementations and commit the validator
   storage-layout manifest. The mechanism is known; the remaining work is selecting and verifying the exact deployed
   implementation versions.
4. Confirm whether the deployed bridge contracts use proxies and choose the implementation-authentication method for each
   chain: proxy implementation slot proof, implementation code hash, or pinned config.
5. Pin the exact `claimedGlobalIndexHashChain` and `unsetGlobalIndexHashChain` update formulas for every supported bridge
   implementation and version, and assign stable formula ids.
6. Confirm exact unset-chain input data per formula id and verify it covers the current invalid-claim/unclaim trimming
   behavior.
7. Validate L1 finalized reads against the intended L1 RPC providers:
   - direct `eth_call` with the `finalized` tag,
   - fallback through `eth_getBlockByNumber("finalized", false)` plus explicit block-number calls.
8. Specify the exact FEP L1 contract reads included in the new scoped FEP mode and ensure all of them are done at
   finalized L1 state.
9. Define failure behavior when proof generation falls outside the full-node state retention window.

## Recommended Implementation Shape

Start with an implementation validation spike before changing validator semantics:

1. Build a small proof generator for one recent L2 block that proves bridge `getRoot()`, deposit count, and one
   `claimedBitMap` word using `eth_getProof`.
2. Generate the first storage-layout manifest and add getter-versus-storage self-checks for the slots used by the proof.
3. Build a standalone verifier that verifies those proofs against an L2 block header.
4. Add the bridge syncer frontier helper and prototype local exit root delta replay from previous LER to final LER using
   ordered bridge leaves and the previous
   frontier from the bridge syncer.
5. Use existing syncer block metadata to select proof blocks and cross-check max included bridge/claim/unclaim blocks.
6. Extend the proof generator to prove `claimedGlobalIndexHashChain` at a previous boundary block and a final block, then
   replay a real imported-bridge-exit sequence using a registered formula id to reconstruct the final hash-chain value.
7. Add equivalent replay for `unsetGlobalIndexHashChain` with a registered formula id.
8. Validate direct L1 reads for `l1InfoRootMap(leafCount)` and `globalExitRootMap(GER)` against the intended L1 RPC
   finality configuration.
9. Measure how far back proof generation works on the intended Geth and Reth full-node configurations.
10. Add scoped FEP validation for finalized L1 FEP contract reads, explicitly excluding OP node output-root checks.
11. Implement the validator setup script bundle and require successful setup before enabling this mode.
12. Add AggSender block-range trimming for hash-chain formula changes.
13. Only after those results, define a versioned validator payload and feature flag.

The first production feature should keep the current validator path as a fallback until every current validation item has
an explicit proof equivalent or is intentionally scoped out.
