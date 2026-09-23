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
| `Claimed` | Terminal: the bridge has been claimed on the destination network. |

Which steps apply, and in which order, depends on the bridge's direction:

- **L1 → L2**: `WaitingGERUpdate` → `WaitingGERInjection` → `WaitingL1InfoLeafAvailable` → `WaitingClaim` → `Claimed`
- **L2 → L1**: `WaitingLERUpdate` → `PendingInclusion` → `CertificatePending` → `WaitL1SettledGER` → `WaitingL1InfoLeafAvailable` → `WaitingClaim` → `Claimed`
- **L2 → L2**: `WaitingLERUpdate` → `PendingInclusion` → `CertificatePending` → `WaitL1SettledGER` → `WaitingGERInjection` → `WaitingL1InfoLeafAvailable` → `WaitingClaim` → `Claimed`

The whole route is published the moment the creating tx resolves, so a client sees every step it
will walk through before any milestone has been checked — not just the current one.

`TrackingStatus` summarizes the bridge's lifecycle for a client that only needs the high-level
state: `registered` (added to the list, not resolved yet), `running`, `error` (a step, or the
initial resolution itself, failed terminally), or `finished` (claimed).

### Step dates

Each step's `start_date`/`end_date` prefer a deterministic on-chain fact (the block the step's
own milestone was met at) over the instant the tracker happened to poll and notice it — see
[BridgeStepPath](bridgetracker/API.md#bridgesteppath) for exactly which steps have one and how
they chain into each other. A step whose resolver could not fully resolve that fact anyway still
completes normally; it just explains why via its own `error`, with `error_type` `"warning"`
(informational only, not a failure — see [ErrorStep](bridgetracker/API.md#errorstep)).

This only ever applies going forward: a bridge whose steps were already `"done"` before an
upgrade to this behavior keeps whatever dates were recorded under the previous, observation-time
implementation — a step already `"done"` is never resolved again (see "How it works" above), so
there is nothing left to recompute it from. A bridge still in flight at the time of the upgrade
will show a mixed timeline: earlier, already-completed steps keep their old observation-time
dates, later ones get the new on-chain-derived ones. This is expected, not a bug — treat it as an
artifact of the moment a given bridge was resolved, not something to backfill.

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
`Claimed`, or the tracker giving up trying to resolve the creating tx at all (invalid tx / not a
bridge transaction). A step-level error on an otherwise-resolved bridge is reported in
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

- `RetentionPeriod`: how long a terminal bridge (finished, or failed to ever resolve) stays
  queryable before the tracker forgets it and a later request re-registers it from scratch.
- `IdleTimeout`: how long a bridge — terminal or still active — stays supervised once nobody has
  read it (REST poll) and it has no active WebSocket subscriber. Unlike `RetentionPeriod`, this
  applies regardless of status: a bridge that never resolves and that nobody is watching would
  otherwise stay in memory forever.
- `RegisterResolveTimeout`: how long the first request for a freshly registered tx waits for the
  engine's immediate resolution attempt before answering, so it has a shot at real progress
  instead of the bare `registered` state; a lookup of an already-registered tx never waits.
- `L1BlockFinality` / `L2BlockFinality`: the finality a bridge's creating tx receipt must reach
  before the tracker accepts it, so a later reorg cannot leave it permanently following an
  orphaned deposit (a resolved bridge is never re-checked).
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

## `[BridgeServiceFinder]` configuration

The `aggkit-proxy` binary shares one `[BridgeServiceFinder]` instance across the `PROXY` request
forwarding, the tracker's canonical-bridge-address resolution (see [How it
works](#how-it-works)) and its own on-chain rollup discovery. Most of its keys — `RollupManagerAddr`,
`BridgeURLs`/`RPCURLs`, `BlockFinality`, `PollInterval`, health-check settings, `IgnoreNetworkIDs` —
are the same finder used by Auto Claim; see [`AutoClaim.BridgeServiceFinder`
keys](autoclaim.md#top-level-keys) for the full field list and defaults. This section covers the
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

## API Documentation

<iframe src="assets/swagger/bridge_tracker/index.html"
  style="width: 100%; height: 90vh; border: none;"
  loading="lazy"></iframe>
