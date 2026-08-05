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

| Step | Meaning |
| --- | --- |
| `WaitingGERUpdate` | L1-originated bridge: the L1 Global Exit Root has not been updated with this deposit yet. |
| `WaitingLERUpdate` | L2-originated bridge: the origin network's Local Exit Root has not been updated yet. |
| `PendingInclusion` | The bridge is not yet part of any certificate sent to the agglayer. |
| `CertificatePending` | Included in a certificate; waiting for it to settle (covers Pending/Proven/Candidate/InError). |
| `WaitL1SettledGER` | L2-originated only: the certificate settled, waiting for its settlement tx to confirm on L1. |
| `WaitingGERInjection` | Waiting for the covering Global Exit Root to be injected on the destination network. |
| `WaitingClaim` | The bridge is claimable: the covering GER has been injected. |
| `Claimed` | Terminal: the bridge has been claimed on the destination network. |

Which steps apply, and in which order, depends on the bridge's direction:

- **L1 → L2**: `WaitingGERUpdate` → `WaitingGERInjection` → `WaitingClaim` → `Claimed`
- **L2 → L1**: `WaitingLERUpdate` → `PendingInclusion` → `CertificatePending` → `WaitL1SettledGER` → `WaitingClaim` → `Claimed`
- **L2 → L2**: `WaitingLERUpdate` → `PendingInclusion` → `CertificatePending` → `WaitL1SettledGER` → `WaitingGERInjection` → `WaitingClaim` → `Claimed`

The whole route is published the moment the creating tx resolves, so a client sees every step it
will walk through before any milestone has been checked — not just the current one.

`TrackingStatus` summarizes the bridge's lifecycle for a client that only needs the high-level
state: `registered` (added to the list, not resolved yet), `running`, `error` (a step, or the
initial resolution itself, failed terminally), or `finished` (claimed).

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

[Tracker.AgglayerClient]
Cached = true
[Tracker.AgglayerClient.ConfigurationCache]
TTL = "1s"
Capacity = 100
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
- `MaxTrackedBridges`: caps the in-memory supervised list; a request beyond it fails instead of
  registering the bridge — reaching the cap never evicts an existing entry to make room, so
  `RetentionPeriod` and `IdleTimeout` are what keep the registry under it during normal operation.
- `AgglayerClient`: the client used to resolve an L2-originated bridge's covering certificate and
  its status (`PendingInclusion`/`CertificatePending`/`WaitL1SettledGER`).

## API Documentation

<iframe src="assets/swagger/bridge_tracker/index.html"
  style="width: 100%; height: 90vh; border: none;"
  loading="lazy"></iframe>
