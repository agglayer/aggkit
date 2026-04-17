# ARCH: bridgesync/migrations

## Overview

The package is a thin composer on top of the generic DB migration runner. SQL files are embedded at build time via `go:embed *.sql`; at package init each file is turned into a `types.Migration{ID, SQL}` whose ID is the filename without the `.sql` suffix, then the slice is sorted by ID so application order is deterministic regardless of filesystem enumeration order. `GetFullMigrations` concatenates the base migrations, this package's migrations, and the tree migrations in that fixed order. `RunMigrations` hands that list plus an "extra" Go callback (`addSourceField`) to `db.RunMigrationsExtended`, which applies any not-yet-applied migrations then invokes the callback. `RunMigrationsDown` is a hard-coded refusal. `GetUpTo` uses binary search to slice the bridgesync-specific list up to and including a given ID — it is a testing aid for reconstructing intermediate schema states. Upholds SPEC #1 (`RunMigrations`), #2 (`GetFullMigrations`), #3 (`GetUpTo`), #4–#7 (embed + sort), #12–#13 (`addSourceField`), #14 (`RunMigrationsDown`).

The SQL files themselves carry the contract for the schema's shape. They are written in the `rubenv/sql-migrate` dialect with `-- +migrate Up` / `-- +migrate Down` markers. The down direction is authored for historical completeness and for test harnesses that exercise individual migrations, but it is not reachable through the public API because later schema reshapes (notably migration 0015 dropping `claim`/`set_claim`/`unset_claim`) make a global rollback non-recoverable.

## Patterns

- **1.** New schema changes MUST be added as a new `bridgesync<NNNN>.sql` file with a numeric ID that sorts after every existing file. Existing files MUST NOT be edited after release, because their IDs are recorded in user databases and an in-place edit would silently skip the new content on any upgraded database.
- **2.** The `-- +migrate Up` section of every migration MUST be self-contained and idempotent-safe against the specific legacy databases it needs to handle; one-off divergences between historical releases (the 0.8.1 vs 0.9.0 `source` column case is the canonical example) SHOULD be handled by a Go-side idempotent step registered as the "extra" callback passed to the runner, not by branching SQL.
- **3.** Any table whose primary key includes `block_num` MUST declare `block_num` as `REFERENCES block(num) ON DELETE CASCADE`. The parent package relies on deleting a `block` row to purge everything derived from it; breaking this pattern silently breaks reorg handling upstream.

## Notable decisions

- **4.** Migration IDs are filenames, not hand-written constants. This keeps the list append-only and diff-auditable, and it couples the applied-migrations table in every user database directly to filenames on disk — which is why pattern #1 is load-bearing.
- **5.** The `bridge.source` column is added in Go (`addSourceField`) rather than in migration 0014's SQL because SQLite has no `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` and two historical release lines (v0.8.1 without the column, v0.9.0 with it) both need to converge on the same schema without either erroring. The Go path tries the `ALTER`, and on failure reads `pragma_table_info` to distinguish "column already exists" (acceptable) from a real error. This runs after every migration pass, including on databases already at the current version — the check is cheap and keeps the behaviour idempotent.
- **6.** `RunMigrationsDown` is not implemented as a real rollback. The tree migrations appended at the end of the full list own mutable state that would be destroyed by a naive down-migration, and migration 0015 drops tables that earlier migrations created columns on; there is no consistent "step back one" semantics to offer. Returning an error is deliberate — callers that thought they wanted to roll back should restore a backup instead.
- **7.** Three SQLite databases checked into `testdata/` (`0.7.3.sqlite`, `0.8.1.sqlite`, `0.9.0.sqlite`) exist so the test suite can prove that applying the current migration list against each historical on-disk format yields the current schema. They are fixtures consumed by `migrations_test.go`; adding a new historical release line that diverges in a way a test needs to guard SHOULD produce a new fixture here.

## Dependencies

- `github.com/rubenv/sql-migrate` (transitively, via `db.RunMigrationsExtended`) defines the `-- +migrate Up` / `-- +migrate Down` marker syntax the SQL files use. Changing runner would require rewriting every file's marker comments.
