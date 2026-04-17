# SPEC: bridgesync

## Summary

Bridge sync is the subsystem that mirrors the state of a bridge smart contract into a local SQLite database by consuming EVM logs. It produces and maintains a local append-only exit tree whose leaves correspond to deposit events observed on the configured bridge contract, and it persists related auxiliary records (token mappings, legacy-token migrations, backward/forward local-exit-tree transitions, and archived bridges) so that consumers can answer bridge-level queries (proofs, paged listings, lookup by deposit count or content) without re-reading the chain.

Two syncer flavors are provided. An L1 flavor tracks a mainnet bridge contract and a local exit tree; an L2 flavor additionally supports a *sovereign* L2 bridge whose local exit tree can be rewound (BackwardLET) or advanced (ForwardLET) by on-chain events, and is seeded with a caller-supplied initial local exit root. Both flavors expose the same read surface and share the same storage shape, differing only in which event kinds they accept and in how the exit tree's state is allowed to mutate.

A bridge syncer is halted when it detects a divergence between persisted state and the exit tree that only a reorg can resolve; in that state it refuses all read and block-processing requests until the reorg detector triggers a rewind that restores consistency.

## Requirements

### Construction and lifecycle

- **1.** A bridge syncer MUST refuse to construct if it cannot build a contract binding for the configured bridge address.
- **2.** A bridge syncer MUST refuse to construct if it cannot read a basic deposit-count sentinel from the configured bridge contract (the sanity check proving the address is a live bridge).
- **3.** A bridge syncer MUST refuse to construct if it cannot determine, by probing the live contract, whether the deployment is a non-sovereign (L1-style) or a sovereign (L2-style) bridge.
- **4.** A bridge syncer MUST refuse to construct if it cannot open or migrate the configured SQLite database to the current schema (see `bridgesync/migrations/SPEC.md#1`).
- **5.** When construction fails for any reason, the syncer MUST return an error to the caller and MUST NOT expose a partially-initialised instance.
- **6.** An L2 bridge syncer MUST accept a caller-supplied initial local exit root and MUST treat that value as the "empty tree" marker when deciding whether the persisted exit tree is consistent with a ForwardLET's or BackwardLET's `PreviousRoot`.
- **7.** An L1 bridge syncer MUST use the empty-LER sentinel defined in `bridgesync/types/SPEC.md#10` as its initial local exit root.
- **8.** A bridge syncer MUST record, as part of its database runtime-compatibility metadata, the chain ID reported by the RPC endpoint, the set of addresses it reads from, the current database schema version, and the effective FromAddress-extraction mode for the database.
- **9.** On startup against a database whose recorded schema version differs from the current schema version, a bridge syncer MUST refuse to proceed and MUST require the operator to recreate the database.
- **10.** On startup against a database whose recorded chain ID or contract-address set differs from the current configuration, a bridge syncer MUST refuse to proceed (delegated to the shared DB-compatibility check).
- **11.** On startup against a legacy database that has no recorded FromAddress-extraction mode, the syncer MUST treat the recorded mode as enabled (historical default) and MUST persist the current mode to the database.
- **12.** A bridge syncer MUST NOT forbid downgrading the FromAddress-extraction mode from enabled to disabled; it MUST proceed but SHOULD emit a warning that newly-recorded bridges will not carry `from_address`.
- **13.** A bridge syncer MUST NOT forbid upgrading the FromAddress-extraction mode from disabled to enabled; it MUST proceed but SHOULD emit a warning that a background backfill will be needed to populate missing values.

### Event ingestion

- **14.** A non-sovereign (L1-style) bridge syncer MUST ingest, at minimum, bridge deposit events and wrapped-token-creation events emitted by the configured bridge contract.
- **15.** A sovereign (L2-style) bridge syncer MUST additionally ingest sovereign-token-registration events, legacy-token migration events, legacy-token removal events, backward-LET events, and forward-LET events emitted by the configured bridge contract.
- **16.** For every persisted bridge record the syncer MUST record: block number, block position, transaction hash, block timestamp, leaf type, origin network and address, destination network and address, amount, metadata, deposit count, transaction sender (`txn_sender`, the outermost tx signer), recipient (`to_address`, the outermost tx recipient), and a provenance marker (`source`) identifying whether the row originated from a direct bridge event, a ForwardLET replay, or an archived-then-restored BackwardLET rollback.
- **17.** For a bridge deposit event, the syncer MUST attempt to populate the deposit initiator (`from_address`) according to the event's leaf kind: for message-kind leaves (see `bridgesync/types/SPEC.md#1`) it MUST take the initiator from the event's origin address; for asset-kind leaves it MUST take the initiator from the transaction sender when the transaction's immediate recipient is the bridge contract itself.
- **18.** When an asset-kind deposit is initiated indirectly (the transaction's immediate recipient is not the bridge contract), the syncer MUST populate `from_address` by tracing the transaction and locating the nested call that satisfies the bridge-deposit method signature and whose decoded parameters match the emitted event.
- **19.** When FromAddress extraction is disabled, the syncer MUST NOT issue a transaction-trace RPC for asset-kind deposits and MUST persist `from_address` as NULL for those rows; `txn_sender` and `to_address` MUST still be populated from the standard transaction lookup.
- **20.** When tracing locates multiple bridge-contract calls that all agree on the caller, the syncer MUST use that common caller as `from_address`; when they disagree, the syncer MUST disambiguate using the decoded call parameters (leaf type, destination network, destination address, amount) against the emitted event, falling back to origin-token equality when multiple candidates remain.
- **21.** A bridge syncer MUST NOT persist any bridge record from a transaction trace call that was reverted.
- **22.** A bridge syncer MUST process blocks transactionally: either every on-disk effect of a block (including exit-tree mutations, inserts, archives, deletes, and sanity-checked LET transitions) is committed, or none of them is.
- **23.** A bridge syncer MUST publish the block number of any block whose commit included at least one new bridge row to the new-bridge subscription stream, and MUST NOT publish blocks that committed no new bridge rows.
- **24.** A bridge syncer MUST forward every committed block (with or without bridge events) to the sync-subscription stream.

### Exit tree consistency

- **25.** Every persisted bridge record MUST correspond to exactly one leaf inserted into the local exit tree, whose index is the bridge's `deposit_count` and whose hash is the bridge's canonical hash.
- **26.** On a ForwardLET event, the syncer MUST verify that the event's `PreviousRoot` equals the current local-exit-tree root (treating the initial-LER value as the empty tree) before applying the event, and MUST verify that the event's `NewRoot` equals the resulting local-exit-tree root after applying all decoded leaves; on either mismatch the block transaction MUST be rolled back.
- **27.** On a BackwardLET event, the syncer MUST verify that the event's `PreviousRoot` equals the current local-exit-tree root before applying the event, and MUST verify that the event's `NewRoot` equals the resulting root after the rewind; on either mismatch the block transaction MUST be rolled back.
- **28.** On a BackwardLET to deposit-count `N`, every bridge row with `deposit_count >= N` MUST be moved to the archive with its provenance marker set to the BackwardLET source, and MUST be removed from the active bridge table; the local exit tree MUST be rewound so that exactly `N` leaves remain.
- **29.** On a ForwardLET event, for each decoded new leaf the syncer MUST look up an archived bridge whose content fields (leaf type, origin network/address, destination network/address, amount, metadata) match the leaf; if exactly one match is found it MUST restore that bridge's identifying fields (`tx_hash`, `txn_sender`, `from_address`) into the new bridge row; if zero or more than one match is found it MUST insert the new bridge row with those fields empty and SHOULD log a warning.
- **30.** On a reorg to block `F` reported by the reorg detector, the syncer MUST delete every `block` row with `num >= F` (cascading to all child rows by foreign-key), MUST rewind the exit tree to the pre-`F` state, MUST re-insert every bridge previously archived by a BackwardLET that touches the reorged range provided that bridge would not itself be cascade-deleted, and MUST clear its halted state if any row was affected.
- **31.** A bridge syncer MUST halt itself if it detects an exit-tree index collision (an attempt to write a leaf at an index that is not the next free index) during block processing.
- **32.** While halted, a bridge syncer MUST reject every read request and every block-processing request with a typed "inconsistent state" error and MUST NOT return partial data; it MUST leave the halted state only when a reorg reaches a block range that restores consistency.

### Read surface

- **33.** Every read operation on a bridge syncer MUST observe the halted state and MUST refuse to return results when halted.
- **34.** Paged read operations MUST reject page numbers of zero and page sizes of zero with typed errors (`ErrInvalidPageNumber`, `ErrInvalidPageSize`).
- **35.** A bridge-by-deposit-count lookup MUST search the active bridge table first and fall back to the archive; a deposit count present in neither MUST be reported as not-found.
- **36.** A bridge-by-content lookup MUST search both the active bridge table and the archive and return the union.

### Backfill

- **37.** A separate backfill operation MUST be available that, given an existing bridge-sync database, populates missing `txn_sender`, `to_address`, and (when FromAddress extraction is enabled) `from_address` on rows that do not already have them, by re-deriving those values from the original transaction using the same extraction rules as live ingestion (#17–#20).
- **38.** The backfill operation MUST NOT reprocess rows whose provenance marker indicates they originated from a LET event (ForwardLET or BackwardLET), because those rows' identifying fields are derived at restore time, not from an on-chain transaction of their own.
- **39.** The backfill operation MUST write each field only when its current value is NULL or empty; it MUST NOT overwrite an already-populated value.
- **40.** The backfill operation MUST be responsive to context cancellation between batches and MUST leave the database in a consistent state if cancelled mid-run (no partial-batch writes).

## External interface

Exported Go API consumed by other aggkit packages. Types and constants in `bridgesync/types/` are re-exported indirectly through these signatures.

- `NewL1(ctx, cfg, reorgDetector, ethClient, originNetwork) (*BridgeSync, error)` — construct a non-sovereign bridge syncer.
- `NewL2(ctx, cfg, reorgDetector, ethClient, originNetwork, syncFullClaims, initialLER) (*BridgeSync, error)` — construct a sovereign bridge syncer with an initial LER.
- `(*BridgeSync).Start(ctx)` — blocking sync loop; returns when `ctx` is done.
- `(*BridgeSync).GetBridgesPaged(ctx, page, pageSize, depositCount, networkIDs, fromAddress) ([]*Bridge, int, error)`.
- `(*BridgeSync).GetBridges(ctx, fromBlock, toBlock) ([]Bridge, error)`.
- `(*BridgeSync).GetBridgeByDepositCount(ctx, depositCount) (*Bridge, error)`.
- `(*BridgeSync).GetBridgesByContent(ctx, leafType, originAddress, destinationNetwork, destinationAddress, amount, metadata) ([]*Bridge, error)`.
- `(*BridgeSync).GetTokenMappings(ctx, pageNumber, pageSize, originTokenAddress) ([]*TokenMapping, int, error)`.
- `(*BridgeSync).GetLegacyTokenMigrations(ctx, pageNumber, pageSize) ([]*LegacyTokenMigration, int, error)`.
- `(*BridgeSync).GetProof(ctx, depositCount, localExitRoot) (tree.Proof, error)`.
- `(*BridgeSync).GetExitRootByHash`, `GetExitRootByIndex`, `GetLastRoot`, `GetRootByLER`, `GetBlockByLER` — exit-tree root lookups.
- `(*BridgeSync).GetLastProcessedBlock(ctx) (uint64, bool, error)`.
- `(*BridgeSync).GetContractDepositCount(ctx) (uint32, error)`; `GetLatestNetworkBlock(ctx) (uint64, error)`.
- `(*BridgeSync).GetLastReorgEvent(ctx) (*LastReorg, error)`.
- `(*BridgeSync).IsActive(ctx) bool` — true iff not halted.
- `(*BridgeSync).OriginNetwork() uint32`.
- `(*BridgeSync).SubscribeToSync(id) <-chan sync.Block`, `SubscribeToNewBridge(id) <-chan uint64`.
- `Bridge`, `TokenMapping`, `LegacyTokenMigration`, `RemoveLegacyToken`, `BackwardLET`, `ForwardLET`, `Event` — persisted-row struct types; `BridgeSource` with constants `BridgeSourceForwardLET`, `BridgeSourceBackwardLET`.
- `BridgeSyncerID` (`L1BridgeSyncer`, `L2BridgeSyncer`), `BridgeDeployment` (`Unknown`, `NonSovereignChain`, `SovereignChain`), `CurrentDBVersion`.
- `Config` — mapstructure-tagged configuration; `Config.Validate() error`; `Config.ResolvedString() []string`. Keys: `DBPath`, `BlockFinality`, `InitialBlockNum`, `BridgeAddr`, `SyncBlockChunkSize`, `RetryAfterErrorPeriod`, `MaxRetryAttemptsAfterError`, `WaitForNewBlocksPeriod`, `RequireStorageContentCompatibility`, `DBQueryTimeout`, `SyncFromInBridges` (`true` | `false` | `auto`).
- `NewBackfillTxnSender(dbPath, client, bridgeAddr, syncFromInBridges, logger) (*BackfillTxnSender, error)`; `(*BackfillTxnSender).BackfillAll(ctx) error`; `.Close() error`.
- `GenerateGlobalIndex(mainnetFlag, rollupIndex, depositCount) *big.Int`; `GenerateGlobalIndexForNetworkID(networkID, depositCount) *big.Int`; `DecodeGlobalIndex(globalIndex) (mainnetFlag, rollupIndex, localExitRootIndex, error)`.
- `ExtractTxnAddresses`, `ExtractFromAddrFromCalls`, `ExtractParamFromCallData`, `BridgeAssetMethodID`, `BridgeMessageMethodID`, `DebugTraceTxEndpoint`, `GetTransactionByHashEndpoint`, `RPCTransactionByHash`, `LeafData`, `Call`, `Transaction` — low-level helpers used by callers that reconstruct bridge events outside the normal ingest path.
- `ErrInvalidPageSize`, `ErrInvalidPageNumber`.

Database shape (tables, columns, indexes, foreign keys) is authoritatively described in `bridgesync/migrations/SPEC.md#8`–`#11`; this package is the sole writer for those tables.

## Error modes

- **41.** Read operations invoked while halted MUST return the shared "inconsistent state" error kind without opening the database.
- **42.** Construction errors MUST be wrapped with context identifying the failing step (contract binding, sanity check, database open, appender construction, downloader construction, driver construction) so that logs distinguish them.
- **43.** Ingestion of a log whose tracer-derived call data fails to decode (method ID unknown, inputs unparseable) MUST return a typed error from the event handler, which MUST cause the block's transaction to roll back and the sync driver to retry per its configured retry policy.

## Out of scope

- Claim events. The current schema does not contain claim-related tables (see `bridgesync/migrations/SPEC.md#10`); any claim-kind behavior lives elsewhere.
- Direct HTTP or RPC exposure of the stored data. Consumers (bridge service, RPC, aggsender, …) are responsible for surfacing these reads.
- Determining whether a transaction ever actually reached the bridge beyond what the emitted event and the tracer agree on. The syncer trusts the log set produced by the underlying downloader after reorg-detector confirmation.
- Schema evolution. Table shape is owned by `bridgesync/migrations/SPEC.md`.
- The leaf-type enumeration and empty-LER sentinel are owned by `bridgesync/types/SPEC.md`.

## Children

- `migrations/` — SQLite schema for the bridgesync database; see `migrations/SPEC.md`.
- `types/` — shared leaf-type enumeration and the empty-LER sentinel; see `types/SPEC.md`.
- `mocks/` — generated test doubles for the interfaces this package exposes; no hand-authored contract.
