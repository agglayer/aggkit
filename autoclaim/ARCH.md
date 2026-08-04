# Architecture

## Data Flow

```text
┌─────────────────┐
│  L1 Bridge DB   │  bridges indexed by deposit_count
│  (bridgesync)   │
└────────┬────────┘
         │ query by deposit_count range
         │
┌────────▼─────────────────────────────────────┐
│              AutoClaim Service               │
│                                              │
│  ┌──────────────────────────────────────┐   │
│  │  GER Monitor                         │   │
│  │  polls imported_global_exit_root     │   │
│  │  tracks last_processed_ger_index     │   │
│  └──────────────┬───────────────────────┘   │
│                 │ new GER (l1_info_tree_index=X)
│  ┌──────────────▼───────────────────────┐   │
│  │  Deposit Count Resolver              │   │
│  │  leaf[X] → MainnetExitRoot           │   │
│  │  GetRootByLER → max_deposit_count    │   │
│  └──────────────┬───────────────────────┘   │
│                 │                            │
│  ┌──────────────▼───────────────────────┐   │
│  │  Bridge Querier                      │   │
│  │  deposit_count in                    │   │
│  │    (last_claimed, max_deposit_count] │   │
│  └──────────────┬───────────────────────┘   │
│                 │                            │
│  ┌──────────────▼───────────────────────┐   │
│  │  Proof Generator                     │   │
│  │  LER proof per bridge                │   │
│  │  RER proof cached per GER            │   │
│  └──────────────┬───────────────────────┘   │
│                 │                            │
│  ┌──────────────▼───────────────────────┐   │
│  │  Claim Executor                      │   │
│  │  claimAsset() on L2 bridge contract  │   │
│  │  updates last_claimed_deposit_count  │   │
│  └──────────────────────────────────────┘   │
└─────────────────────────────────────────────┘
```

## Internal Subcomponents

| Subcomponent | Responsibility |
| --- | --- |
| **GER Monitor** | Polls `imported_global_exit_root`, filters removed GERs via `remove_ger_events`, emits one event per new GER |
| **Deposit Count Resolver** | Maps a GER's `l1_info_tree_index` to the maximum claimable `deposit_count` via `MainnetExitRoot` |
| **Bridge Querier** | Queries L1 bridge DB for eligible bridges in the computed `deposit_count` range |
| **Proof Generator** | Generates LER and RER proofs; caches the RER proof (shared by all bridges in the same GER) |
| **Claim Executor** | Submits `claimAsset()` transactions, handles per-bridge failures, updates persisted state |

## External Dependencies

| Dependency | Interface used |
| --- | --- |
| `l2gersync` DB | `imported_global_exit_root`, `remove_ger_events` tables |
| `bridgesync` DB (L1) | Bridge query by `deposit_count` range; `GetRootByLER` |
| `l1infotreesync` | `GetL1InfoTreeLeaf(index)` to obtain `MainnetExitRoot` |
| `bridgeservice` | Proof generation logic (LER proof, RER proof) |
| L2 tx manager | Submission and nonce/gas management for `claimAsset()` transactions |

## State

Persisted in a single-row SQLite table managed by the component:

```sql
CREATE TABLE auto_claim_state (
    key VARCHAR PRIMARY KEY,
    last_claimed_deposit_count INTEGER NOT NULL DEFAULT 0,
    last_processed_ger_index   INTEGER NOT NULL DEFAULT 0
);
```

Migrations are handled via the standard `RunMigrations` pattern used across the codebase.

## Processing Loop

```text
for each new GER (index X):
    if GER in remove_ger_events → skip
    mainnetExitRoot ← l1InfoTree.GetLeaf(X).MainnetExitRoot
    maxDepositCount ← bridgeL1.GetRootByLER(mainnetExitRoot).Index
    bridges ← bridgeL1.GetBridges(lastClaimed+1, maxDepositCount, l2NetworkID)
    rerProof ← proofGenerator.GetRERProof(X)          // cached
    for each bridge (ascending deposit_count):
        lerProof ← proofGenerator.GetLERProof(bridge)
        claimExecutor.Submit(bridge, lerProof, rerProof)
    persist(lastClaimedDepositCount=maxDepositCount, lastProcessedGERIndex=X)
```

## Key Design Decisions

**No per-bridge claim tracking table** — if a bridge is already claimed the `claimAsset()` call reverts; this is treated as a skip. The L2 `bridgesync` claim table provides an audit trail if needed.

**Range query over `deposit_count`** — bridges are synced sequentially so a single indexed range scan covers all eligible bridges without per-bridge L1 info tree index lookups.

**RER proof cache** — all bridges in the same GER share the same `rollupExitRoot`, so the RER proof is computed once per GER and reused.
