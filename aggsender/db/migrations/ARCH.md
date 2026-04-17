# ARCH: aggsender/db/migrations

## Overview

The directory is a flat set of numbered SQL files (`0001.sql` … `0004.sql`) and a thin Go wrapper (`migrations.go`) that embeds each file with `//go:embed`, wraps them in `types.Migration` records keyed by their filename stem, and delegates application to the shared runner in `db` (`db.RunMigrationsDB`). Each SQL file uses the `sql-migrate` convention of `-- +migrate Up` / `-- +migrate Down` section markers to hold both directions in the same file. Upholds SPEC #1 (embed), #2 (Up/Down sections), #3 (string IDs `"0001"`…`"0004"`).

Migration content evolves the `certificate_info` / `certificate_info_history` pair together: `0001` creates both, `0002` adds aggchain-proof / finalized-L1-info fields to both, `0003` adds `cert_type` / `cert_source` / `extra_data` to both, `0004` is a data-fix migration that cleans up rows inserted before `l1_info_tree_leaf_count` was populated. Upholds SPEC #4–#8.

Tests live alongside the code. `migrations_test.go` validates the forward-only path (apply all, insert). `000N_test.go` files use `dbmigrations.TestMigration` (from `db/migrations/testutils`) to exercise up-and-down symmetry against either an empty DB or the checked-in template in `testdata/aggsender-001.sqlite`.

## Patterns

- **1.** A new column added to certificate state MUST be added in a single new migration file that applies the same `ALTER TABLE ADD COLUMN` to both `certificate_info` and `certificate_info_history`, with a matching pair of `DROP COLUMN` statements in the `-- +migrate Down` section. Splitting across two migrations or touching only one table would break SPEC #5 / #11.
- **2.** A new migration file MUST be registered in the `Migrations` slice in the correct ordinal position and MUST carry a `//go:embed` directive for its SQL content. Relying on directory scanning is out of pattern here.
- **3.** New columns added to existing tables SHOULD be nullable or carry a `DEFAULT`; the established convention is `DEFAULT ""` for string-valued fields and nullable for binary/numeric ones. This preserves SPEC #6 on in-place upgrade.
- **4.** Data-fix migrations (delete/update rows) SHOULD be written as their own numbered step separate from schema migrations, so the history of why a row vanished stays inspectable in the SQL file.

## Notable decisions

- **5.** Migration `0004` deletes non-finalized rows that have `l1_info_tree_leaf_count IS NULL` rather than backfilling them, because the true value was not persisted before `0002` and cannot be recovered. Finalized rows (status `4`) are kept because their leaf count is no longer needed — only non-finalized certificates are ever retried, and retry is where the missing field would surface. This is the rationale for SPEC #7.
- **6.** Primary keys intentionally differ between the two tables (`height` vs `(height, retry_count)`). `certificate_info` is the current-state view (latest attempt per height, overwritten on retry); `certificate_info_history` is append-only across retries. Unifying them was considered and rejected; queries that ask "what's the current status at height H" would otherwise need a subquery on every call.
- **7.** `testdata/aggsender-001.sqlite` is a binary fixture that freezes the shape of the database at schema version 1. It exists so migration `0002`'s Up/Down can be tested against real pre-migration data, not just against an empty DB. Regenerating it requires re-applying migration `0001` and re-running any data-seed step; it is not source-of-truth for the schema, `0001.sql` is.

## Dependencies

- `github.com/agglayer/aggkit/db` — provides the shared migration runner. Replacing it would require re-implementing transactional application and the `migrations` bookkeeping table.
- `github.com/agglayer/aggkit/db/migrations/testutils` — shared up/down test harness; its `TestMigration` helper is the contract the `000N_test.go` files implement via their `migrationTester` types.
