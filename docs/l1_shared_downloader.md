# L1 shared downloader

> **Status — initial integration scope for issue #1224.** This page describes the normative
> behaviour of the L1 shared downloader ("multidownloader") and of the optional
> transaction-inclusion support for the L1 synchronizers. The initial scope **retains the legacy
> sync drivers**: they are not replaced. The source implementation for this initial scope is
> complete locally; real-node end-to-end validation is handled separately and is not claimed here.

## Overview

Aggkit can download L1 blocks and logs for the L1 synchronizers in two ways:

- **Legacy** (`legacy`, the default): each synchronizer uses its own dedicated EVM downloader,
  as it does today.
- **Multidownloader** (`multidownloader`): the L1 synchronizers share the L1 multidownloader
  service, which downloads block headers and logs once and serves them to every L1 consumer.

The two L1 synchronizers — the L1 bridge syncer and the L1 claim syncer — select the backend
**independently**:

- `[BridgeL1Sync].Downloader`
- `[ClaimL1Sync].Downloader`

Both accept `legacy` or `multidownloader` (case-insensitive, surrounding whitespace trimmed) and
both default to `legacy`. The empty value behaves as `legacy`, so a configuration that does not
set the field keeps the historical behaviour. Mixed modes are supported: for example the bridge
syncer on `multidownloader` while the claim syncer stays on `legacy`.

The selection is **per section**: setting a value in one L1 section does not change the other.

## Validation and startup errors

- An invalid value (anything other than `legacy`/`multidownloader`) is rejected while decoding
  the configuration.
- Selecting `multidownloader` for an L1 syncer is only valid when the L1 multidownloader service
  is active, that is, `[L1Multidownloader].Enabled = true`. If a syncer selects the
  multidownloader while `[L1Multidownloader].Enabled = false`, **startup fails with an error**.
  There is no silent fallback to the legacy downloader.
- The L2 synchronizers (`[BridgeL2Sync]`, `[ClaimL2Sync]`) do **not** support the multidownloader:
  only `legacy` is valid for them. Because the check is per section, an L2 section cannot select
  the shared downloader.

## Shared logs and headers

When the multidownloader is active, the block headers and logs that cover the L1 consumers are
downloaded once by the shared service and reused by every registered consumer. A consumer's
**log coverage** is defined by the downloaded **range** — including intervals that contain no
matching logs — not only by the logs that happen to be present.

Headers are served through the multidownloader's `HeaderByNumber`, which reads from the
multidownloader's own storage and **falls back to an RPC call** when a header is not present in
storage. The multidownloader's planning chunking (`[L1Multidownloader].BlockChunkSize`) is
independent from a syncer's processing chunking (`SyncBlockChunkSize`).

## Transaction inclusion (optional, library level)

The full transaction that produced a log is an **optional** capability that a consumer must
explicitly request when it registers with the shared downloader. It is **not** exposed as operator
TOML fields: it is registered in code through the shared sync configuration type `SyncerConfig`,
using `TransactionRequirements`:

- `TransactionRequirements.Enabled` (bool, default `false`): whether the syncer needs the full
  transaction for the logs it syncs. Transaction fetching is opt-in; the zero value disables it.
- `TransactionRequirements.FromBlock` (uint64): the frontier from which transactions are demanded.
- `TransactionRequirements.Topic0` (`[]common.Hash`): the event signatures (topic0) that trigger a
  transaction fetch; the entries are OR-ed. An empty list means every log requires a transaction,
  including topicless logs.

Requirements are modelled as **per-syncer tuples** `(contract address, frontier, topic set,
target)`. Only **identical** tuples are merged (deduplicated); different tuples are **not**
collapsed into a single min-frontier/union-topic demand. This matters because a topic must not be
fetched before its own activation: if one syncer needs `topicA` from an earlier frontier and
another needs `topicB` from a later frontier, combining the demands does not fetch `topicB` over
the earlier range — each topic keeps its own frontier. A combined demand is therefore a union of
the required work that respects each tuple's frontier, not a lossy merge.

For the running background demand, a transaction requirement applies to the **intersection** with
the actually registered log domain, `max(logFrom, txFrom)`: transactions are fetched only for logs
that are actually registered. An **explicit historical backfill/replay** request can instead use
logs that are already covered and older than the running demand, so a requirement's frontier is
**not** restricted to logs newer than the current range.

### Who requires transactions

- **L1 bridge syncer (`bridgesync`)**: every `BridgeEvent` needs the full transaction that produced
  it; the sender (`From`) and recipient (`To`) always come from the transaction. `SyncFromInBridges`
  **never** governed whether a transaction was needed — it only controls a conditional, indirect
  lookup of the asset `FromAddress` for bridge asset events. The bridge syncer therefore always
  requires the transaction, regardless of `SyncFromInBridges`, and there is no user-facing
  "fetch transactions" toggle for the existing `BridgeL1Sync`.
- **L1 claim syncer (`claimsync`)**: does not currently request transaction enrichment through this
  mechanism. Claims continue to rely on the call trace for the data they need, as described below.

### Claim tracing today

- Claims do **not** request transaction enrichment through this mechanism. They use the call trace
  to obtain the **claim calldata / the missing claim fields** — the claim model has no transaction
  sender field to fill.
- The **ordinary `ClaimEvent`** and the **pre-Etrog `ClaimEvent`** handlers require the trace
  (`debug_traceTransaction` with the call tracer), walking the full nested successful call tree of
  the bridge contract (skipping reverted calls and following `CALL`s to the bridge address).
- The current **`DetailedClaimEvent`** handler is direct: the log already carries the data, so it
  does not trace.
- The coexistence hook drops an ordinary `ClaimEvent` when a `DetailedClaimEvent` for the same
  transaction is present in the batch. That suppression applies to the **ordinary** signature only;
  it is **not** equivalent to suppressing the pre-Etrog path, which still requires the trace. Being
  on the Detailed path avoids the trace only while that log is actually processed, so a
  stale/dropped Detailed log does not by itself guarantee the transaction is handled trace-free.
- Full claims are currently **hardcoded enabled** in the claim appender (there is no operator mode
  that turns them off), and the trace is still needed to recover the calldata.
- Startup bridge-contract detection uses direct `eth_call`s, and the existing event-time claim
  tracing uses direct `debug_traceTransaction` calls. The claim syncer's
  `GetLatestBlockNumByGlobalIndexFromRPC` helper is also a direct RPC call. All of these are
  separate from, and not routed through, the shared downloader in this initial scope.
- `GetLatestBlockNumByGlobalIndexFromRPC` is an RPC fallback that resolves the claim syncer's
  starting block from a settled certificate's global index by running a historical, topic-filtered
  `FilterLogs` scan backwards from a target block. It is **not** routed through the shared
  downloader: the scan can request a range outside the multidownloader's cached/registered range,
  and the shared `FilterLogs` does not perform topic matching (it only serves already-downloaded
  logs for covered ranges and waits for coverage).

### Backfill

Backfilling the transaction data of existing bridge records reads from the optional transaction
provider. When the bridge syncer selects the multidownloader, the backfill resolves through the
**same shared provider and the same selected download backend** as the syncer: it reads the exact
original bridge log from the shared downloader instead of issuing a direct RPC `FilterLogs`, and
there is no fallback to `eth_getTransactionByHash` in that mode. A transaction provider configured
without the download backend is rejected (`ErrBackfillBackendRequired`); with neither set, the legacy
direct-RPC behaviour is kept. Backfill is **distinct from cache fill**: it updates the
already-materialized bridge records (the consumer rows), whereas filling the transaction cache only
stores transactions. Filling the cache does **not** update consumer historical rows automatically.
Keeping the two separate means a backfill can be re-run without assuming the cache is warm, and the
cache can be filled without implying the consumer rows were updated.

## Coverage, readiness and the background worker

- Per-consumer **log coverage** and derived **transaction readiness** are different. Readiness is
  computed for a downloaded **range** and requires the whole interval to be covered — including
  intervals that contain no matching logs — together with a stored transaction row for every
  relevant log. The mere presence of some logs is not enough.
- The transaction fetch runs in an **independent background worker**. It is not part of the log
  download loop and does not block log ingestion.
- Availability is computed **on demand** from the current canonical coverage and per-transaction
  row existence. There is **no** cached per-syncer interval and **no** negative cache (a "not
  available" answer is not remembered).
- The provider supports **on-demand replay for demand that lies before a registration's activation
  boundary**: older logs can be resolved on demand, so availability is not limited to logs above a
  frontier. Filling the cache does not, by itself, rewrite consumer historical rows.
- The registration checkpoint `L` is the **consumer processor's committed checkpoint** (the last
  block the consumer processor has committed), not a multidownloader log frontier:
  - a **new** registration with no checkpoint uses the configuration's initial block;
  - the **existing bridge** registration is future-only and starts at `L + 1`. `L` is
    materialized processed history, but that does **not** mean the transaction cache already holds
    the history up to `L`: switching to the shared downloader does not retroactively enrich the
    transactions of already-processed blocks, so the running requirement must not start before
    `L + 1`;
  - an **explicit historical** registration may request older logs.
- Registration happens at startup, so changes to transaction requirements take effect **across
  restarts**, not live.
- Missing transaction gaps reuse the already-downloaded log cache. Adding a new topic0 requirement
  **widens the topic filter** over the shared cached logs rather than re-downloading the logs.

### Transaction worker resources

- The foreground resolver and the background worker share a single RPC budget: the transaction RPC
  concurrency limit **reuses `[L1Multidownloader].MaxParallelBlockHeaderRetrieval`** (default `30`),
  the same value that bounds parallel block-header retrieval. The transaction path therefore never
  opens more sockets than the rest of the multidownloader, and it is not a separate knob.
- A single transaction RPC request is bounded by a **fixed internal timeout** (a package constant of
  `30s`), not by an operator-facing TOML field. It exists so a stuck provider cannot hold a shared
  slot forever, and it is deliberately **not configurable**.
- The background scan walks the already-covered range **page by page**, using
  `[L1Multidownloader].BlockChunkSize` as the page size, so memory stays O(page size) and the number
  of goroutines stays O(concurrency), never O(history).
- The periodic scan period **inherits `[L1Multidownloader].PeriodToCheckReorgs`** when it is set and
  otherwise falls back to a small internal default, so there is no unbounded busy-retry loop. Newly
  committed logs also wake the worker immediately.
- None of this blocks log ingestion: log-only consumers never wait on the transaction worker; only a
  consumer that requires transaction data waits for that data.

## Transaction identity and inclusion

- Transactions are canonical to a **single chain**. A transaction inclusion is identified by the
  pair `(tx_hash, block_hash)`: the same hash included in two different blocks (a stale inclusion
  plus the canonical one) is stored as two distinct inclusions and never conflated.
- Integrity is enforced against the stored blocks by `(block_number, block_hash)`; there is no
  foreign key that would let a block deletion cascade into a different block that shares a number.
- On **write**, each transaction's `(block_number, block_hash)` must match a canonical block.
  Otherwise the whole save fails with `ErrTransactionInclusionMismatch` and nothing is written (the
  save is atomic).
- On **reorg**, the transaction rows for the affected `(block_number, block_hash)` are removed
  together with the blocks and logs. A re-inclusion of the same hash in a new canonical block is
  stored as a **new** inclusion row; the removed row is not retained as a cached orphan.
- On **read**, rows are joined to the blocks on both block number and block hash, so only rows
  whose block is still canonical can be served.
- **Validation** rejects: nil transactions; a missing hash or sender; **pending** transactions
  (no block hash); a missing or malformed block number; and malformed input. Contract-creation
  transactions (`to` is null) are valid and keep a nil/zero recipient.
- **Absence of coverage** (a range not downloaded yet) is reported separately from a reorg/
  inclusion mismatch: it is a benign "not yet available", never the inclusion-mismatch sentinel.
- Transaction RPC work is **bounded and cancellable**. It does not block log ingestion, and
  log-only consumers never wait on it; only a consumer that requires transaction data waits for
  that data. A stale inclusion is dropped and the affected range is refetched rather than retrying
  the same stale log forever.

## Reorg tracking

- Reorg tracking stays with the current drivers, which keep using the **current legacy reorg
  detector**; the multidownloader is used as the shared download backend.
- The **bridge** syncer's **post-commit subscriptions are preserved**.
- The **claim** syncer's **lazy/bootstrap** behaviour is preserved.
- This is **not** a migration to a native multidownloader reorg driver.
- Reorged rows — including transaction inclusions — are removed together with the affected blocks
  and logs.

## Minimal TOML examples

Bridge on the multidownloader and claims on the legacy downloader:

```toml
[BridgeL1Sync]
Downloader = "multidownloader"

[ClaimL1Sync]
Downloader = "legacy"

[L1Multidownloader]
Enabled = true
```

Both L1 syncers on the multidownloader:

```toml
[BridgeL1Sync]
Downloader = "multidownloader"

[ClaimL1Sync]
Downloader = "multidownloader"

[L1Multidownloader]
Enabled = true
```

Notes:

- `Downloader` defaults to `legacy`, so writing `Downloader = "legacy"` is optional. The field is
  independent per section.
- The L2 sections (`[BridgeL2Sync]`, `[ClaimL2Sync]`) must keep `legacy`.
- These snippets only show the downloader selector. The remaining multidownloader options keep
  their own defaults; the worker-related values the transaction path reuses are listed under
  "Transaction worker resources" above.

## Local testing

Use the local test and build targets only. Do **not** point them at production endpoints and do not
use real secrets.

```bash
# Focus a single package or test locally
go test -short -run TestName ./path/to/package

# Full unit test suite with coverage
make test-unit

# Lint and build
make lint
make build
```

## Out of scope for the initial scope

- L2 synchronizers on the multidownloader.
- Automatic rewriting of consumer historical data: filling the transaction cache does **not**
  rewrite existing consumer rows. (Resolving older logs on demand is supported; see above.)
- A shared trace cache.
- Live (no-restart) hot update of transaction requirements.
- Migrating to a native multidownloader reorg driver.
