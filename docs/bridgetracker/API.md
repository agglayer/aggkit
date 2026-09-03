# API
The API is going to be an API REST: 
GET /tracker/v1/network/{network_id}/tx/{tx_hash}
GET /tracker/v1/activity/from/{from_address}
GET /tracker/v1/bridge-address
GET /tracker/v1/bridge-address/{network_id}
GET /tracker/v1/health

In addition to the REST endpoint, a WebSocket endpoint is provided to receive bridge status updates as they happen (see [WebSocket](#websocket)).

Request (BridgeRequest):

| param | location | type | mandatory | desc |
| ------|----------|------|-----------|------|
| network_id | path | uint32 | yes | network where the bridge transaction was sent (0 -> Mainnet) |
| tx_hash | path | Hash | yes | hash of the transaction that created the bridge (bridgeAsset or bridgeMessage) |

## Behavior

Calling this endpoint **adds the bridge to the list of supervised bridges** (if it was not already being tracked). It always returns:

- `200 OK` — the body is a [TrackingData](#trackingdata). `tracking_status` is `registered` and `bridge_status`/`step_index`/`all_steps` are `null` until the tracker resolves the bridge; from then on `tracking_status` mirrors the bridge's lifecycle (`running`/`error`/`finished`) and those fields carry the full picture (see [BridgeStatus](#bridgestatus)). If the tracker instead gives up trying to resolve the bridge at all — the transaction **does not exist on the network or is not a bridge transaction** (no `BridgeEvent`) — `tracking_status` becomes `error`, `bridge_status`/`step_index`/`all_steps` stay `null` forever, and `error` carries why (see [ErrorStep](#errorstep)). That determination is asynchronous, so a tx may answer with `tracking_status: registered` on the first few polls and `error` only once the tracking engine gives up. When polling, the client keeps calling the endpoint until `tracking_status` moves off `registered`. Over the [WebSocket](#websocket) no polling is needed: every `status` message carries the same `TrackingData`, and the client just watches it evolve.
  - **First-registration head start**: the very first time a tx is registered, the tracker wakes its resolution engine immediately instead of waiting for the next poll round, and this request waits up to `RegisterResolveTimeout` (default `3s`, configurable, `0` disables the wait) for that attempt to land. So the very first response has a real chance of already carrying real progress instead of the bare `registered` state — though it may still come back `registered` if resolution takes longer than the timeout. A lookup of an already-registered tx never waits, regardless of this setting.
- `400 Bad Request` — invalid path parameters (`network_id` not a uint32, `tx_hash` not a valid hash): the body is an [ErrorData](#errordata). Nothing is registered.

## Response types

Implemented in `aggkit/bridgetracker/types/` (`status.go`, `tracking.go`, `health.go`, `websocket.go`), except `TrackingData` itself, which lives in `aggkit/bridgetracker/api/tracking_data.go` — it's the only place that constructs it.

Most enum fields are serialized as a **bare string** (`tracking_status`, `bridge_type`,
`event.leaf_type`, `step_name`, `status`), with the exact value set given per field below. Only
two fields follow a different, numeric convention: `error_type` (in [ErrorStep](#errorstep)) and
`status` (in [CertificateData](#certificatedata)) are serialized as their numeric value **plus** a
companion `<field>_string` field carrying the string representation — that companion is a regular
struct field, auto-populated from the numeric value when the structure is marshaled to JSON.
There is no general rule: check the field's type in the tables below.

Optional fields that use Go's `omitempty` tag (e.g. `start_date`, `end_date`, `result`, `error` on
a step, or `settlement_tx_hash`/`error` on `CertificateData`) are **omitted from the JSON
altogether** when not applicable, not serialized as `null`. `TrackingData.bridge_status`/
`step_index`/`all_steps`/`error` are the exception: those are always present in the response,
explicitly `null` while not applicable, precisely so clients can poll on their presence.

All JSON field names use `snake_case` (they are listed below exactly as they appear on the wire).

## TrackingData

Body of every REST response (always `200 OK`) and of every WebSocket `status` message: the bridge is registered in the supervised list, and `bridge_status`/`step_index`/`all_steps` carry its resolved status once the tracker has it — or `error` carries why the tracker gave up trying to resolve it at all.

| field | type | desc |
| ------|------|------|
| tracking_status | string | bare string, one of `"registered"` (added to the supervised list, `bridge_status`/`step_index`/`all_steps`/`error` still `null`), `"running"` (resolved, alive), `"error"` (either a step reached an error on an otherwise-resolved bridge, or the tracker gave up resolving the bridge at all — see `error`), `"finished"` (resolved, reached `Claimed`) |
| network_id | uint32 | network of the request |
| tx_hash | Hash | transaction hash of the request |
| bridge_status | *BridgeStatus | `null` while tracking_status is `registered`, and forever `null` if the tracker gives up resolving the bridge (`error` is set instead); see [BridgeStatus](#bridgestatus) |
| step_index | *int | `null` under the same conditions as `bridge_status`; from then on, the index into `all_steps` of the step that explains `tracking_status`: the step in progress when `running`, the step in error when `error`, or the last step (`Claimed`) when `finished` |
| all_steps | BridgeStepPath [] | `null` under the same conditions as `bridge_status`; from then on, all expected steps of the bridge's route — GER/LER, certificate and claim data are reported per step in each entry's `result` (see [StepResult](#stepresult)) |
| error | *ErrorStep | `null` unless the tracker gave up trying to resolve the bridge at all (e.g. the tx does not exist on the network or is not a bridge transaction); see [ErrorStep](#errorstep). Unrelated to per-step errors, which live in `all_steps[i].error` instead |

Example (unresolved, just registered):

```json
{
  "tracking_status": "registered",
  "network_id": 1,
  "tx_hash": "0x0000000000000000000000000000000000000000000000000000000000000001",
  "bridge_status": null,
  "step_index": null,
  "all_steps": null,
  "error": null
}
```

Example (resolved, one step in progress — trimmed to two `all_steps` entries for brevity; a real
response carries every step of the bridge's route):

```json
{
  "tracking_status": "running",
  "network_id": 1,
  "tx_hash": "0x0000000000000000000000000000000000000000000000000000000000000001",
  "bridge_status": {
    "bridge_type": "L2->L1",
    "block_number": 1000,
    "log_index": 2,
    "block_timestamp": 1700000000,
    "event": {
      "leaf_type": "Asset",
      "origin_network": 1,
      "origin_address": "0x0000000000000000000000000000000000000020",
      "destination_network": 0,
      "destination_address": "0x0000000000000000000000000000000000000030",
      "amount": "100",
      "deposit_count": 7
    }
  },
  "step_index": 1,
  "all_steps": [
    {
      "step_index": 0,
      "step_name": "WaitingLERUpdate",
      "status": "done",
      "start_date": "2026-08-10T10:00:00Z",
      "end_date": "2026-08-10T10:00:05Z",
      "result": {
        "network_id": 1,
        "ler": "0x000000000000000000000000000000000000000000000000000000000000000b",
        "block_number": 998
      }
    },
    {
      "step_index": 1,
      "step_name": "PendingInclusion",
      "status": "inProgress",
      "start_date": "2026-08-10T10:00:05Z"
    }
  ],
  "error": null
}
```

## BridgeStatus

`BridgeStatus` is not flat: the facts taken directly off the on-chain `BridgeEvent` log
(leaf type, origin/destination, amount, deposit count) nest under an `event` object
([BridgeEventData](#bridgeeventdata)); `bridge_status` itself only carries the direction and the
block-level context (block number/index/timestamp) around that event.

| field | type | desc |
| ------|------|------|
| bridge_type | string | bare string, one of `"L1->L2"`, `"L2->L1"`, `"L2->L2"` |
| block_number | uint64 | block, on the origin network, where the `BridgeEvent` (`bridgeAsset`/`bridgeMessage`) was emitted |
| log_index | uint32 | position of the `BridgeEvent` log within `block_number` |
| block_timestamp | uint64 | timestamp of the block, on the origin network, where the `BridgeEvent` was emitted |
| event | BridgeEventData | facts unpacked directly from the on-chain `BridgeEvent` log, see [BridgeEventData](#bridgeeventdata) |

### BridgeEventData

| field | type | desc |
| ------|------|------|
| leaf_type | string | bare string, one of `"Asset"` (`bridgeAsset`), `"Message"` (`bridgeMessage`) |
| origin_network | uint32 | network where the bridged asset originates from |
| origin_address | Address | address of the asset on the origin network |
| destination_network | uint32 | network the bridge exits to (0 -> Mainnet) |
| destination_address | Address | address that receives the asset on the destination network |
| amount | string | amount of the asset being bridged, as a **decimal string** (not a JSON number) — avoids precision loss on wei-scale amounts in clients that decode numbers as `float64` (e.g. JavaScript) |
| deposit_count | uint32 | index of the bridge leaf in the origin exit tree |

Example (`TrackingData.bridge_status` once resolved):

```json
{
  "bridge_type": "L2->L1",
  "block_number": 1000,
  "log_index": 2,
  "block_timestamp": 1700000000,
  "event": {
    "leaf_type": "Asset",
    "origin_network": 1,
    "origin_address": "0x0000000000000000000000000000000000000020",
    "destination_network": 0,
    "destination_address": "0x0000000000000000000000000000000000000030",
    "amount": "100",
    "deposit_count": 7
  }
}
```

## BridgeStepPath
| field | type | desc |
| ------|------|------|
| step_index | int | this step's position within the parent `TrackingData.all_steps` list |
| step_name | string | bare string, one of `"WaitingGERUpdate"`, `"WaitingLERUpdate"`, `"PendingInclusion"`, `"CertificatePending"`, `"WaitL1SettledGER"`, `"WaitingL1InfoLeafAvailable"`, `"WaitingGERInjection"`, `"WaitingClaim"`, `"Claimed"` |
| status | string | bare string, one of `"pending"`, `"inProgress"`, `"done"`, `"error"` |
| start_date | *time.Time | **omitted** (no key) while `nil`, not serialized as `null` |
| end_date | *time.Time | **omitted** (no key) while `nil`, not serialized as `null` |
| expected_duration | *Duration | reserved for a future per-step protocol duration estimate; serializes as a human-readable string (e.g. `"5m0s"`) when set, **omitted** otherwise — no resolver currently populates it, so it never appears on the wire today; do not rely on it |
| result | *StepResult | data produced by the step once it completes; its shape depends on `step_name` (see [StepResult](#stepresult)). **Omitted** (no key) until the step produces it, and for steps without a result |
| error | *ErrorStep | error details, only set when `status` is `"error"` (see [ErrorStep](#errorstep)). **Omitted** (no key) otherwise |

Example (an in-progress step, and a completed one with a result):

```json
{
  "step_index": 0,
  "step_name": "WaitingGERUpdate",
  "status": "inProgress",
  "start_date": "2026-08-10T10:00:00Z"
}
```

```json
{
  "step_index": 2,
  "step_name": "PendingInclusion",
  "status": "done",
  "start_date": "2026-08-10T10:00:05Z",
  "end_date": "2026-08-10T10:02:00Z",
  "result": {
    "certificate_id": "0x000000000000000000000000000000000000000000000000000000000000000f",
    "new_ler": "0x0000000000000000000000000000000000000000000000000000000000000010"
  }
}
```

## StepResult

Carried in the `result` field of a [BridgeStepPath](#bridgesteppath). Its shape depends on the step that produced it:

| step | result fields | desc |
| --- | --- | --- |
| WaitingGERUpdate | `l1_info_tree_index` (uint32), `ger` (Hash), `mer` (Hash), `rer` (Hash), `block_number` (uint64), `block_timestamp` (uint64), `log_index` (uint) | GER resulting from the update on L1, the L1 info tree leaf index it landed at, and the block where it was updated |
| WaitingLERUpdate | `network_id` (uint32), `ler` (Hash), `block_number` (uint64) | LER resulting from the update on the origin L2 and the block where it was updated |
| PendingInclusion | `certificate_id` (Hash), `new_ler` (Hash), `previous_ler` (*Hash) | the certificate that first includes the bridge and the LER transition it produced; `previous_ler` is nil for a network's first certificate |
| CertificatePending | [CertificateData](#certificatedata) | the certificate's current data; set as soon as a certificate exists, updated as its status changes (Pending, Proven, Candidate, InError), and reflects the final settled data — including `block_number`/`block_timestamp` — once `status` is `done` |
| WaitL1SettledGER | `tx_hash` (Hash), `settlement_block_number` (uint64), `settlement_block_timestamp` (uint64), `settlement_log_index` (uint), `ger` (Hash), `ger_block_number` (uint64), `ger_block_timestamp` (uint64), `ger_log_index` (uint), `l1_info_tree_index` (*uint32), `has_verify_batches_trusted_aggregator` (bool), `has_update_l1_info_tree` (bool), `has_update_l1_info_tree_v2` (bool) | evidence, read off the certificate's settlement tx receipt once it reaches L1 finality, that the settlement propagated to the L1 Global Exit Root; `ger` is computed from `UpdateL1InfoTree`'s mainnet/rollup exit roots, and `ger_block_number`/`ger_block_timestamp`/`ger_log_index` locate the event it was computed from — normally the same block as the settlement, but the closest earlier one on L1 when the settlement tx's own receipt didn't move the GER itself. `l1_info_tree_index` is the leaf `ger` landed at — populated straight from `UpdateL1InfoTreeV2`'s `LeafCount` when that (optional) event fires, otherwise resolved with one extra GER->leaf lookup before the step can complete; it is never `null` once the step is `done`. The two `has_*` booleans besides `has_update_l1_info_tree_v2` are required for the step to complete, that third one is informational only |
| WaitingL1InfoLeafAvailable | `ger` (Hash), `block_number` (uint64), `block_timestamp` (uint64) | L2->L1 only (see [#1823](https://github.com/agglayer/aggkit/issues/1823)): the L1 info tree leaf produced by `WaitL1SettledGER`'s settlement, as seen by the destination network's own bridge-service instance — `block_number`/`block_timestamp` are the L1 event that produced the leaf, the same fields as `InjectedGERResult`'s own `l1_info_tree_leaf` (see [InjectedGERResult](#injectedgerresult)), just not nested under it since there is no destination-side injection to report alongside |
| WaitingGERInjection | [InjectedGERResult](#injectedgerresult) | GER covering the bridge: the L1 Info Tree leaf it resolves to, and — once known — the actual L2 block/timestamp it was injected at |
| Claimed | `claim_tx` (Hash), `block_number` (uint64), `block_timestamp` (uint64) | claim transaction on the destination network, its block and that block's timestamp |
| any other step | — | no result: always `nil` |

## ErrorStep

The same structure carries two different kinds of error, depending on where it appears:

- in the `error` field of a [BridgeStepPath](#bridgesteppath), when that step's `status` is `"error"` — a step of an otherwise-resolved bridge failed;
- in the `error` field of [TrackingData](#trackingdata) — the tracker gave up trying to resolve the bridge at all (e.g. `bridgeAsset`/`bridgeMessage` tx not found, or the tx exists but emitted no `BridgeEvent`). In that case `retry_count` counts the not-found polls before giving up.

| field | type | desc |
| ------|------|------|
| error_type | ErrorType (int) | 0->transient, 1->permanent, 2->exhausted (retries have been given up on) |
| error_type_string | string | string representation of error_type (e.g. "transient") |
| retry_count | int | number of retries attempted so far |
| description | string [] | human-readable description(s) of the error, one entry per occurrence |

## GERData

**Not part of any tracker response.** `GERData` (`bridgetracker/types/status.go`) is the domain
layer's internal currency to decide GER coverage (`GERSource.OriginGER`/`InjectedGER`); it is
never embedded in `TrackingData`, `BridgeStatus` or `BridgeStepPath`. It has JSON tags and its own
`MarshalJSON` (documented here for completeness in case it is ever exposed or logged), but no
tracker endpoint or WebSocket message currently serializes it.

| field | type | desc |
| ------|------|------|
| network_id | uint32 | Network (0->Mainnet)
| ger  | *Hash |  Global Exit Root
| mer | *Hash | Mainnet Exit Root
| rer | *Hash | Rollup Exit Root
| ler | *Hash | Local Exit Root
| ler_type | LERType (int) | 0->NA, 1->Mainnet , 2-> Local
| ler_type_string | string | string representation of ler_type (e.g. "Mainnet")

## InjectedGERResult

The result of `WaitingGERInjection` once it completes: the GER covering the bridge, split into
where it comes from on each side. `l1_info_tree_leaf`'s `block_number`/`block_timestamp` are
always the **L1** `UpdateL1InfoTree`/`UpdateL1InfoTreeV2` event that produced the leaf — never the
block it was actually injected at on the destination network. `l2_injected_ger` carries that
separately, and is the fix for a bug where the L1 block was returned in its place (making the
result useless for calculating L2-side injection timing).

`l2_injected_ger` is resolved two ways, in order:

1. Straight from the destination's bridge-service instance (`injected_l2_block_num`/
   `injected_l2_block_timestamp` on `GET /bridge/v1/injected-l1-info-leaf`, see
   [REFERENCE_API.md](REFERENCE_API.md)) — the common case.
2. If that instance predates those fields, and the destination network's
   `GlobalExitRootManagerL2` contract address is configured (`Tracker.L2GlobalExitRootAddress`,
   `bridgetracker/config.go`), the tracker falls back to scanning that network's own
   `UpdateHashChainValue` logs backwards from latest until it finds the one that injected this
   GER (`GERSource.findL2InjectionBlockBackwards`, `bridgetracker/sources/ger.go`).

`l2_injected_ger` is **omitted** (no key), not `null`, when neither resolves it — the bridge-service
doesn't report it and either no `L2GlobalExitRootAddress` entry is configured for the network or the
backward scan itself failed or found nothing (logged as a warning, never fails the step).

| field | type | desc |
| ------|------|------|
| l1_info_tree_leaf.ger | Hash | Global Exit Root covering the bridge |
| l1_info_tree_leaf.block_number | uint64 | L1 block of the event that produced the leaf |
| l1_info_tree_leaf.block_timestamp | uint64 | that L1 block's timestamp |
| l2_injected_ger | *object | **omitted** (no key) while neither resolution path above produces a value |
| l2_injected_ger.block_number | uint64 | L2 block where the GER was actually injected on the destination network |
| l2_injected_ger.block_timestamp | *uint64 | that L2 block's timestamp; **omitted** (no key) — separately from `l2_injected_ger` itself — if it could not yet be resolved (e.g. the bridge-service's own RPC backfill, see [REFERENCE_API.md](REFERENCE_API.md), hasn't succeeded yet), resolving on a later request |

Example (fully resolved):

```json
{
  "l1_info_tree_leaf": {
    "ger": "0x330d1f1546dc784aa465fdf83fb9d88e0a3778064d74e182c1dfb803ef155c1",
    "block_number": 11606405,
    "block_timestamp": 1788188124
  },
  "l2_injected_ger": {
    "block_number": 11606512,
    "block_timestamp": 1788188250
  }
}
```

Example (destination bridge-service instance not yet upgraded, or `l2_injected_ger.block_timestamp`
still resolving):

```json
{
  "l1_info_tree_leaf": {
    "ger": "0x330d1f1546dc784aa465fdf83fb9d88e0a3778064d74e182c1dfb803ef155c1",
    "block_number": 11606405,
    "block_timestamp": 1788188124
  }
}
```

## CertificateData

This is one of the two response fields that **does** follow the numeric+`_string` convention
(the other is [ErrorStep.error_type](#errorstep)) — everything else in the tracker response is a
bare string, see the note at the top of [Response types](#response-types).

| field | type | desc |
| ------|------|------|
| certificate_id | Hash |
| status | CertificateStatus (int) | Mapped from proto in [agglayer_grpc_client.go:559](aggkit/agglayer/grpc/agglayer_grpc_client.go#L559): 0->`Pending`, 1->`Proven`, 2->`Candidate`, 3->`InError`, 4->`Settled`
| status_string | string | string representation of status (e.g. "Settled")
| error | string | Only set if the proto carries `Error.Message` (relevant for `InError` certs); **omitted** (no key) otherwise |
| settlement_tx_hash | *Hash | Set once the certificate has a settlement tx (normally only from `Settled` onward); **omitted** (no key), not `null`, before that |
| block_number | *uint64 | The L1 block `settlement_tx_hash` was mined in; **omitted** (no key), not `null`, until it is visible there — which can lag a tick behind `status` turning `Settled` |
| block_timestamp | *uint64 | `block_number`'s timestamp; same omit/lag rules as `block_number` |

Example (settled certificate, as it appears in `all_steps[i].result` for `CertificatePending`):

```json
{
  "certificate_id": "0x0000000000000000000000000000000000000000000000000000000000000001",
  "status": 4,
  "status_string": "Settled",
  "settlement_tx_hash": "0x0000000000000000000000000000000000000000000000000000000000000002",
  "block_number": 400,
  "block_timestamp": 1700000400
}
```

## ErrorData

Error structure **shared by the REST `400` response and the WebSocket `error` message**, both reserved for invalid request parameters (bad `network_id`/`tx_hash`) — before any bridge is registered, so there is no [TrackingData](#trackingdata) to carry it yet. Once a bridge is registered, every outcome — including the tracker giving up on it — is reported through `TrackingData` instead (see `error` there).

| field | type | desc |
| ------|------|------|
| code | int | HTTP-like error code: always 400 (invalid params) |
| message | string | human-readable description |

## Health

GET /tracker/v1/health

Health-check endpoint: no parameters, no side effects (it does **not** register anything in the supervised list). Useful as liveness/readiness probe and to check which build is running on each instance behind the proxy.

Always returns `200 OK` with a `HealthResponse` body:

### HealthResponse

| field | type | desc |
| ------|------|------|
| status | string | always `"ok"` |
| instance_id | string | UUID generated at startup; changes on every execution. Two responses with different `instance_id` come from different instances (or the same instance after a restart) |
| config_sha1 | string | sha1sum (hex) of the configuration the instance was started with; allows checking that all instances run the same configuration. The binary accepts several `--cfg` files, so the hash is computed over the **concatenation of the config files in the order they were passed** |
| version | VersionInfo | build/version information of the running instance |

### VersionInfo

Populated from `aggkit.GetVersion()` ([version.go](aggkit/version.go), `FullVersion` struct — `Version`, `GitRev`, `GitBranch` and `BuildDate` are injected at build time).

| field | type | desc |
| ------|------|------|
| version | string | semantic version (e.g. `v0.1.0`) |
| git_rev | string | git revision the binary was built from |
| git_branch | string | git branch the binary was built from |
| build_date | string | build timestamp |
| go_version | string | Go runtime version (e.g. `go1.24.0`) |
| os | string | target OS (e.g. `linux`) |
| arch | string | target architecture (e.g. `amd64`) |

Example:

```json
{
  "status": "ok",
  "instance_id": "3f1c9a2e-8b4d-4f6a-9c0e-5d7b2a1e4c8f",
  "config_sha1": "2ef7bde608ce5404e97d5f042f95f89f1c232871",
  "version": {
    "version": "v0.1.0",
    "git_rev": "a1b2c3d",
    "git_branch": "main",
    "build_date": "Fri, 17 Jun 1988 01:58:00 +0200",
    "go_version": "go1.24.0",
    "os": "linux",
    "arch": "amd64"
  }
}
```

## Activity

GET /tracker/v1/activity/from/{from_address}

Answers "what bridges has this address sent, and what is their claim state" across **every
bridge service the tracker currently knows about** (via the bridge service finder), instead of
one network/tx at a time like the main endpoint. Results are cached per `from_address` (see
[Caching and eviction](#caching-and-eviction) below).

Request:

| param | location | type | mandatory | desc |
| ------|----------|------|-----------|------|
| from_address | path | Address | yes | address that sent the bridges to look up |
| includeTracking | query | bool | no | `true` additionally registers every still-unclaimed bridge in the result with the bridge tracker (same effect as calling the main endpoint for it) and includes its current [TrackingData](#trackingdata) snapshot. Default `false` |
| filterBridges | query | string | no | one of `"all"` (default), `"claimed"`, `"pending"`, `"error"` — restricts the result to bridges with only that `claimed` state |

### Behavior

- `200 OK` — the body is an [ActivityResponse](#activityresponse).
- `400 Bad Request` — invalid `from_address`, or an unrecognized `filterBridges` value: the body is an [ErrorData](#errordata).
- `500 Internal Server Error` — scanning the configured bridge services failed: the body is an [ErrorData](#errordata).
- **This endpoint is opt-in**: it only exists if the binary is configured with both an activity bridge scanner and claim checker (`Config.ActivityScanner`/`ActivityClaims`); otherwise the route is not registered at all (plain `404`).
- Requesting `filterBridges=pending` or `filterBridges=error` **skips fetching the claim record** of a bridge found to be claimed, since it would be filtered out of that result anyway — its cache entry simply has no `claim` yet, and is fetched normally the next time `filterBridges=all`/`claimed` is used for that address.
- A network whose bridge service could not be scanned **never fails the request**: it is skipped and reported in `warnings` instead, so `bridges` is still whatever every other network reported (possibly incomplete for the networks listed in `warnings`).

### ActivityResponse

| field | type | desc |
| ------|------|------|
| from_address | Address | the address requested |
| bridges | ActivityItem [] | every bridge found for `from_address`, across every configured bridge service, matching `filterBridges` |
| warnings | ActivityWarningItem [] | every network whose bridge service could not be scanned this call; **omitted** (no key) when every configured network was scanned successfully |

### ActivityWarningItem

| field | type | desc |
| ------|------|------|
| network_id | uint32 | the network whose bridge service could not be scanned |
| message | string | the error encountered while scanning `network_id` |

### ActivityItem

`bridge` and `claim` are the bridge service's own response shapes, reported **exactly as-is**
(see [BridgeResponse](#bridgeresponse) / [ClaimResponse](#claimresponse) below) — this endpoint
is a cache over that data, not a reinterpretation of it. `bridge_network_id`/`claim_network_id`
sit alongside them (not nested inside) so the caller knows which bridge service produced each one.

| field | type | desc |
| ------|------|------|
| bridge | BridgeResponse | raw bridge event, exactly as returned by the bridge service that reported it |
| bridge_network_id | uint32 | the network whose bridge service returned `bridge` — i.e. the network the bridge-creating tx was actually sent to. **Not** the same as `bridge.origin_network`, which is the origin network of the bridged *asset* and can differ when re-bridging an asset that itself originated on a third network |
| claimed | string | bare string, tri-state result of the destination bridge contract's `isClaimed()` call the last time it was checked: `"false"` (confirmed unclaimed), `"true"` (claimed), or `"error"` if the check itself failed (e.g. no bridge contract address configured for the destination network) — callers must **not** read `"error"` as `"false"` |
| claim_network_id | uint32 | network whose bridge service reported `claim` (the bridge's destination network); **omitted** (no key) until `claim` is present |
| claim | ClaimResponse | raw claim record, exactly as returned by the destination network's bridge service, once `claimed` is `"true"` and the indexer has recorded it; **omitted** (no key) until then |
| creation_timestamp | uint64 | unix seconds; when this bridge was first cached by this endpoint — never changes after that |
| last_updated_timestamp | uint64 | unix seconds; when this item's claim/tracking state was last (re)checked, whether or not anything about it actually changed. Stops advancing once the bridge is claimed with its claim record fetched, since it is never rechecked again from that point on |
| tracking | TrackingData | the bridge tracker's current status for this bridge (see [TrackingData](#trackingdata)); **omitted** (no key) unless the request set `includeTracking=true` and the bridge is still unclaimed |
| errors | map[string]string | message of whatever check failed the last time this item was refreshed, keyed by which check it was — currently only `"claim"`, present only when `claimed` is `"error"`. **Omitted** (no key) while nothing has failed |

### BridgeResponse

Exactly as returned by the origin network's own bridge service (`GET /bridge/v1/bridges`); not reinterpreted.

| field | type | desc |
| ------|------|------|
| block_num | uint64 | block number where the bridge event was recorded |
| block_pos | uint64 | position of the bridge event within the block |
| from_address | Address | address that initiated the transaction on the bridge contract; may be absent |
| tx_hash | Hash | hash of the transaction that included the bridge event |
| global_index | string | global index of the bridge event (mainnet flag + rollup id + deposit count), serialized as a decimal string |
| block_timestamp | uint64 | timestamp of the block containing the bridge event |
| leaf_type | uint8 | 0 -> asset, 1 -> message |
| origin_network | uint32 | network where the bridge transaction originated |
| origin_address | Address | address of the token/sender on the origin network |
| destination_network | uint32 | network the bridge transaction is destined to |
| destination_address | Address | address of the receiver on the destination network |
| amount | string | amount being bridged, as a decimal string |
| metadata | string | optional metadata attached to the bridge event |
| deposit_count | uint32 | deposit index in the origin exit tree |
| bridge_hash | Hash | unique hash identifying the bridge event |
| txn_sender | Address | address that sent the transaction |
| to_address | Address | recipient contract of the transaction (may differ from the bridge contract) |

### ClaimResponse

Exactly as returned by the destination network's own bridge service (`GET /bridge/v1/claims`); not reinterpreted.

| field | type | desc |
| ------|------|------|
| block_num | uint64 | block number where the claim was processed |
| block_timestamp | uint64 | timestamp of the block containing the claim |
| tx_hash | Hash | transaction hash of the claim |
| global_index | string | global index of the claim, as a decimal string |
| origin_address | Address | address initiating the claim on the origin network |
| origin_network | uint32 | origin network id |
| destination_address | Address | address receiving the claim on the destination network |
| destination_network | uint32 | destination network id |
| amount | string | amount claimed, as a decimal string |
| from_address | Address | address the claim originated from |
| mainnet_exit_root | Hash | mainnet exit root associated with the claim |
| rollup_exit_root | Hash | rollup exit root associated with the claim |
| global_exit_root | Hash | global exit root associated with the claim |
| proof_local_exit_root | Proof | local exit root proof; **omitted** (no key) unless the bridge service was asked to include proofs |
| proof_rollup_exit_root | Proof | rollup exit root proof; **omitted** (no key) unless the bridge service was asked to include proofs |
| metadata | string | metadata associated with the claim |
| is_message | bool | `true` for a message claim (leaf type 1), `false` for an asset claim |

Example (one claimed bridge, one still-pending bridge with `?includeTracking=true`):

```json
{
  "from_address": "0x1111111111111111111111111111111111111111",
  "bridges": [
    {
      "bridge": {
        "block_num": 1000,
        "block_pos": 0,
        "tx_hash": "0x0000000000000000000000000000000000000000000000000000000000000001",
        "global_index": "4294967296",
        "block_timestamp": 1700000000,
        "leaf_type": 0,
        "origin_network": 1,
        "origin_address": "0x0000000000000000000000000000000000000020",
        "destination_network": 2,
        "destination_address": "0x0000000000000000000000000000000000000030",
        "amount": "100",
        "metadata": "0x",
        "deposit_count": 7,
        "bridge_hash": "0x0000000000000000000000000000000000000000000000000000000000000abc",
        "txn_sender": "0x1111111111111111111111111111111111111111",
        "to_address": "0x0000000000000000000000000000000000000030"
      },
      "bridge_network_id": 1,
      "claimed": "true",
      "claim_network_id": 2,
      "claim": {
        "block_num": 1050,
        "block_timestamp": 1700003600,
        "tx_hash": "0x0000000000000000000000000000000000000000000000000000000000000002",
        "global_index": "4294967296",
        "origin_address": "0x0000000000000000000000000000000000000020",
        "origin_network": 1,
        "destination_address": "0x0000000000000000000000000000000000000030",
        "destination_network": 2,
        "amount": "100",
        "from_address": "0x1111111111111111111111111111111111111111",
        "mainnet_exit_root": "0x0000000000000000000000000000000000000000000000000000000000000010",
        "rollup_exit_root": "0x0000000000000000000000000000000000000000000000000000000000000011",
        "global_exit_root": "0x0000000000000000000000000000000000000000000000000000000000000012",
        "metadata": "0x",
        "is_message": false
      },
      "creation_timestamp": 1700000100,
      "last_updated_timestamp": 1700003700
    },
    {
      "bridge": {
        "block_num": 1200,
        "block_pos": 1,
        "tx_hash": "0x0000000000000000000000000000000000000000000000000000000000000003",
        "global_index": "4294967297",
        "block_timestamp": 1700010000,
        "leaf_type": 0,
        "origin_network": 1,
        "origin_address": "0x0000000000000000000000000000000000000020",
        "destination_network": 2,
        "destination_address": "0x0000000000000000000000000000000000000030",
        "amount": "50",
        "metadata": "0x",
        "deposit_count": 8,
        "bridge_hash": "0x0000000000000000000000000000000000000000000000000000000000000def",
        "txn_sender": "0x1111111111111111111111111111111111111111",
        "to_address": "0x0000000000000000000000000000000000000030"
      },
      "bridge_network_id": 1,
      "claimed": "false",
      "creation_timestamp": 1700010100,
      "last_updated_timestamp": 1700010100,
      "tracking": {
        "tracking_status": "running",
        "network_id": 1,
        "tx_hash": "0x0000000000000000000000000000000000000000000000000000000000000003",
        "bridge_status": null,
        "step_index": null,
        "all_steps": null,
        "error": null
      }
    }
  ]
}
```

### Caching and eviction

- **Opt-in and always-on caching**: the endpoint only exists when configured (see [Behavior](#behavior-1) above); when it does, results for a given `from_address` are cached in memory across calls: a bridge already confirmed claimed, with its claim record already fetched, is never re-verified again. Every other bridge (new, still unclaimed, claimed but not yet indexed, or errored) is rechecked on every call — without re-walking already-scanned pages of the underlying bridge services, since each network's scan stops as soon as it reaches a bridge already in the cache.
- **Idle eviction**: a `from_address` nobody has asked about in `Tracker.ActivityIdleTimeout` (default 30 minutes, same idea as the main endpoint's `IdleTimeout`) is forgotten entirely on the next request for it — everything cached for it (bridges, claim state) is freed, and it starts fresh exactly as if it were being queried for the first time.
- **`includeTracking=true` registers, it does not wait**: unlike the main tracker endpoint, this does not wait for the tracking engine's first resolution attempt — it registers the bridge (if not already registered) and reports whatever `TrackingData` snapshot is available right away, which may still be the bare `"registered"` state.

## Bridge Address

GET /tracker/v1/bridge-address

GET /tracker/v1/bridge-address/{network_id}

Reports the bridge contract address of one network, or of **every network the tracker currently
knows about** (via the bridge service finder), without needing a fixed config list. With no
`network_id` the body is a [BridgeAddressResponse](#bridgeaddressresponse); with `network_id` the
body is a single [BridgeAddressItem](#bridgeaddressitem).

Request:

| param | location | type | mandatory | desc |
| ------|----------|------|-----------|------|
| network_id | path | uint32 | no | network to look up; omit to get every network |

### Behavior

- `200 OK` — the body is a [BridgeAddressResponse](#bridgeaddressresponse) (no `network_id`) or a [BridgeAddressItem](#bridgeaddressitem) (`network_id` given).
- `400 Bad Request` — `network_id` is not a uint32: the body is an [ErrorData](#errordata).
- `500 Internal Server Error` — resolving the bridge contract address failed (e.g. the on-chain rollup manager lookup failed): the body is an [ErrorData](#errordata).
- **This endpoint is opt-in**: it only exists if the binary is configured with a bridge address resolver (`Config.BridgeAddressResolver`); otherwise both routes are not registered at all (plain `404`).

### BridgeAddressResponse

| field | type | desc |
| ------|------|------|
| bridges | BridgeAddressItem [] | the bridge contract address of every network the tracker currently knows about |

### BridgeAddressItem

| field | type | desc |
| ------|------|------|
| network_id | uint32 | the network `bridge_address` belongs to |
| bridge_address | Address | the bridge contract address on `network_id` |

Example, `GET /bridge-address`:

```json
{
  "bridges": [
    { "network_id": 0, "bridge_address": "0x1111111111111111111111111111111111111111" },
    { "network_id": 1, "bridge_address": "0x2222222222222222222222222222222222222222" }
  ]
}
```

Example, `GET /bridge-address/1`:

```json
{
  "network_id": 1,
  "bridge_address": "0x2222222222222222222222222222222222222222"
}
```

## WebSocket

Endpoint to subscribe to a bridge and receive its status updates as they happen, instead of polling the REST endpoint.

GET /tracker/v1/network/{network_id}/tx/{tx_hash}/ws

Same path parameters as the REST endpoint (`network_id`, `tx_hash`). The request is upgraded to a WebSocket connection (`Upgrade: websocket`).

### Server messages

All messages are JSON text frames with an envelope:

| field | type | desc |
| ------|------|------|
| type | string | `status` or `error` |
| data | object | `TrackingData` for `status`, `ErrorData` for `error` |

- Connecting adds the bridge to the list of supervised bridges, same as the REST endpoint.
- On connect, the server immediately sends a `status` message with the current [TrackingData](#trackingdata) — `bridge_status`/`step_index`/`all_steps` are `null` if the tracker has no information yet, or populated if it does.
- After that, a new `status` message is pushed every time something changes: `tracking_status` moves, `bridge_status`/`step_index`/`all_steps` go from `null` to populated, `step_index` moves, a step status/date in `all_steps` changes, or the certificate status changes. Each message carries the full `TrackingData`, not a delta.
- When the bridge reaches a terminal state — `Claimed` (`tracking_status == "finished"`), or the tracker giving up trying to resolve it at all (`tracking_status == "error"` with `bridge_status: null` and `error` set) — the server sends the final `status` message and closes the connection with code `1000` (normal closure). A step-level error on an otherwise-resolved bridge (`tracking_status == "error"` with `bridge_status` populated) is **not** terminal: the connection stays open and the engine keeps polling in case it clears.

### Errors

An `error` message ([ErrorData](#errordata)) is sent, followed by the closure of the connection with code `1008` (policy violation), only when the path parameters themselves are invalid (`code=400`) — mirrors the REST `400`. This happens before any bridge is registered, so there is no [TrackingData](#trackingdata) yet. Once a bridge is registered, giving up on it (tx not found / not a bridge transaction) is reported as a normal `status` message instead (see [Server messages](#server-messages) above), not as an `error` message.

### Keepalive

The server sends WebSocket `ping` frames periodically; the client must answer with `pong` (handled automatically by most WebSocket libraries). Connections that miss pongs are closed by the server.

### Cross-origin access

Browsers don't apply CORS to the WebSocket `Upgrade` request (no preflight, no `Access-Control-*`
headers), so the REST server's `[REST.CORS]` config (see `docs/common_config.md`) can't restrict this
endpoint the same way it restricts REST responses. Instead, the handshake itself is rejected (`403`)
for a disallowed origin, based on the same `[REST.CORS].AllowedOrigins` list. With CORS disabled (the
default), any origin can connect, same as before `[REST.CORS]` existed.



## Notes

- **The server is stateful**: calling the REST endpoint (or connecting via WebSocket) registers the tx in the list of supervised bridges, and that list lives in the instance that served the request. This has consequences when deploying behind a proxy / load balancer with more than one instance:
  - A poll may register the tx on instance A and the next poll may land on instance B, which does not know it — the client would get `bridge_status: null` again even though the bridge was already resolved elsewhere, showing an erratic behavior (`bridge_status` flickering between `null` and populated, or statuses that go "backwards" if the instances are not equally synced).
  - A WebSocket connection is tied to the instance that accepted it; on reconnect, the client may land on a different instance and be registered again from scratch.
  - Mitigations to consider: a single instance, sticky sessions (affinity by `tx_hash`) on the proxy, or a shared store for the supervised-bridges list so any instance can answer for any registered tx.
- The `instance_id` / `config_sha1` fields of the [health endpoint](#health) help diagnosing the multi-instance issues above (they identify which instance/configuration answered each request).
- **Terminal bridges are eventually forgotten**: once a bridge reaches a terminal state — `finished`, or `error` because the tracker gave up resolving the tx at all — its status stays queryable for a retention period (default 10 minutes), after which the tracker forgets it to bound memory. Polling or subscribing within that window observes the terminal `tracking_status` normally. Asking about the tx **after** it was forgotten registers it again from scratch (`tracking_status: registered`, `bridge_status: null`) and tracking restarts. This is also how a client **retries** a tx the tracker gave up on (e.g. it was not mined yet back then): wait out the retention and request it again — the new attempt either resolves normally this time or fails again after the unresolved timeout.
