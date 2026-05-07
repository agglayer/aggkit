# Audit of `PROPOSAL.md` (Proof-Carrying AggSender Validator Payload)

This document audits each load-bearing claim in `aggkit/PROPOSAL.md` against the
actual code in this working directory (`aggkit`, `agglayer-contracts`,
`agglayer`) and against external standards (EIP-1186, Geth/Reth docs, JSON-RPC
spec). The goal is a trustworthiness verdict on whether the proposal can be
implemented without unexpected blockers, or whether it has fundamental flaws.

The audit is organized in three parts:

1. Per-claim validation (every concrete assumption the proposal makes).
2. Issues the proposal does not call out, ranked by severity.
3. Final verdict.

References to file paths use `repo/path/file.go:line`. Web sources are listed
inline.

---

## 1. Per-claim validation

### 1.1 "Current validator rebuilds the certificate from independent sources"

**Verdict: confirmed.**

`aggkit/aggsender/validator/validate_certificate.go:51` (`ValidateCertificate`)
performs exactly the chain described in the proposal:

- `certQuerier.GetLastSettledCertificateToBlock(...)` (line 56).
- `validateLastL2BlockInCert` (line 62) and `checkContigousCertificates`
  (line 67), with the height + LER continuity check at line 150-158, plus
  `checkFirstCertificateBlocks` at line 184.
- `getCertificatePreBuildParams` calls
  `l1InfoTreeDataQuerier.GetL1InfoRootByLeafIndex` (line 216).
- `flow.GenerateBuildParams` + `flow.BuildCertificate` reconstruct an
  expected certificate, which is then compared via `compareCertificates`
  (line 97 → line 164).
- `verifyClaimProofs` calls `ImportedBridgeExit.VerifyProofs` (line 121-132,
  delegating to `aggkit/agglayer/types/types.go:1212`).
- `flow.VerifyCertificate` runs flow-specific checks (line 110-115). The
  pessimistic-proof flow's `verifier_flow_pp.go` is empty of extra checks;
  the FEP flow at `aggkit/aggsender/flows/verifier_flow_aggchain_prover.go:33`
  reconstructs `AggchainParams` and compares the hash.

The validator description in the proposal matches code one-to-one.

### 1.2 "L2 bridge contract exposes `claimedGlobalIndexHashChain` and `unsetGlobalIndexHashChain` and the formulas described"

**Verdict: confirmed.**

`agglayer-contracts/contracts/sovereignChains/AgglayerBridgeL2.sol`:

- Public state variables declared at lines 65 and 70.
- Update site for the claimed chain (line 1424-1428):

  ```solidity
  claimedGlobalIndexHashChain = Hashes.efficientKeccak256(
      claimedGlobalIndexHashChain,
      Hashes.efficientKeccak256(bytes32(globalIndex), leafValue)
  );
  ```

  This is exactly the formula the proposal cites
  (`keccak(prev, keccak(globalIndex, leafValue))`).

- Update site for the unset chain (line 673-677):

  ```solidity
  unsetGlobalIndexHashChain = Hashes.efficientKeccak256(
      unsetGlobalIndexHashChain,
      bytes32(globalIndex)
  );
  ```

  i.e. `keccak(prev, globalIndex)` — the unset formula deliberately does
  **not** include the leaf value, so the proposal's claim that the two
  formulas are different and must be pinned independently is correct.

- The events `UpdatedClaimedGlobalIndexHashChain` and
  `UpdatedUnsetGlobalIndexHashChain` exist at lines 177 and 187.
- `claimedBitMap`, `isClaimed`, `BridgeEvent`, `ClaimEvent`,
  `DetailedClaimEvent`, `SetClaim`, `BackwardLET`, `ForwardLET` are all
  present (`AgglayerBridge.sol:77,122,136,1185`,
  `AgglayerBridgeL2.sol:65,70,177,187,196,200`,
  `aggkit/bridgesync/downloader.go:41-42`).

### 1.3 "`leafValue` for the claimed-chain formula equals the bridge leaf hash used in the LET"

**Verdict: confirmed, but with a correctness caveat for implementers.**

The contract leaf value is computed by `getLeafValue(...)` in
`agglayer-contracts/contracts/lib/DepositContractV2.sol:22-43` as
`keccak256(abi.encodePacked(leafType, originNetwork, originAddress,
destinationNetwork, destinationAddress, amount, metadataHash))`.

`AgglayerBridge.sol:1424-1428` feeds that exact value into the hash chain.

The aggkit equivalent is `BridgeExit.Hash()` at
`aggkit/agglayer/types/types.go:646-668`. The two should produce the same
32-byte output, **but** the Go side feeds `metadataHash` differently when
metadata is empty (`EmptyBytesHash`) vs. non-empty (raw `Metadata` bytes).
Anyone implementing the formula must verify with vector tests that
`BridgeExit.Hash()` and the contract's `getLeafValue(...)` produce identical
values for every leaf in scope. The proposal's instruction to "pin the exact
hash-chain update formula for every supported deployed bridge implementation
and version" already covers this if taken seriously, but it is the most
likely place to silently regress.

### 1.4 "L1 GER manager exposes `l1InfoRootMap`, `globalExitRootMap`, `getRoot`, `getLastGlobalExitRoot`"

**Verdict: confirmed.**

`agglayer-contracts/contracts/AgglayerGER.sol`:

- `mapping(uint32 leafCount => bytes32 l1InfoRoot) public l1InfoRootMap;`
  (line 33).
- `getLastGlobalExitRoot()` (line 144).
- `getRoot()` overriding `DepositContractBase` (line 155).
- `globalExitRootMap` is a `mapping(bytes32 => uint256)` declared in
  `lib/LegacyAgglayerGERBaseStorage.sol:19`. The value stored on insertion
  is `lastBlockHash` (`AgglayerGER.sol:114`), so a non-zero value really
  does signal "GER exists", matching the existing
  `DoesGERExistsOnL1` implementation at
  `aggkit/aggsender/query/l1info_tree_data_query.go:233-241` which checks
  `gerIndex.Cmp(common.Big0) == 1`.

The Go binding `agglayerger.Agglayerger` is already in use in the repo, so
the proposal's "validator can replace the L1 info-tree event syncer with
direct contract reads" is mechanically possible today.

### 1.5 "Syncer already attaches block metadata used to pick proof blocks"

**Verdict: confirmed.**

- `aggkit/tree/types/types.go:19` declares
  `Root{Hash, Index uint32, BlockNum uint64, BlockPosition uint64}`.
- `aggkit/bridgesync/bridgesync.go:408` defines
  `GetRootByLER(ctx, ler) (*tree.Root, error)`.
- `aggkit/bridgesync/bridgesync.go:420` defines
  `GetExitRootByIndex(ctx, index) (tree.Root, error)`.
- `Bridge`, `Claim`, and unset rows all carry `BlockNum` / `BlockPos` /
  `LogIndex` (`aggkit/bridgesync/processor.go:111-145` and tests under
  `aggkit/aggsender/query/bridge_query.go:210-214`).

The proposal's claim that proof blocks can be derived from syncer state
without an additional event scan is supported.

### 1.6 "Append-only tree frontier helper is a small addition, not a new indexing requirement"

**Verdict: feasible but not as small as implied.**

The append-only tree state required to "stateleslly" replay leaves is
held privately in `_branch[_DEPOSIT_CONTRACT_TREE_DEPTH]` in the contract
(`agglayer-contracts/contracts/lib/DepositContractBase.sol:40`). The Go
side caches the equivalent in
`aggkit/tree/appendonlytree.go:23` (`lastLeftCache`).

Two facts in the existing code support the helper:

- Tree nodes are stored permanently keyed by hash (`tree.go:135-146`,
  `getRHTNode` at line 119) and **not** garbage-collected on tree growth.
  Reorg/backward operations only delete from the `root` table, not from
  `rht` (lines 237-253). So the underlying data needed to reconstruct a
  frontier for any historical root is on disk as long as the corresponding
  root entry has not been reorged away.
- `initCache` (`appendonlytree.go:94-135`) already walks from the latest
  root via `getRHTNode` to rebuild the canonical left-frontier. The same
  walk works for any historical root if `GetLastRoot(tx)` is replaced by
  `GetRootByHash(ctx, ler)` (already exposed at `tree.go:169-183`).

Mathematical soundness of the validator's proposed check is sound: given a
canonical (zero-padded at unused heights) frontier `F` and `leaf_count = n`,
the function `getRoot(F, n)` is determined by `F` and `n` — finding a
distinct `F'` with the same root reduces to a keccak collision. So the
validator can accept any (F, n) that recomputes the proved previous LER.

Caveats the proposal does not enumerate:

- **Reorg interaction.** The current `Reorg(tx, firstReorgedBlock)` only
  drops `root` rows. After a reorg, the `rht` table keeps stale tree nodes
  that may have hash collisions with future re-additions of identical
  subtrees, but that is fine because the helper is keyed by *hash* and only
  walks from a `Root` that still exists. However, if the previous-boundary
  LER from the *AggLayer*-side certificate header points to a root that has
  been reorged off the local `root` table, the helper will fail. The
  AggSender-side range-trim logic at the proof-window boundary needs to
  handle this case explicitly.
- **Helper must support arbitrary historical roots.** The proposal admits
  this in passing (line 328-329) but it is not a one-line change: code
  paths that today assume "we only ever rebuild from the latest root"
  exist (`appendonlytree.go:94-135`). Any concurrent write while the
  helper is walking a historical root could see partial state without
  proper transaction boundaries.

The helper is implementable; it is "small" only in the sense that no new
data needs to be indexed.

### 1.7 "Imported bridge exit proof verification is preserved"

**Verdict: confirmed.**

`ImportedBridgeExit.VerifyProofs(rootFromWhichToProve)` at
`aggkit/agglayer/types/types.go:1212-1236` is purely a function of the
already-marshalled certificate fields and the L1 info root supplied by the
caller. Once the validator authenticates the L1 info root via
`l1InfoRootMap(leafCount)`, the existing call is unchanged. The proposal's
claim that this check survives unmodified is correct.

### 1.8 "FEP/Aggchain proof verifier reconstructs `AggchainParams` using L1 FEP contract data and OP-node output roots"

**Verdict: confirmed; the scope reduction is real and significant.**

`aggkit/aggsender/flows/verifier_flow_aggchain_prover.go:33-83` calls
`fepInputsQuery.GetAggchainParams(...)`, which in turn (via
`aggkit/aggsender/query/agg_proof_public_values_query.go:48-92`) reads:

- `OutputAtBlockRoot(lastProvenBlock)` and
  `OutputAtBlockRoot(requestedEndBlock)` — both **OP-node specific** RPCs.
- `aggchainFEPContract.SelectedOpSuccinctConfigName(nil)` and
  `aggchainFEPContract.OpSuccinctConfigs(nil, configName)` — L1 contract
  reads (note: with `nil` CallOpts → "latest" block, **not** finalized
  today).
- Optionally `GetAggchainSigners(nil)` for the trusted signer.

`AggchainParams.Hash()` at `aggkit/aggsender/types/fep_inputs.go:119-147`
takes 8 inputs; **two of them (`L2PreRoot`, `ClaimRoot`) come exclusively
from the OP node**. Without OP-node access the new mode literally cannot
recompute the hash and therefore cannot detect a malicious AggSender that
forges the `AggchainParams` field.

The proposal acknowledges this (lines 614-647 and 824) and explicitly
recommends FEP chains stay on the current validator path. That is honest,
but means: **for FEP chains, the proof-carrying mode is strictly weaker
than today.** Calling it "an intentional validation-scope reduction" is
fair; calling it "the same validator guarantees" would not be.

### 1.9 "EIP-1186 / `eth_getProof` and verification against block header `stateRoot`"

**Verdict: confirmed.**

EIP-1186 specifies the response format (`accountProof`, `storageProof`,
balances, nonce, storage hash, code hash) and the verification path:
walk from `stateRoot` along `keccak(address)` for the account proof, and
along `keccak(slot)` from the account's `storageHash` for storage. Off-line
verification against a trusted block hash / header `stateRoot` is exactly
what the proposal proposes.

The proposal's statement that "if the validator only verifies that a block
hash exists, it cannot verify storage proofs" is correct: the proof root is
the header `stateRoot`, not the block hash. So pairing each storage proof
with the matching authenticated header is mandatory, which the proposal
already requires.

Source: <https://eips.ethereum.org/EIPS/eip-1186>.

### 1.10 "Geth/Reth historical proof retention requires explicit configuration"

**Verdict: confirmed at the documentation level — but the operational
picture is worse than the proposal admits.**

Geth: from v1.17.x, historical Merkle proofs require `--history.trienode=N`.
Default is `-1` (no retention). Without it, archive mode stores flat states
but no historical trie nodes, so historical `eth_getProof` is unsupported.
Source: <https://geth.ethereum.org/docs/fundamentals/archive>.

Reth: by default Reth runs in archive mode; the documented full-node mode
keeps the last 10,064 blocks. Historical proof generation has known
production issues:

- `paradigmxyz/reth#15142` reports that setting
  `--rpc.eth-proof-window=100000` and querying old blocks results in
  database read-transaction timeouts on archive mainnet nodes:
  <https://github.com/paradigmxyz/reth/issues/15142>.
- The OP-Reth `op-rs/op-reth` fork explicitly exists *because* serving
  `eth_getProof` for blocks up to ~7 days back requires loading thousands
  of changesets into memory and frequently OOMs upstream Reth. The
  proposed sidecar (~1 TB extra storage for ~4 weeks of Base-Testnet) is
  forward-only — it cannot backfill, so a freshly-restarted node cannot
  serve proofs for the window before initialization.
  Source: <https://github.com/op-rs/op-reth>.

What the proposal gets right:

- It does not claim that "any full node will work."
- It tells operators to set explicit retention flags and to feature-test
  proof generation at startup at both a recent block and a block at least
  `W` blocks back.

What the proposal misses:

- Mainline Reth has a **known correctness bug** at non-trivial proof
  windows (see issue 15142). The proposal treats Reth as ready and
  assigns the "validate exact production versions" task to a deployment
  step (Issue 1 in "Remaining Main Issues"). For chains relying on Reth-
  family RPC, this is currently an unresolved upstream blocker, not just a
  configuration question. It should be reclassified as a hard precondition.
- The OP-Reth sidecar's forward-only nature creates an operational footgun:
  validators that cold-start cannot validate certificates whose
  previous-boundary block is older than their sidecar's start block. The
  proposal's "AggSender should trim certificate ranges that would require
  a previous-boundary proof older than the configured proof window"
  (line 153-154) is per-AggSender; the validator-side equivalent (refuse
  to start when the sidecar can't cover `W`) is not specified.
- Storage cost is non-trivial. ~1 TB for a 4-week window on a low-throughput
  L2 (Base Testnet) is a meaningful operator constraint that should appear
  in the proposal, not just be implied.

### 1.11 "`eth_call` supports the `finalized` block tag, with go-ethereum's `rpc.FinalizedBlockNumber`"

**Verdict: confirmed.**

The Ethereum execution API and JSON-RPC docs both list `finalized` as a
valid block parameter for state reads (`eth_call`, `eth_getBlockByNumber`,
etc.). Go-ethereum exposes `rpc.FinalizedBlockNumber` and aggkit already
uses it in production paths:

- `aggkit/l1infotreesync/l1infotreesync.go:466`:
  `l1Client.HeaderByNumber(ctx, big.NewInt(int64(rpc.FinalizedBlockNumber)))`.
- `aggkit/etherman/rpcopnode.go:63`:
  `if number != nil && rpc.BlockNumber(number.Int64()) == rpc.FinalizedBlockNumber`.
- `aggkit/types/block_finality.go:308`: a typed `Finalized` constant.

The CLAUDE.md instruction to use `BlockNumberFinality` for block references
is consistent with the proposed approach.

Caveat: the *current* `DoesGERExistsOnL1` call uses
`&bind.CallOpts{Pending: false}`, which is "latest", not "finalized". The
proposal correctly notes this needs to change. Provider compatibility
varies — some public endpoints don't honor the `finalized` tag at all — so
the "fallback path: resolve finalized block number then call at that
explicit block" pattern (line 590-593) is not theoretical, it is required
in practice. Aggkit already uses both forms.

Sources:
- <https://ethereum.github.io/execution-apis/api/methods/eth_call/>
- <https://ethereum.org/developers/docs/apis/json-rpc/>

### 1.12 "Bridges deployed via TransparentUpgradeableProxy → EIP-1967 implementation slot is available"

**Verdict: confirmed.**

`agglayer-contracts/deployment/v2/3_deployContracts.ts:296` and
`1_createGenesis.ts:211` reference the OpenZeppelin
`TransparentUpgradeableProxy`. The EIP-1967 implementation slot is the
standard `0x360894...382bbc` (referenced in
`contracts/lib/TokenWrappedTransparentProxy.sol:86`). So the proposal's
proposed implementation-authentication-via-EIP-1967-slot path is
mechanically valid for Polygon-style deployments.

### 1.13 "Solidity compiler emits `storageLayout`; storage slots can be derived deterministically"

**Verdict: confirmed.**

The Solidity docs document the JSON-output `storageLayout` schema with
`label`, `slot`, `offset`, `type` entries:
<https://docs.solidity.org/en/latest/internals/layout_in_storage.html#json-output>.

The contracts in `agglayer-contracts/foundry.toml` and
`hardhat.config.ts` use solc, so generating `storageLayout` is a
build-flag away. For the variables the proposal needs
(`claimedGlobalIndexHashChain`, `unsetGlobalIndexHashChain`, `depositCount`,
`_branch`, `lastUpdatedDepositCount`), the layout is straightforward
because they are scalar / fixed-array slots in a known inheritance chain.

Caveat: `_branch` is `bytes32[32]` and lives in the inheritance chain
(`DepositContractBase` → `AgglayerBridge` → `AgglayerBridgeL2`). Because
the proposal's preferred mechanism does **not** prove `_branch` (it uses an
AggSender-supplied frontier and verifies it via root recomputation), this
caveat does not actually matter. But operators should resist the
temptation to "prove `_branch` from storage" — the slot offsets shift
whenever an upstream contract in the chain changes its `__gap`.

### 1.14 "Validator continues to query AggLayer for previous-certificate state"

**Verdict: confirmed and a sensible scoping choice.**

`aggsender/validator/validator_service.go:72-90` uses
`agglayerClient.GetCertificateHeader(ctx, prevID)` today. Keeping that
dependency avoids reproducing AggLayer's own height/continuity bookkeeping
through L1 storage proofs (which would not be possible — that state is
internal to AggLayer, not the bridge contract).

---

## 2. Issues the proposal does not adequately address

These are ranked by severity for the question "can this be implemented
without unexpected blockers."

### 2.1 (High) The proof-window blocker for Reth is upstream and unresolved

The proposal frames Geth/Reth proof retention as a deployment-time
verification task. For Reth specifically, the upstream client has a known
correctness bug serving historical proofs at meaningful windows
(`paradigmxyz/reth#15142`) and the de-facto solution (OP-Reth sidecar)
trades ~1 TB of disk for ~4 weeks of history with no backfill. This is not
a small "decide `W` for each chain" item; for Reth-backed L2s it can mean
the validator mode cannot run on stock Reth at all, only on a forked
sidecar build. The proposal should reclassify this as a hard precondition
and call out the OP-Reth dependency explicitly.

### 2.2 (High) FEP chains lose `AggchainParams` validation entirely

The proposal admits this in the FEP scope section, but it is easy to read
the document and miss it. In practical terms: for chains using the
Aggchain/FEP flow, the proof-carrying validator does **not** detect a
malicious AggSender that forges `L2PreRoot` or `ClaimRoot` in
`AggchainParams`. The only defense for those fields becomes the AggLayer
SP1/aggchain proof verification itself — which is a separate trust anchor.
Operators evaluating "should I switch?" need the document to state this in
plain language: *for FEP chains, the new mode preserves L2-bridge-state
checks but drops half of the AggchainParams check*.

### 2.3 (Medium) `BackwardLET` / `ForwardLET` interaction with delta replay

The proposal explicitly ignores backward/forward LET event semantics
(line 297-301 and 820-821). In practice this means: any certificate range
that crosses a `BackwardLET` cannot be validated by the new mode, because
append-only replay of forward bridge exits cannot reach a final LER that
was reduced and re-grown. This is fail-closed, so it is safe, but it is a
real availability constraint for sovereign chains where the emergency
unwind has been used. The proposal should specify the AggSender behavior
("close the certificate at the LET event, restart after") rather than
leaving it implicit.

### 2.4 (Medium) Frontier helper interacts with reorg state

`tree.Reorg(tx, firstReorgedBlock)` only deletes from the `root` table,
not the `rht` table (`aggkit/tree/tree.go:237-253`). The proposed
`GetAppendFrontierByLER(ler)` looks up the root by hash. If a reorg has
deleted the matching `root` row but the same LER was later re-indexed at a
different `(blockNum, blockPosition)`, the helper still works. If the LER
was reorged off but never re-emerged, the helper returns "not found". The
validator must distinguish "previous boundary not yet observed by my
syncer" from "previous boundary was reorged" — currently both look the
same. The proposal does not say how the validator behaves in that case
(retry, defer, error). Acceptable but should be specified.

### 2.5 (Medium) `BridgeExit.Hash()` vs. `getLeafValue(...)` parity is not just "pin the formula"

The proposal asks implementers to "pin the exact hash-chain update formula
for every supported deployed bridge implementation." That sounds like it
covers everything, but the *actual* sensitive input is the bridge leaf
hash, not the chain step. The two implementations differ in metadata
handling (Go uses `EmptyBytesHash` for empty metadata; the contract takes a
`metadataHash` parameter computed by the caller as `keccak256(metadata)`).
Implementers must add a vector test that re-imports a real on-chain claim
and produces an identical leaf value via `BridgeExit.Hash()`. This is the
single highest-risk-of-silent-divergence point in the design.

### 2.6 (Low) Receipt/log proofs for "block-range completeness" are still open

The proposal correctly notes that `eth_getProof` is a state proof, not a
log proof, and marks log/receipt completeness as **Needs research**.
For the new mode this is mostly fine because hash-chain replay subsumes
ordering and completeness for *imported* bridge exits, and LER delta
replay subsumes it for *outgoing* bridge exits. The remaining open item is
the unset-chain formula coverage of "current invalid-claim/unclaim
trimming behavior" (proposal line 545-547). Until that mapping is written
down, the new mode cannot claim parity with `adjust_block_range.go`'s
current trimming.

### 2.7 (Low) Provider compatibility for the `finalized` tag is real

Some hosted L1 providers do not honor `finalized` for `eth_call`. The
proposal already mentions a fallback ("resolve `finalized` block number
then call at the explicit number"), but does not specify which path the
setup script must verify; it should require that *both* paths pass at
startup so a provider that silently downgrades `finalized` to `latest`
(returning newer, unfinalized state) is detected.

### 2.8 (Low) Nullifier tree confusion

The proposal notes (line 226-228, **Needs research**) that the contract
shows a bitmap and a hash chain but no Merkle nullifier tree. This is
correct: the Merkle nullifier tree exists only inside the AggLayer
pessimistic-proof core
(`agglayer/crates/pessimistic-proof-core/src/nullifier_tree.rs:16`,
64-bit depth keyed by `(networkId, letIndex)`), not on any L2 contract.
There is no pending discovery here — the absence is structural. The
proposal can simply remove the "Needs research" tag and state the fact.

---

## 3. Verdict

The proposal is unusually careful for a design document. It correctly
describes current validator behavior, correctly identifies which contract
state can prove what, and correctly scopes out the parts that fundamentally
cannot be proved with `eth_getProof` alone (FEP/AggchainParams,
event-ordering for legacy chains, OP-node output roots).

Spot checks against the codebase support every load-bearing claim:

- The current validator code paths exist as described.
- The L2 bridge contract exposes the hash-chain state and the proposal's
  formulas match the on-chain code byte for byte.
- The L1 GER manager exposes `l1InfoRootMap` / `globalExitRootMap` exactly
  as described, and existing aggkit code already uses the binding.
- Syncer block metadata is sufficient to pick proof blocks.
- The append-frontier helper is implementable from existing tree state.
- EIP-1967 proxies, the `finalized` JSON-RPC tag, and `eth_getProof` are
  all real and supported in aggkit's existing dependency stack.

**There is no fundamental design flaw.** The cryptographic argument
(authenticated header ⇒ verified state root ⇒ verified storage slots ⇒
replay against bridge formulas) is sound. The validation reductions the
proposal accepts (LET delta replay subsumes bridge events; hash-chain
replay subsumes claim ordering) are mathematically equivalent for the
chains that expose the required state.

**The unexpected-blocker risk is concentrated in two places, both
operational rather than architectural:**

1. **Reth historical-proof support is not production-ready upstream.**
   Anyone running the new validator mode on a Reth-family L2 RPC will hit
   either the open issue 15142 timeout, OOM behavior on long windows, or
   the OP-Reth sidecar's storage / forward-only constraints. This is the
   single most likely reason a deployment of this design will fail to
   meet expectations. It should be elevated from "deployment validation"
   to "hard precondition with explicit fallback strategy."
2. **For FEP chains the new mode is strictly weaker than the current
   validator.** The proposal admits this; consumers must read carefully
   to notice. Operators should be told plainly: *do not enable this mode
   on FEP chains unless you accept that AggchainParams forgery becomes
   undetectable at the validator layer.*

Subject to those two operational caveats, the design is implementable.
The recommended implementation shape (start with a spike: prove `getRoot`
and one bitmap word, build a verifier, then scale up to hash-chain replay
and L1 finalized reads) is consistent with what a careful team would do
anyway. The most likely silent regression — `BridgeExit.Hash()` vs. the
contract's `getLeafValue(...)` — should be locked down with vector tests
in the spike, before any payload format is frozen.

**Overall trustworthiness rating:** the proposal can be implemented as
specified for the pessimistic-proof flow on chains whose L2 RPC backend
either is Geth with `--history.trienode=W` or has a working historical-
proof story. For FEP chains and Reth-only deployments, do not commit to
the new path until the two highlighted operational issues are resolved
upstream.
