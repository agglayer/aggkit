# SPEC: aggsender/db/migrations

## Summary

Defines the versioned schema evolution of the aggsender's local SQLite database, which persists certificate lifecycle state (the `certificate_info` table and its retry-indexed `certificate_info_history` companion). Each migration step is embedded into the binary at compile time and applied in order by a migration runner at aggsender startup, taking an existing database from any prior schema version up to the current one.

Two parallel tables hold the same column set. `certificate_info` is keyed by `height` (one row per certificate height — the latest attempt), while `certificate_info_history` is keyed by `(height, retry_count)` and retains every attempt. Schema changes are always applied to both tables together so they stay structurally identical except for the primary key.

## Requirements

- **1.** The package MUST expose an ordered, immutable list of schema migrations that is fully embedded in the compiled binary and requires no external SQL files at runtime.
- **2.** Each migration MUST declare both an upward transformation and a downward transformation, so any deployed schema version can be reached from any other by composing them in order.
- **3.** Each migration MUST carry a stable identifier that is unique within the list and preserves the strict ordering of application.
- **4.** The initial migration MUST create a `certificate_info` table keyed by `height` alone and a `certificate_info_history` table keyed by `(height, retry_count)`, with the following columns required on both: `height`, `retry_count`, `certificate_id`, `status`, `previous_local_exit_root` (nullable), `new_local_exit_root`, `from_block`, `to_block`, `created_at`, `updated_at`, `signed_certificate` (nullable).
- **5.** Any schema change that adds or removes a column from certificate state MUST apply the identical change to both `certificate_info` and `certificate_info_history` within the same migration step, so the two tables never diverge in column set.
- **6.** Adding a column to existing tables MUST be done in a way that preserves all previously stored rows (i.e., with a default value or permitting NULL), so a running deployment upgrading through the migration loses no certificate history.
- **7.** A migration that removes rows as part of a data fix MUST restrict the deletion to rows that are not in the finalized terminal status (status code `4`); rows in the finalized status MUST be preserved regardless of other column values.
- **8.** After every migration has been applied, every row in either table MUST have a non-NULL value for the `l1_info_tree_leaf_count` column.
- **9.** Running the migration sequence a second time against a database already at the latest version MUST be a no-op (idempotent) and MUST NOT return an error.
- **10.** Running the migration sequence against an older, previously-populated database MUST bring it to the latest schema and leave finalized rows readable and queryable.

## Invariants

- **11.** For any applied migration version `N`, the set of columns present on `certificate_info` equals the set of columns present on `certificate_info_history`.
- **12.** The only primary-key difference between the two tables is that `certificate_info_history` includes `retry_count` in its key; this MUST hold at every migration version.

## External interface

- A single package-level entry point that applies the embedded migration list to a provided SQL handle, given a logger for progress output.
- A package-level exported `Migrations` slice whose contents (IDs and SQL bodies) form part of the contract consumed by generic migration-runner test utilities elsewhere in the repo.

## Out of scope

- Defining the Go row structs that map onto these tables. The storage layer owns those; this directory owns only the schema.
- Transactional application of the migration steps. The generic runner is responsible for wrapping each step.
- Rollback on running production systems. Down migrations exist for test symmetry; they are not invoked by normal startup.
