# SPEC: bridgesync/migrations

## Summary

Defines the SQLite schema evolution for the bridgesync persistence layer: the set of ordered, versioned migrations that create and alter the tables bridgesync reads and writes (blocks, bridges, claims, token mappings, legacy token migrations, archived bridges, and local-exit-tree event rows), together with the composition that produces the full migration list actually applied at startup. The directory is the source of truth for the on-disk shape of the bridgesync database: any change to a table, column, index, or constraint that bridgesync depends on lives here.

Consumers obtain the full migration list (base + bridgesync-specific + tree) from this package and either apply it themselves or ask the package to apply it to a given database path. Migrations are pinned by stable IDs and applied in a deterministic order; older databases are brought forward by the subset of migrations they have not yet applied.

## Requirements

- **1.** The package MUST expose, for a given database path, a single operation that brings that database to the current schema, applying any not-yet-applied migrations in their defined order.
- **2.** The package MUST expose an operation that returns the full ordered migration list (base migrations, then bridgesync-specific migrations, then tree migrations) without applying anything, so that callers can compose it with their own runner.
- **3.** The package MUST expose an operation that returns the prefix of the bridgesync-specific migration list up to and including a caller-named migration ID, to support testing intermediate schema states.
- **4.** Each bridgesync-specific migration MUST have an identifier that is stable across releases; once shipped, a migration's identifier and its effect on a clean database MUST NOT change.
- **5.** Bridgesync-specific migrations MUST be applied in ascending lexicographic order of their identifiers, and this order MUST be a total order (no duplicate identifiers).
- **6.** Applying the full migration list on a database at any prior shipped version MUST result in the same final schema as applying it on an empty database.
- **7.** Applying the full migration list on a database already at the current version MUST be a no-op with respect to schema and data.
- **8.** The current schema MUST provide, at minimum: a `block` table keyed by block number with a block hash column, a `bridge` table of deposit events keyed by `(block_num, block_pos)` carrying leaf type, origin/destination network and address, amount, metadata, deposit count, transaction hash, block timestamp, originating-transaction sender, deposit sender (`from_address`), recipient (`to_address`), and a `source` marker, a `token_mapping` table of wrapped-token registrations keyed by `(block_num, block_pos)`, a `legacy_token_migration` table of legacy-token migration events keyed by `(block_num, block_pos)`, a `backward_let` table of backward local-exit-tree rollbacks keyed by `(block_num, block_pos)`, a `forward_let` table of forward local-exit-tree advances keyed by `(block_num, block_pos)`, and a `bridge_archive` table keyed by `deposit_count`.
- **9.** Every table whose primary key includes `block_num` MUST declare `block_num` as a foreign key to `block(num)` with `ON DELETE CASCADE`, so that removing a block transitively removes all events attached to that block.
- **10.** The current schema MUST NOT contain a `claim` table, a `set_claim` table, an `unset_claim` table, or any `calldata` column.
- **11.** The current schema MUST provide an index on `bridge(deposit_count)` in descending order and an index on `bridge(txn_sender)`.
- **12.** The `bridge.source` column MUST be present on databases upgraded from any prior shipped version as well as on freshly-created databases, and its absence MUST be treated as a schema bug.
- **13.** Adding the `bridge.source` column MUST be idempotent: running the upgrade against a database that already has the column MUST succeed without error and MUST NOT alter existing `source` values.
- **14.** The downward (rollback) direction of the migration set MUST be refused as an operation: the package MUST return an error when asked to roll back and MUST NOT mutate the database in that case.

## External interface

- Apply-to-path operation: takes a filesystem path to a SQLite database and returns an error; success means the database is at the current schema.
- Full-list operation: returns the ordered list of migrations that the apply operation would run, each migration identified by a stable string ID and its SQL body.
- Prefix operation: takes a migration ID and returns the ordered prefix of the bridgesync-specific migration list up to and including that ID; an unknown ID yields an empty result, not an error.
- Refuse-rollback operation: takes a path and a count, returns an error unconditionally.
- Migration SQL file naming convention (part of the contract because IDs are derived from filenames): files embedded from this directory named `<id>.sql` contribute a migration with identifier `<id>`. New migrations MUST use an identifier that sorts after every previously-shipped identifier.

## Error modes

- **15.** When the apply operation cannot open, create, or write to the database at the given path, it MUST return an error and MUST NOT leave the database in a state that claims a higher applied-migration level than was actually committed.
- **16.** When the apply operation encounters a migration whose SQL fails, it MUST return an error identifying the failure and MUST NOT mark that migration as applied.
- **17.** When the rollback operation is invoked, it MUST return an error whose message explains that rollback is not supported for this schema.

## Out of scope

- Query execution, row marshalling, and any business logic over the tables. This directory defines shapes only; the logic that writes and reads these tables lives in the parent package.
- The content and ordering of the base migrations and of the tree migrations. Those are owned by their respective packages; this directory only composes them into the final list.
- Data migration beyond what the SQL migrations themselves perform. There is no bulk-backfill or external-data-source step in this package.
