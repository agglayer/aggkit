# Bridge Tracker component

The bridge tracker gives a client a single endpoint to follow one bridge (identified by its
creating transaction) from the moment it is sent until it is claimed, instead of the client
polling the bridge service, the Global/Local Exit Root state and the agglayer certificate status
itself and stitching the result together. It is served by the `aggkit-proxy` binary (`TRACKER`
component), alongside the bridge service finder.

## How it works

Registering a bridge (`GET .../tx/{tx_hash}`, or connecting over the WebSocket) adds it to an
in-memory supervised list. A background engine resolves each supervised bridge's creating
transaction (`FindBridge`, over the origin network's JSON-RPC endpoint) and then walks it through
its expected path, one milestone at a time, checking the fact behind the current step and
advancing once it is met:

`FindBridge` never trusts a `BridgeEvent`-shaped log on its topic alone: it always checks the log's
emitting address against that network's canonical bridge contract address before parsing it. That
check is mandatory and fail-closed — there is no signature-only fallback for a network `FindBridge`
cannot determine a bridge address for. The address itself comes from `Tracker.BridgeAddrs` when the
network has an entry there, and from the bridge service finder (the same `[BridgeServiceFinder]`
instance — resolved on-chain through its `RollupManagerAddr` — the tracker already uses elsewhere)
for every other network — `BridgeAddrs` is a static override for pinning or working around finder
resolution, not the only source of truth. Resolving the address for a network
neither source knows about yet is a transient condition (`"resolving canonical bridge address for
network %d"`) that the engine retries, exactly like a not-yet-mined receipt; a log whose address
does not match the resolved bridge — including the case where the *only* signature-matching logs in
the receipt fail the address check — falls through to the same permanent `ErrBridgeTxNotABridge`
outcome as a receipt with no matching log at all, since neither can ever change on retry.

The accepted `BridgeEvent` log must also be coherent with the rest of the upstream's answer, or
`FindBridge` fails closed with `ErrInvalidUpstreamData` and records nothing: the log's block hash
and number must be its receipt's, its tx hash must be the tracked tx, and the header the RPC
reports at the receipt's block number (the same lookup that provides `block_timestamp`) must have
that number and the receipt's block hash. The hash compared is the one the RPC client reports, i.e.
the RPC's own `hash` field when `HashFromJSON` is enabled (the default of the proxy), so chains with
non-standard headers are not rejected. Unlike `ErrBridgeTxNotABridge` this error is transient: a
defective or out-of-sync endpoint, or a reorg between the calls, is retried until the upstream
answers coherently or `UnresolvedTimeout` elapses.
Two RPC client settings can make the check fail on every retry, so the engine gives up on the
affected bridges after `UnresolvedTimeout` instead of resolving them:

- With `HashFromJSON = false` (the generic default in [common_config.md](common_config.md)) the
  compared hash is recomputed from the RLP header. On a chain whose node-reported hash differs from
  that, no header ever matches its receipt, so no bridge on that chain resolves. This is why the
  proxy defaults `HashFromJSON` to `true`.
- With `SupportNonEthereumWithMaxGapsInBlocks > 0`, if the node answers the receipt's height as
  missing, the client serves the nearest previous block instead, whose number differs from the
  receipt's. That bridge is refused rather than stamped with another block's timestamp: without the
  node serving the receipt's block by number it cannot be proven canonical.

A fully coherent lying endpoint is out of scope: this is a defense against inconsistent data, not a
substitute for trusting the configured RPC.

| Step | Meaning |
| --- | --- |
| `WaitingGERUpdate` | L1-originated bridge: the L1 Global Exit Root has not been updated with this deposit yet. |
| `WaitingLERUpdate` | L2-originated bridge: the origin network's Local Exit Root has not been updated yet. |
| `PendingInclusion` | The bridge is not yet part of any certificate sent to the agglayer. |
| `CertificatePending` | Included in a certificate; waiting for it to settle (covers Pending/Proven/Candidate/InError). |
| `WaitL1SettledGER` | L2-originated only: the certificate settled, waiting for its settlement tx to confirm on L1. |
| `WaitingGERInjection` | L1 → L2 and L2 → L2 only: waiting for the covering Global Exit Root's injection tx to land on the destination network — an L2-side fact; skipped for L2 → L1, since mainnet needs no injection. |
| `WaitingL1InfoLeafAvailable` | Always right before `WaitingClaim`, on every route: waiting for the bridge-service instance that will build the claim proof — the origin network's own instance, or the destination's when the origin is mainnet (which has no bridge-service deployment of its own) — to have its L1 info tree sync caught up to this deposit (`GET /bridge/v1/l1-info-tree-index`). Unlike `WaitingGERInjection`, this is never skipped or inferred from a sibling step: injecting a GER on the destination is not the same fact as the proof-building instance having caught up, and that sync can lag behind the finality this tracker uses elsewhere (see [#1823](https://github.com/agglayer/aggkit/issues/1823)). |
| `WaitingClaim` | The bridge is claimable: the proof-building instance has the bridge's L1 info tree index. |
| `Claimed` | The bridge has been claimed on the destination network. Terminal only once the claim's block is final (see [Reorgs](#reorgs-re-evaluation-and-backward-transitions)). |

Which steps apply, and in which order, depends on the bridge's direction:

- **L1 → L2**: `WaitingGERUpdate` → `WaitingGERInjection` → `WaitingL1InfoLeafAvailable` → `WaitingClaim` → `Claimed`
- **L2 → L1**: `WaitingLERUpdate` → `PendingInclusion` → `CertificatePending` → `WaitL1SettledGER` → `WaitingL1InfoLeafAvailable` → `WaitingClaim` → `Claimed`
- **L2 → L2**: `WaitingLERUpdate` → `PendingInclusion` → `CertificatePending` → `WaitL1SettledGER` → `WaitingGERInjection` → `WaitingL1InfoLeafAvailable` → `WaitingClaim` → `Claimed`

The whole route is published the moment the creating tx resolves, so a client sees every step it
will walk through before any milestone has been checked — not just the current one.

`TrackingStatus` summarizes the bridge's lifecycle for a client that only needs the high-level
state: `registered` (added to the list, not resolved yet), `running`, `error` (a step, or the
initial resolution itself, failed terminally), or `finished` (claimed, and the claim's block is
final: `claim_status` `claimed_finalized`). A bridge claimed with a claim that is not final yet is
still `running`, with `claim_status` `claimed`.

### Step dates

Each step's `start_date`/`end_date` prefer a deterministic on-chain fact (the block the step's
own milestone was met at) over the instant the tracker happened to poll and notice it — see
[BridgeStepPath](bridgetracker/API.md#bridgesteppath) for exactly which steps have one and how
they chain into each other. A step whose resolver could not fully resolve that fact anyway still
completes normally; it just explains why via its own `error`, with `error_type` `"warning"`
(informational only, not a failure — see [ErrorStep](bridgetracker/API.md#errorstep)).

This only ever applies going forward: a bridge whose steps were already `"done"` before an
upgrade to this behavior keeps whatever dates were recorded under the previous, observation-time
implementation — a step already `"done"` is never resolved again (see "How it works" above; a
reorg can only reopen it, see below), so there is nothing left to recompute it from. A bridge still in flight at the time of the upgrade
will show a mixed timeline: earlier, already-completed steps keep their old observation-time
dates, later ones get the new on-chain-derived ones. This is expected, not a bug — treat it as an
artifact of the moment a given bridge was resolved, not something to backfill.

### Reorgs: re-evaluation and backward transitions

A tracking is **final** only when its `Claimed` step is backed by a claim whose block is final on
the destination network: the finality resolver (see
[Block finality of bridges and claims](#block-finality-of-bridges-and-claims)) is asked about the
claim's `tx_hash` and `block_num`, with the `block_finalized` that `GET /bridge/v1/claims` reported
for it. Then `claim_status` is `claimed_finalized`, `tracking_status` is `finished`, the bridge
leaves the active list and **nothing of it is ever re-evaluated again**. A final claim implies every
fact before it (the claim runs on the same chain as the GER injection, at or after its block), so
intermediate steps never need a finality of their own to close a tracking, and the origin bridge is
marked final too.

Until then, on every engine tick (`PollInterval`, 10 s by default) the tracker re-checks what a
reorg can undo, before advancing the current step, and moves the tracking **backwards** when a fact
is gone:

| What is re-checked | When | Call | Gone means |
| --- | --- | --- | --- |
| The origin block of the bridge tx | While `bridge_status.block_finalized` is `false` | The canonical block hash at `block_number` (one header), then the finality resolver for the bridge tx | The hash changed: the resolved bridge is discarded (`tracking_status` back to `registered`, `bridge_status`/`all_steps` `null`) and `FindBridge` resolves the tx again, in the same tick, wherever it was re-included (or gives up after `UnresolvedTimeout` if it never is) |
| The claim | `Claimed` reached, claim not final | `isClaimed()` on the destination; while it is true, `GET /bridge/v1/claims` (a claim tx re-included in another block replaces the stored one) and the finality resolver | `isClaimed()` false: back to `WaitingClaim` (`claimed` → `readyToClaim`), or to the earliest step the claimed-bridge fallback skipped, since those were only inferred from the claim (→ `pending`) |
| The GER injection | At `WaitingClaim` (`readyToClaim`), L1 → L2 and L2 → L2, until the bridge service reports it final | `GET /bridge/v1/injected-l1-info-leaf` (never the L2 scan fallback) | Back to `WaitingGERInjection` (→ `pending`) |
| The L1 info leaf | At `WaitingClaim`, every route, until it is known final: on L1 → L2 and L2 → L2, once the injection before it is final; on L2 → L1, which has no injection, once the bridge service reports the leaf's own L1 block final | `GET /bridge/v1/l1-info-tree-index`; on L2 → L1, while the leaf is still there, also `GET /bridge/v1/injected-l1-info-leaf` with `network_id=0` and the index just returned, on the same bridge-service instance, for its `block_finalized` | Back to `WaitingL1InfoLeafAvailable` (→ `pending`) |

On L2 → L1 the leaf is final when that `block_finalized` is `true`. A bridge service that omits
it (unknown, or a version that predates the field) leaves the leaf not final, so it keeps being
re-checked on every tick until the claim is final. The finality request never moves anything back:
whether the leaf is gone is only decided by `l1-info-tree-index`. It is not sent when
`l1-info-tree-index` answers another index than the `l1_info_tree_index` stored in the step (the
leaf moved): the step then stays not final and is re-checked on every tick, as before.

Moving back reopens that step (`in_progress`, keeping its `start_date`) and sets every later step
back to `pending`, as if never reached; the forward resolution then runs again from there in the
same tick. So `claimed` can go back to `readyToClaim` (only the claim was removed) or to `pending`
(the GER injection or the leaf was removed as well). A backward move is persisted and pushed to
WebSocket subscribers exactly like a forward one.

`bridge_status.block_finalized` is the finality of the origin bridge block: set once the finality
resolver proves the bridge tx final in `block_number` (the tracker resolves the bridge from its
receipt, so there is no bridge-service value to start from), or once the claim is final. It never
goes back to `false`, and the origin is not re-checked anymore once it is `true`.

An error of any of these checks — an RPC failure, or the bridge service answering `503` while it
resolves a reorg of its own — never moves anything backwards: the state is kept and checked again on
the next tick. An error of the forward resolution is still recorded as a transient step error, as
before.

Cost: a tracking that is not final costs, per tick, on top of its forward resolution: one header
call while its origin block is not final (plus at most one receipt, once, when it becomes final);
at `readyToClaim`, one `injected-l1-info-leaf` and one `l1-info-tree-index` request, on every
route, and only until they report it final: the injection on L1 → L2 and L2 → L2, the leaf itself
on L2 → L1 (then none). Against a bridge service that never reports `block_finalized`, an L2 → L1
tracking keeps making both requests on every tick until its claim is final;
once claimed, one `isClaimed()` call and one `GET /bridge/v1/claims` (the finality resolver costs
no call when the bridge service reports `block_finalized`, otherwise one receipt once the claim's
block is at or below the finalized head, which is fetched at most once every 5 s per network). A
final tracking costs nothing. Idle trackings (`IdleTimeout`) are still forgotten whatever their
state.

With the SQLite registry, rows written before this behavior that hold a claim that is not final
are supervised again after the upgrade. On reload (`DBPath`), a row whose origin block is already
known final is not checked against the chain anymore.

## Endpoints

All routes are served under `/tracker/v1`.

| Method | Path | Description |
| --- | --- | --- |
| GET | `/tracker/v1/health` | Health status, instance identity and build info. |
| GET | `/tracker/v1/network/{network_id}/tx/{tx_hash}` | Registers (or looks up) the bridge and returns its current `TrackingData`. |
| GET | `/tracker/v1/network/{network_id}/tx/{tx_hash}/ws` | Same bridge, pushed as a `status` WebSocket message on every change instead of polled. |

The response, both over REST and as each WebSocket `status` message, is a `TrackingData`: its
`bridge_status` field stays `null` until the tracker resolves the creating tx, and `all_steps` is
`null` until then too. `bridge_status.event` carries the facts taken directly from the on-chain
`BridgeEvent` log (origin/destination network and address, amount, leaf type); `block_number`,
`log_index` and `block_timestamp` sit alongside it as the block-level context the event was
found in, not the event's own fields.

The WebSocket connection closes normally (code 1000) once the bridge reaches a terminal state —
`Claimed` with a final claim (`claim_status` `claimed_finalized`), or the tracker giving up trying
to resolve the creating tx at all (invalid tx / not a bridge transaction). A claim that is not
final yet keeps the connection open: a reorg can still move the bridge back. A step-level error on an otherwise-resolved bridge is reported in
`TrackingData.error` but is not terminal: the engine keeps retrying it.

Every error string these endpoints return to a client — a step's `error.description[]`, the
tx-level `error`, the REST/WebSocket `ErrorData.message` and the WebSocket close-frame reason —
has any backend URL, `host:port` or bare IP address replaced with `<redacted-url>` /
`<redacted-host>`, keeping the rest of the message intact. Only application logs keep the real
endpoint; operators need it to debug.

That replacement happens at the API layer, where the value becomes client-facing: the response
marshalers (`ErrorStep`, `ErrorData`, `CertificateData`, `ActivityItem`, `ActivityWarningItem`) and
the WebSocket close frame, which carries its reason as a bare string and so is redacted where it
is built. The tracker's internal objects and its activity store keep the raw error, exactly as the
logs do.

## Configuration

Enable the `TRACKER` component (`--components TRACKER,...`) and configure the `[Tracker]`
section:

```toml
[Tracker]
RetentionPeriod = "10m"
IdleTimeout = "30m"
RegisterResolveTimeout = "3s"
L1BlockFinality = "LatestBlock"
L2BlockFinality = "LatestBlock"
L1FinalizedBlock = "FinalizedBlock"
L2FinalizedBlock = "FinalizedBlock"
MaxTrackedBridges = 100000
L2InjectionLookbackBlocks = 10000000

# Workaround only: uncomment for a destination network whose bridge-service instance does not
# report the L2 block a covering GER was injected at.
# [Tracker.L2GlobalExitRootAddress]
# 1 = "0x..."

[Tracker.AgglayerClient]
Cached = true
[Tracker.AgglayerClient.ConfigurationCache]
TTL = "1s"
Capacity = 100
SendCertificate = "forbidden"
GetCertificateHeader = "cached"
GetEpochConfiguration = "cached"
GetLatestPendingCertificateHeader = "cached"
GetNetworkInfo = "cached"
[Tracker.AgglayerClient.GRPC]
URL = "https://agglayer-dev.polygon.technology"
UseTLS = false
```

- `RetentionPeriod`: how long a terminal bridge (finished — claimed with a final claim —, or failed
  to ever resolve) stays queryable before the tracker forgets it and a later request re-registers it
  from scratch. A claimed bridge whose claim is not final is not terminal: the window does not start
  until it is.
- `IdleTimeout`: how long a bridge — terminal or still active — stays supervised once nobody has
  read it (REST poll) and it has no active WebSocket subscriber. Unlike `RetentionPeriod`, this
  applies regardless of status: a bridge that never resolves and that nobody is watching would
  otherwise stay in memory forever.
- `RegisterResolveTimeout`: how long the first request for a freshly registered tx waits for the
  engine's immediate resolution attempt before answering, so it has a shot at real progress
  instead of the bare `registered` state; a lookup of an already-registered tx never waits.
- `L1BlockFinality` / `L2BlockFinality`: the finality a bridge's creating tx receipt must reach
  before the tracker accepts it. A resolved bridge whose origin block is not final yet is re-checked
  on every tick anyway (see [Reorgs](#reorgs-re-evaluation-and-backward-transitions)), so the
  default `LatestBlock` no longer risks following an orphaned deposit.
- `L1FinalizedBlock` / `L2FinalizedBlock` (default `"FinalizedBlock"` for both): the block the proxy
  considers finalized on L1 (network 0) / on any L2 when it has to decide by itself whether a bridge
  or a claim is final — a bridge or claim mined in a block at or below it is final. It is **not**
  `L1BlockFinality` / `L2BlockFinality`, which only decide when a creating tx's receipt is accepted
  (default `LatestBlock`); the two are independent. Accepted values: `FinalizedBlock`, `SafeBlock`
  and `LatestBlock/-N` (N blocks behind the latest one). See [Block finality of bridges and
  claims](#block-finality-of-bridges-and-claims). **Using anything but `FinalizedBlock` is not
  recommended**: the guarantee that a final bridge or claim is not reorged out is only given for the
  `finalized` tag (it is given at another level, not by the proxy). Any other value makes the proxy
  log a warning at startup.
- `FinalizedBlockByNetwork`: optional networkID → block map overriding `L1FinalizedBlock` /
  `L2FinalizedBlock` for the networks it lists (e.g. an L2 whose RPC does not serve the `finalized`
  tag: `[Tracker.FinalizedBlockByNetwork]` with `3 = "SafeBlock"`). Empty by default; same
  recommendation as above.
- `BridgeAddrs`: a static networkID → canonical bridge contract address **override**, checked before
  falling back to the bridge service finder's on-chain resolution — it is not the only source of the
  address `FindBridge` checks a `BridgeEvent` log's emitter against (see [How it
  works](#how-it-works)). Use it to pin a network's address or work around a finder resolution
  problem; a network absent from this map (the default, empty map) resolves through the finder
  instead — there is no network for which this check can be skipped.
- `MaxTrackedBridges`: caps the in-memory supervised list; a request beyond it fails instead of
  registering the bridge — reaching the cap never evicts an existing entry to make room, so
  `RetentionPeriod` and `IdleTimeout` are what keep the registry under it during normal operation.
- `L2GlobalExitRootAddress`: **workaround only** — a networkID → `GlobalExitRootManagerL2` contract
  address map, used solely as a fallback for a destination network whose bridge-service instance
  does not report the L2 block a covering GER was actually injected at. For a network present
  here, the tracker scans that network's own L2 for the `UpdateHashChainValue` event instead of
  leaving it absent. A network absent from this map (the default, empty map) never gets this
  fallback attempted; it should not be set otherwise.
- `L2InjectionLookbackBlocks`: bounds how many blocks that same fallback scans backwards from the
  destination network's head before giving up, instead of continuing all the way back to genesis.
  Defaults to 10,000,000 blocks when unset or `<= 0`. The scan pages backwards in
  `bridgeservicefinder.DefaultBlockChunkSize`-sized (10,000-block) `eth_getLogs` calls, so on a
  miss (wrong address, injection older than the window, or the event genuinely absent) it can
  issue up to `L2InjectionLookbackBlocks / 10,000` sequential RPC calls before giving up — at the
  default, up to ~1,000 per bridge, once, the first time that bridge's step needs the fallback
  (it is never re-scanned once the step completes). Size it to how far back a genuine injection
  can realistically lag on that network, not larger than needed, especially against a
  rate-limited RPC provider with several bridges resolving concurrently
  (`MaxConcurrentResolutions`).
- `AgglayerClient`: the client used to resolve an L2-originated bridge's covering certificate and
  its status (`PendingInclusion`/`CertificatePending`/`WaitL1SettledGER`). `Cached` is the master
  switch for `ConfigurationCache`'s per-method policy (`false` ignores it entirely). Each method
  is `cached` (served from its own TTL cache), `passthrough` (always calls the agglayer directly,
  the default for a method left unset), or `forbidden` (refused without ever reaching the
  agglayer — the tracker only ever reads agglayer state, so `SendCertificate` is forbidden here).
  `GetLatestSettledCertificateHeader` is intentionally left unset (passthrough): its "latest"
  answer must always be fresh.

### Block finality of bridges and claims

The bridge service reports `block_finalized` (`true`, `false`, or omitted) on each bridge and claim
it returns. The proxy turns it into a plain "is final" answer (`finality.Resolver` in
`bridgetracker/finality`) with these rules:

- An explicit `true` / `false` from the bridge service always wins, and the fallback below is never
  used for it.
- Omitted means **not final**. The field is omitted when the bridge service cannot tell: it is
  older than agglayer/aggkit#1921, the block was stored without its hash, the syncer is
  unavailable, or a reorg is in progress.
- Finality is monotonic: once an entry has been seen final it is kept final, even if another
  bridge-service instance omits the field (or reports `false`) later. Only final answers are
  remembered, in a bounded cache. A `false` that arrives after the entry was seen final is logged
  as a warning: it is expected from an instance that is lagging behind, and otherwise points at a
  reorg deeper than the finality in use.
- There is no negative cache: while the receipt does not match, it is fetched again on every
  refresh, so the entry is corrected as soon as the bridge-service catches up.
- Fallback, only for bridges and claims whose `block_finalized` is omitted: the proxy fetches the
  receipt of the entry's `tx_hash` from the RPC of that network and checks that it is mined, with a
  successful status, in the entry's `block_num` and that this block is at or below the block configured by
  `L1FinalizedBlock` / `L2FinalizedBlock` (`FinalizedBlockByNetwork`). That is one RPC call per
  entry, once: the receipt is not fetched while the block is above the finalized one, and a final
  answer is cached. A receipt that is not found, an RPC error, a reverted receipt (a tx re-mined at the
  same height by a reorg can revert and then emits no event), or a receipt mined in another block
  than `block_num` leaves the entry **not final** — it never removes anything. Block number alone is
  never enough: a stored block can have been replaced by another one with the same number.
- The finalized block of each network is fetched at most once every 5 seconds, not once per
  request.

Bridges found through the RPC log scan fallback of the activity endpoint never carry
`block_finalized`; they stay not final until the bridge service reports them.

### Activity endpoint and reorgs

The activity endpoint (`GET /tracker/v1/activity/from/{from_address}`) uses the same resolver
(see [API.md](bridgetracker/API.md#caching-and-eviction) for the client-facing behaviour):

- **Scan.** Each network's `GET /bridge/v1/bridges` listing is read newest first down to the first
  bridge that is already cached **and final** (and past every cached non-final bridge of that
  network); deeper pages are never read again. Every cached non-final bridge is re-read on each
  refresh: a different `tx_hash` / `block_num` / `block_timestamp` corrects the entry, and its
  absence (once the scan went past its position) removes it. A multi-page scan re-reads page 1 at
  the end, and runs once more if the listing changed while it was paged (its count changed, or the
  re-read page 1 shows a bridge the pass had not listed or listed elsewhere); if it changed
  again during that second pass, the scan counts as failed (see below).
- **Failures.** A bridge service that cannot be read — unreachable, any transient error, a `503`
  while it resolves a reorg, or a listing that kept changing in both passes — never removes anything: its cached entries are kept as they are, a
  warning is added to the response, and the next refresh retries. A failed `isClaimed()` or
  claim-record fetch on a claimed entry keeps the last known claim; a resolver error means "not
  final yet".
- **RPC fallback.** It keeps scanning its recent window on every refresh and is ignored for every
  bridge the bridge service lists (or has cached). An entry it alone found is never final; it is
  removed when the window no longer has it or once the bridge service lists a later deposit of the
  same sender without it.
- **Settled.** An entry stops being refreshed only once it is claimed, its claim record is fetched,
  and both the bridge's block (origin network) and the claim's block (destination network) are
  final; it is then `claimed_finalized` / `finalized`. Until then `isClaimed()` and the claim record
  are checked again on every refresh, and readiness (`l1-info-tree-index` +
  `injected-l1-info-leaf`) is re-checked on every refresh while unclaimed. The finality of the
  injected L1 info leaf is not needed: a final claim implies a final injection.
- **Revision.** Both stores keep the per-address revision up to date on every addition, removal or
  change of an entry (the SQLite store keeps one hash per row and sums them, nothing else is
  persisted), so both give the same value for the same entries, also after a restart. A restart of
  the SQLite store in the middle of a non-final state resumes it: finality flags are stored with
  each entry.

## `[BridgeServiceFinder]` configuration

The `aggkit-proxy` binary shares one `[BridgeServiceFinder]` instance across the `PROXY` request
forwarding, the tracker's canonical-bridge-address resolution (see [How it
works](#how-it-works)) and its own on-chain rollup discovery. Its keys are:

| Key | Default | Meaning |
| --- | --- | --- |
| `RollupManagerAddr` | `{{L1NetworkConfig.RollupManagerAddr}}` | Address of the RollupManager / AgglayerManager contract on L1 from which the set of attached networks is enumerated. |
| `BridgeURLs` | `{}` | Highest-priority static override map from network ID to bridge service base URL (e.g. `1 = "http://bridge-svc-1:5577"`). Never overwritten by on-chain events. The only way to resolve network 0 (L1), which is not enumerated on-chain. |
| `RPCURLs` | `{}` | Static override map from network ID to that network's JSON-RPC endpoint, with the same priority semantics as `BridgeURLs`. Networks absent from this map take their RPC endpoint from the rollup's trusted-sequencer URL. |
| `BridgeAddress` | `{}` | Static override map from network ID to bridge contract address, consulted in priority order: the network's own entry, then `BridgeAddress[0]` (which doubles as the default for every network without an entry — typically the shared L1 bridge), then the rollup manager's own on-chain `BridgeAddress()`. Only networks whose bridge contract differs from that default need an entry. |
| `BlockFinality` | `FinalizedBlock` | Finality level bounding the upper block of each event scan, so the finder does not react to logs that may still be reorged away. |
| `PollInterval` | `30s` | Period between event-scan iterations of the listener loop. |
| `BlockChunkSize` | `10000` | Maximum number of blocks queried per `FilterLogs` request while scanning. |
| `HealthCheckPath` | `/` | HTTP path appended to a resolved bridge service URL to probe liveness. The bridge service serves the same always-200 health handler on both `/` and `/health`. |
| `HealthCheckTimeout` | `5s` | Timeout applied to each health-check HTTP request. |
| `RequireAllHealthyOnStart` | `false` | When `true`, finder startup fails if any resolved bridge service is unreachable; when `false`, unreachable services are cached as unhealthy and may heal from a later on-chain update. |
| `AutoRegisterNewNetworks` | `true` | Controls whether networks discovered after `Start` are served — see below. |
| `IgnoreNetworkIDs` | `[]` | Network IDs (rollup IDs) excluded from resolution entirely: no on-chain reads and no health probe during enumeration, no cache entry even when the network is also present in `BridgeURLs`/`RPCURLs`, and rollup-manager lifecycle events announcing them are ignored by live discovery too. Intended for known-dead networks whose unreachable reads would otherwise slow down startup and event processing. `GetURL` returns `ErrNetworkDisabled` for them. |
| `AllowNetworkIDs` | `[]` | Whitelist counterpart of `IgnoreNetworkIDs`: when non-empty, **only** the listed network IDs are resolved and every other network is excluded exactly as if it were in `IgnoreNetworkIDs` (same skipping at enumeration and live discovery, same `ErrNetworkDisabled` from `GetURL`, never recorded as pending). Combined with `AutoRegisterNewNetworks = true` it lets you list a network ID before that network is attached to the rollup manager. Network `0` (L1) is a regular entry: include `0` to keep serving the L1 bridge service. Empty means no allowlist. **Mutually exclusive with `IgnoreNetworkIDs`**: setting both makes the proxy fail at startup. |

This section covers the
one field that changes what the tracker's health endpoint reports:

- `AutoRegisterNewNetworks` (bool, **TOML default `true`**): controls whether a network discovered
  after the finder's `Start` gets served. When `true` (today's behavior, unqualified), a rollup
  attached to the rollup manager after startup — or a startup-enumerated network that only
  announces its bridge service URL later — is resolved and served immediately, live, without a
  restart. When `false` the **served set is frozen at startup**: neither of those two paths adds a
  new network afterwards. A URL *refresh* of a network already being served is unaffected either
  way — the existing health-gating and source-priority rules keep applying.
- A network blocked by `AutoRegisterNewNetworks = false` is recorded as **pending** (network id,
  rollup contract address, the block of the event that would have activated it, first-seen time,
  and a reason) instead of served, logged once at `Warn`, and listed under `pending_networks` in
  `GET /tracker/v1/health` (see [HealthResponse](bridgetracker/API.md#healthresponse)) — the key
  is omitted once there is nothing pending. When the binary runs with only the `PROXY` component
  enabled (no `TRACKER`, so no health endpoint), pending networks are only visible in the logs.
- **Activation** always requires restarting the service — the startup enumeration is the explicit
  operator step that (re-)serves everything it can resolve at that point, and config is not
  hot-reloaded, so simply adding the network to `BridgeURLs` / `RPCURLs` and leaving the process
  running does nothing. That static override is always installed regardless of this flag, but
  only as of the *next* restart. There is deliberately no admin endpoint to activate a pending
  network any other way.

```toml
[BridgeServiceFinder]
AutoRegisterNewNetworks = true
```

## `[Proxy]` configuration

The `PROXY` component forwards every `/bridge/v1/*` request to the bridge service of the network
selected by `network_id`. It forwards **only `GET` and `HEAD`** (the bridge service API is
read-only) and **no request body**: any other method is answered `405` with `Allow: GET, HEAD`, and
a request carrying a body (also a chunked one) is answered `413` and its connection closed, both
before any bridge service is contacted. (To tell an empty chunked body from a non-empty one, the
proxy reads one byte of it: a chunked request whose first chunk never arrives is held until
`[REST] ReadTimeout`.) A CORS preflight (`OPTIONS`) is answered by the CORS handler
of the shared REST server, not forwarded. These two rules are not configurable; the shared REST
server's `MaxRequestBodyBytes` still applies to every request as a backstop.

The upstream side is bounded by these keys, so a bridge service that accepts a request and never
answers cannot hold it (and its connections) for as long as the client stays connected:

| Key | Default | Meaning |
| --- | --- | --- |
| `UpstreamResponseHeaderTimeout` | `30s` | How long to wait, once the request is sent to a bridge service, for its response headers. Its clock starts only once the request is written: connecting (and a TLS handshake) is bounded by `UpstreamRequestTimeout` and by the transport's own 30s dial timeout, not by this key. A request over it is answered `504`. Keep it **below** `[REST] WriteTimeout`, for the same reason as `UpstreamRequestTimeout` (the proxy warns at startup if it is not). `0` disables it. |
| `UpstreamRequestTimeout` | `2m` | Total budget of a proxied request: connecting, response headers and the streaming of the response body, so it also covers a backend that stalls halfway through its answer. A request over it is answered `504`; if the response had already started (the status is sent), the connection is closed instead, so the client sees a truncated body as an error. Keep it **below** `[REST] WriteTimeout` (the server closes the connection at that point, before the proxy can answer; the proxy warns at startup if it is not below), and **above** the slowest legitimate bridge service response. `0` disables it. |
| `UpstreamMaxConnsPerHost` | `256` | Maximum connections (idle, dialing and in use) to each bridge service. More requests wait for a free one, up to `UpstreamRequestTimeout`. Up to this many idle connections are kept for reuse (net/http keeps 2 otherwise). `0` means no limit. |

All networks' bridge services share these limits. Responses are streamed, not buffered, and the proxy
no longer adds `Accept-Encoding: gzip` on its own to the upstream request (a client's own
`Accept-Encoding` is still relayed, and the response passed through as the bridge service sent it).

```toml
[Proxy]
UpstreamResponseHeaderTimeout = "30s"
UpstreamRequestTimeout = "2m"
UpstreamMaxConnsPerHost = 256
```

## Prometheus Metrics

The tracker exposes (unless `[Prometheus] Enabled = false`, it is enabled by default) the following Prometheus metrics, refreshed every `[Tracker] MetricsSampleInterval` (default `15s`):

| **Metric Name**                  | **Type** | **Description**                                                                                      |
| -------------------------------- | -------- | ---------------------------------------------------------------------------------------------------- |
| `bridgetracker_cache_size_bytes`    | Gauge    | On-disk size of the SQLite cache (main file plus `-wal`/`-shm`); `0` when `Tracker.DBPath` is empty (in-memory) |
| `bridgetracker_alive_trackers`      | Gauge    | Supervised bridges not yet in a terminal state                                                       |
| `bridgetracker_alive_activities`    | Gauge    | `from_address`es currently supervised by the activity subsystem (only present when the activity endpoint is configured) |
| `bridgetracker_activity_reorgs_detected_total`   | Counter  | Activity scans of one network's bridge service (or RPC fallback window) that found a reorg: a cached non-final bridge corrected or removed (only present when the activity endpoint is configured) |
| `bridgetracker_activity_entries_corrected_total` | Counter  | Activity entries re-read at a different block or transaction and corrected in place |
| `bridgetracker_activity_entries_removed_total`   | Counter  | Activity entries removed because a reorg dropped their bridge |

A failed read keeps the gauge's previous value (and logs a warning) instead of reporting a misleading zero. The activity counters are incremented as the scans happen, not sampled.

```toml
[Prometheus]
Enabled = true
Host = "localhost"
Port = 9091
```

The metrics are then available at `http://<Host>:<Port>/metrics`.

> **Note:** aggkit and the proxy both default to `localhost:9091`. If they run on the same host, change `Port` on one of them: the second one to start fails to bind the port, logs an error and keeps running **without** metrics.

## API Documentation

<iframe src="assets/swagger/bridge_tracker/index.html"
  style="width: 100%; height: 90vh; border: none;"
  loading="lazy"></iframe>
