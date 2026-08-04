# Spec

## Overview

`autoclaim` is a service that automatically claims L1-to-L2 bridges.

## Functional Requirements

### FR-1: GER Monitoring

The service must poll the `imported_global_exit_root` table (via `l2gersync`) to detect newly injected GERs. It must track `last_processed_ger_index` to avoid reprocessing.

### FR-2: GER Validity Check

Before processing a GER, the service must verify it is not present in the `remove_ger_events` table. Removed GERs must be skipped.

### FR-3: Bridge Eligibility

A bridge is eligible to be claimed when:

- Its `destination_network` matches the L2 network ID
- Its `deposit_count` is greater than `last_claimed_deposit_count`
- Its `deposit_count` is less than or equal to `max_deposit_count` derived from the current GER

The upper bound `max_deposit_count` is computed as:

1. Get the L1 info tree leaf at index `X` (the GER's `l1_info_tree_index`) → extract `MainnetExitRoot`
2. Query L1 bridge syncer: `GetRootByLER(MainnetExitRoot)` → `root.Index` is the maximum `deposit_count`

### FR-4: Proof Generation

For each eligible bridge, the service must generate:

- **LER proof**: `GetProof(depositCount, mainnetExitRoot)` from the L1 bridge syncer
- **RER proof**: `GetRollupExitTreeMerkleProof(networkID, rollupExitRoot)` from the L1 info tree

The RER proof is identical for all bridges sharing the same GER and must be cached per GER.

### FR-5: Claim Execution

The service must submit a `claimAsset()` transaction to the L2 bridge contract for each eligible bridge, in ascending `deposit_count` order.

### FR-6: Failure Handling

| Failure | Behaviour |
| --- | --- |
| Bridge already claimed | Skip (expected, not an error) |
| Invalid proof / parameters | Log error (indicates a bug, do not retry) |
| Nonce / gas issues | Delegated to the transaction manager |

### FR-7: State Persistence

After processing each GER the service must persist:

- `last_claimed_deposit_count`: updated to `max_deposit_count` of the processed GER (or the highest successfully claimed deposit count on partial failure)
- `last_processed_ger_index`: updated to the `l1_info_tree_index` of the processed GER

Schema:

```sql
CREATE TABLE auto_claim_state (
    key VARCHAR PRIMARY KEY,
    last_claimed_deposit_count INTEGER NOT NULL DEFAULT 0,
    last_processed_ger_index   INTEGER NOT NULL DEFAULT 0
);
```

### FR-8: Start Timestamp Filter

The service must accept a configuration parameter `L1TimestampStart` (default `0`). On startup, bridges whose L1 timestamp is earlier than this value must not be claimed.

## Out of Scope (future enhancements)

- Batch multiple claims into a single transaction
    - Contract support it
    - Use the GroupingClaims as zkevm-bridge
- Priority queue for high-value bridges
- Filtering and claim by address, ....
- L2-to-L1 claiming
- L2-to-L2 claiming
