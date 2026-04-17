# SPEC: aggsender/db

## Summary

Persistent storage layer for the aggsender. Holds the full lifecycle of certificates this node has produced for the agglayer: the latest attempt per height in a current-state table, and all prior attempts in an append-only history table. It also holds auxiliary state: a single "last rejected certificate" slot in a key-value area, and a network-identity compatibility record. The schema itself is owned by the child migrations package; this level owns the Go access surface that callers (the aggsender flow) use to save, update, query, move-to-history, and prune certificates.

The layer is opinionated about two things beyond plain CRUD. First, large signed-certificate blobs are spilled to the filesystem and the table stores only a sigil path, which keeps the SQLite DB small and fast to snapshot. Second, an explicit retention policy is consulted on every new-height write to decide whether prior attempts get archived to the history table, discarded outright, or the oldest finalized heights pruned — so the DB does not grow without bound.

## Requirements

- **1.** Opening storage MUST bring the underlying database to the latest schema version before returning a usable handle; see `aggsender/db/migrations/SPEC.md#9`, `#10`.
- **2.** Saving a first-attempt certificate for a new height MUST persist it as the current certificate for that height and MUST invoke the configured retention policy before the insert, within the same transaction.
- **3.** Saving a retry (retry_count > 0) for a height that already has a current row MUST replace the current row's retry position atomically; whether the prior attempt is retained in history is determined solely by the retention policy and MUST NOT be decided inside the save operation.
- **4.** When the retention policy preserves retries, the prior current row for a height MUST be copied to the history table and then removed from the current table within the same transaction, so no certificate is ever visible in both tables simultaneously for the same `(height, retry_count)`.
- **5.** When the retention policy does not preserve retries, the prior current row MUST be deleted from the current table and MUST NOT be copied to history.
- **6.** When the retention policy is configured with a finite retain count `N` and a new first-attempt certificate at height `H ≥ N` is saved, all rows (in both tables) with `height < H − N` MUST be deleted in the same transaction as the save.
- **7.** A retain count of zero MUST mean "retain all" and MUST suppress any height-based pruning; it MUST NOT mean "retain none".
- **8.** Updating a certificate by its certificate id MUST modify only `status` and `updated_at`, MUST target the current-state table only, and MUST be atomic.
- **9.** A save-or-update by height MUST insert when no row for that height exists, and otherwise MUST apply the same status/`updated_at` update by certificate id as in #8; it MUST NOT otherwise replace column values of an existing row.
- **10.** A query for "last sent certificate" MUST return the current-table row with the maximum height, or a not-found indication if the table is empty.
- **11.** A query for "last settled certificate" MUST return the current-table row with status equal to the terminal `Settled` value and maximum height among those, or a distinguishable not-found indication if none exists.
- **12.** A query for "certificates by status" MUST consult the current-state table only and MUST return rows ordered by ascending height; an empty or nil status list MUST return all rows.
- **13.** An explicit delete-by-height MUST remove the row from the current table and MUST also remove any rows at that height from the history table, within the caller-supplied transaction when provided.
- **14.** When an explicit delete-by-height is invoked in "must-delete" mode and no row exists in the current table at that height, the operation MUST fail with a not-deleted error; in "maybe-delete" mode it MUST succeed as a no-op on the current table.
- **15.** Missing history rows MUST NOT cause an explicit delete-by-height to fail.
- **16.** `DeleteOldCertificates(maxHeight)` MUST delete from both tables all rows with `height < maxHeight`.
- **17.** Any operation that removes a row whose signed-certificate column points at a filesystem file MUST attempt to delete that file, and MUST NOT fail the DB operation if the file is already gone.
- **18.** Saving a certificate whose signed-certificate payload is non-empty MUST write that payload to a file under the configured certificates directory, whose name is uniquely derived from `(height, certificate_id, retry_count)`, and MUST persist in the row only a sigil reference to that file, not the raw payload.
- **19.** Reading a certificate back MUST return the raw signed-certificate content to the caller: if the stored column is a sigil file reference, the layer MUST read and substitute the file content transparently, and MUST NOT surface the sigil to the caller.
- **20.** Saving a "non-accepted" certificate MUST overwrite the single non-accepted slot (there is at most one at any time), MUST persist the signed payload as a file in the certificates directory under a fixed filename, and MUST store in the slot the file sigil plus the Keccak256 hash of the original payload.
- **21.** Reading the non-accepted certificate MUST verify that the Keccak256 hash of the current file contents matches the stored hash, and MUST fail with an error if they differ; if no non-accepted certificate has ever been recorded, reading MUST return a nil result without error.
- **22.** Every write operation that performs more than one statement MUST execute inside a single transaction: either a caller-supplied one where the API exposes it, or one opened and committed internally. On any step error, the transaction MUST be rolled back and no partial effect MUST remain visible.
- **23.** A runtime-data record MUST declare the network id this DB belongs to, and a compatibility check MUST reject any attempt to reuse the storage under a different network id.

## Invariants

- **24.** For any height `H`, the current-state table contains at most one row with `height = H`.
- **25.** For any `(height, retry_count)` pair, the history table contains at most one row with those values.
- **26.** The union of the two tables MUST NOT contain two distinct rows with the same `(height, retry_count)`.
- **27.** Every row whose signed-certificate column begins with the file sigil character points to a filename in the configured certificates directory; rows with a nil or non-sigil value MUST be treated as inline-or-empty, never as a file reference.

## External interface

- Package `db` exposes constructor `NewAggSenderSQLStorage(logger, AggSenderSQLStorageConfig) (*AggSenderSQLStorage, error)`. The config carries DB path, certificates directory, and a `StorageRetainCertificatesPolicy`.
- `AggSenderSQLStorage` implements the `AggSenderStorage` interface, which composes `AggSenderStorageMaintainer` (move-to-history, delete, delete-old by height) with read/write methods on current and last-sent/last-settled certificates, status updates, the last-sent-header-with-proof-if-in-error query, non-accepted-certificate save/get, save-or-update, and the key-value storage surface inherited from the shared `db` package.
- `StorageRetainCertificatesPolicy` (exported struct with `RetainCertificatesCount uint32`, `KeepCertificatesHistory bool`) and `StorageRetainCertificatesPolicier` interface (`OnNewCert`) form the pluggable retention surface.
- `CertificateKey{Height, RetryCount}` is the public identifier for a certificate attempt.
- `NonAcceptedCertificate` is the exported shape for the non-accepted slot; `NewNonAcceptedCertificate` serializes an agglayer certificate into this shape.
- `RuntimeData{NetworkID}` and its `IsCompatible` method implement the cross-run compatibility check required by #23.
- `ErrNoCertDeleted` is the sentinel returned under #14 for "must-delete" misses.
- Storage schema (column set, keys) is defined and versioned by `aggsender/db/migrations/SPEC.md#4`–`#8`, `#11`, `#12`.

## Error modes

- **28.** A read that finds no matching row at a height greater than zero MUST return the shared not-found sentinel of the underlying `db` package; a read at height zero MUST return `(nil, nil)` because height zero is never written.
- **29.** An explicit delete at a height absent from the current table in "must-delete" mode MUST return `ErrNoCertDeleted` wrapped with context.
- **30.** A runtime-data compatibility check MUST return a non-nil error when the stored network id differs from the expected one.
- **31.** Errors crossing the package boundary MUST carry an operation-tagging prefix via `%w` wrapping, so callers can match root causes without string matching.

## Out of scope

- The schema itself and its evolution — see `aggsender/db/migrations/SPEC.md`.
- The shared SQL runner, key-value table primitives, and `meddler` registration plumbing owned by `github.com/agglayer/aggkit/db`.
- The definition of `types.Certificate`, `types.CertificateHeader`, and the agglayer status enum.
- Any HTTP, RPC, or metrics surface — this layer is in-process only.

## Children

- `migrations/` — versioned schema evolution; see `aggsender/db/migrations/SPEC.md`.
