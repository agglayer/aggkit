# dvnsyncer

`dvnsyncer` indexes LayerZero `PacketSent` and AggLayerDVN `JobAssigned` events
from an EVM chain into a local SQLite database.  It has no remote service
dependencies beyond the chain RPC; all state is stored in a single file on disk.

Two instances are run side-by-side — one for L1 and one for L2 — under the
`agglayer-dvn-worker` component.

## Responsibilities

1. Poll new blocks up to `tip - ConfirmationDepth`.
2. Fetch logs from two contracts per block range:
   - `EndpointV2` (LayerZero) — decodes `PacketSent` into `PacketRecord`.
   - `AggLayerDVN` — decodes `JobAssigned` into `JobAssignedRecord`.
3. Persist events to SQLite via `db.InsertPacket` / `db.InsertJobAssigned`
   (INSERT OR IGNORE — idempotent on (chain_id, tx_hash, log_index)).
4. On chain reorg: `db.DeleteFromBlock(ctx, chainID, reorgFromBlock)` trims
   all rows at or above the reorged block.

## Config fields

```toml
[DVNSyncerL1]
ChainID            = 1                                    # EVM chain ID
RPCUrl             = "https://eth-mainnet.example.com"    # JSON-RPC endpoint
EndpointV2Addr     = "0x..."                              # LZ EndpointV2 on this chain
AggLayerDVNAddr    = "0x..."                              # AggLayerDVN on this chain
SyncStartBlock     = 0                                    # first block to index
ConfirmationDepth  = 12                                   # blocks before an event is final
DBPath             = "/tmp/dvnsyncer_l1.db"               # SQLite file
```

`DVNSyncerL2` uses the same fields with different values (Polygon chain ID,
Polygon contract addresses, separate `DBPath`).

## Query surface

These methods are called by `dvnworker` components; the service exposes them
directly on `*dvnsyncer.Service`:

| Method | Description |
|--------|-------------|
| `ListPendingJobs(ctx, sinceBlock, confirmations)` | Returns all `JobAssignedRecord` rows for this chain with `block_num >= sinceBlock`. Pass `confirmations=0` to skip the confirmations filter. |
| `GetPacketByHash(ctx, payloadHash)` | Returns the `PacketRecord` for a given `payloadHash` (hex string). Returns `nil, nil` when not found. |
| `GetJobAssigned(ctx, payloadHash)` | Returns the `JobAssignedRecord` for a given `payloadHash`. Returns `nil, nil` when not found. |

The underlying `db.DB` also exposes `DeleteFromBlock` for reorg handling.

## Database schema

Two tables in the SQLite file:

- `lz_packet` — one row per `PacketSent` log.  Key fields: `chain_id`,
  `payload_hash`, `global_index` (NULL if not an AggLayer OFT packet),
  `oft_send_to`, `oft_amount_sd`.
- `dvn_job_assigned` — one row per `JobAssigned` log.  Key fields:
  `chain_id`, `payload_hash`, `confirmations`.

Migrations live in `dvnsyncer/db/migrations/` and run automatically on startup.

## Running standalone (for debugging)

The syncer is not exposed as a standalone component; it is always started as
part of `agglayer-dvn-worker`.  To run the full worker:

```sh
aggkit run --components agglayer-dvn-worker --cfg /path/to/config.toml
```

See `config.toml.example` for all required fields.
