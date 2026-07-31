# API
The API is going to be an API REST: 
GET /tracker/v1/network/{network_id}/tx/{tx_hash}
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
- `400 Bad Request` — invalid path parameters (`network_id` not a uint32, `tx_hash` not a valid hash): the body is an [ErrorData](#errordata). Nothing is registered.

## Response types

Implemented in `aggkit/bridgetracker/types/` (`status.go`, `tracking.go`, `health.go`, `websocket.go`), except `TrackingData` itself, which lives in `aggkit/bridgetracker/api/tracking_data.go` — it's the only place that constructs it.

All the enums are serialized as numeric values, and each enum field has a companion
`<field>_string` field with its string representation. The companion fields are regular
struct fields, auto-populated from the numeric value when the structure is marshaled to JSON.

All JSON field names use `snake_case` (they are listed below exactly as they appear on the wire).

## TrackingData

Body of every REST response (always `200 OK`) and of every WebSocket `status` message: the bridge is registered in the supervised list, and `bridge_status`/`step_index`/`all_steps` carry its resolved status once the tracker has it — or `error` carries why the tracker gave up trying to resolve it at all.

| field | type | desc |
| ------|------|------|
| tracking_status | TrackingStatus (int) | 0->registered (added to the supervised list, `bridge_status`/`step_index`/`all_steps`/`error` still `nil`), 1->running (resolved, alive), 2->error (either a step reached an error on an otherwise-resolved bridge, or the tracker gave up resolving the bridge at all — see `error`), 3->finished (resolved, reached `Claimed`) |
| tracking_status_string | string | string representation of tracking_status (e.g. "running") |
| network_id | uint32 | network of the request |
| tx_hash | Hash | transaction hash of the request |
| bridge_status | *BridgeStatus | `null` while tracking_status is `registered`, and forever `null` if the tracker gives up resolving the bridge (`error` is set instead); see [BridgeStatus](#bridgestatus) |
| step_index | *int | `null` under the same conditions as `bridge_status`; from then on, the index into `all_steps` of the step that explains `tracking_status`: the step in progress when `running`, the step in error when `error`, or the last step (`Claimed`) when `finished` |
| all_steps | BridgeStepPath [] | `null` under the same conditions as `bridge_status`; from then on, all expected steps of the bridge's route — GER/LER, certificate and claim data are reported per step in each entry's `result` (see [StepResult](#stepresult)) |
| error | *ErrorStep | `null` unless the tracker gave up trying to resolve the bridge at all (e.g. the tx does not exist on the network or is not a bridge transaction); see [ErrorStep](#errorstep). Unrelated to per-step errors, which live in `all_steps[i].error` instead |

## BridgeStatus 

| field | type | desc |
| ------|------|------|
| bridge_type | BridgeType (int) | 0-> L1->L2, 1->L2->L1, 2->L2->L2 |
| bridge_type_string | string | string representation of bridge_type (e.g. "L2->L2") |
| bridge_leaf_type | BridgeLeafType (int) | 0->Asset (bridgeAsset), 1->Message (bridgeMessage) |
| bridge_leaf_type_string | string | string representation of bridge_leaf_type (e.g. "Asset") |
| block_number | uint64 | block, on the origin network, where the `BridgeEvent` (`bridgeAsset`/`bridgeMessage`) was emitted |
| log_index | uint32 | position of the `BridgeEvent` log within `block_number` |

## BridgeStepPath
| field | type | desc |
| ------|------|------|
| step | BridgeStep (int) | 0->WaitingGERUpdate, 1->WaitingLERUpdate, 2->PendingInclusion, 3->CertificatePending, 4->WaitingGERInjection, 5->WaitingClaim, 6->Claimed |
| step_string | string | string representation of step (e.g. "WaitingGERUpdate") |
| status | StepStatus (int) | 0->pending, 1->inProgress, 2->done, 3->error
| status_string | string | string representation of status (e.g. "inProgress") |
| start_date | *time.Time | can be nil
| end_date | *time.Time | can be nil
| expected_duration | *Duration | serialized as human-readable string (e.g. "5m0s")
| result | *StepResult | data produced by the step once it completes; its shape depends on `step` (see [StepResult](#stepresult)). `nil` until the step produces it, and for steps without a result |
| error | *ErrorStep | error details, only set when `status` is `error` (see [ErrorStep](#errorstep)) |

## StepResult

Carried in the `result` field of a [BridgeStepPath](#bridgesteppath). Its shape depends on the step that produced it — a JSON object for most, a bare value for PendingInclusion:

| step | result fields | desc |
| --- | --- | --- |
| WaitingGERUpdate | `ger` (Hash), `block_number` (uint64) | GER resulting from the update on L1 and the block where it was updated |
| WaitingLERUpdate | `network_id` (uint32), `ler` (Hash), `block_number` (uint64) | LER resulting from the update on the origin L2 and the block where it was updated |
| PendingInclusion | Hash | the ID of the certificate that includes the bridge, set as soon as one exists |
| CertificatePending | [CertificateData](#certificatedata) | the certificate's current data; set as soon as a certificate exists, updated as its status changes (Pending, Proven, Candidate, InError), and reflects the final settled data once `status` is `done` |
| WaitingGERInjection | `ger` (Hash) | GER injected on the destination network that covers the bridge; no block number, the injection source does not expose it |
| WaitingClaim | `claim_tx` (Hash), `block_number` (uint64) | claim transaction on the destination network and its block |
| any other step | — | no result: always `nil` |

## ErrorStep

The same structure carries two different kinds of error, depending on where it appears:

- in the `error` field of a [BridgeStepPath](#bridgesteppath), when that step's `status` is `error` (3) — a step of an otherwise-resolved bridge failed;
- in the `error` field of [TrackingData](#trackingdata) — the tracker gave up trying to resolve the bridge at all (e.g. `bridgeAsset`/`bridgeMessage` tx not found, or the tx exists but emitted no `BridgeEvent`). In that case `retry_count` counts the not-found polls before giving up.

| field | type | desc |
| ------|------|------|
| error_type | ErrorType (int) | 0->transient, 1->permanent, 2->exhausted (retries have been given up on) |
| error_type_string | string | string representation of error_type (e.g. "transient") |
| retry_count | int | number of retries attempted so far |
| description | string [] | human-readable description(s) of the error, one entry per occurrence |

## GERData
| field | type | desc |
| ------|------|------|
| network_id | uint32 | Network (0->Mainnet)
| ger  | *Hash |  Global Exit Root
| mer | *Hash | Mainnet Exit Root
| rer | *Hash | Rollup Exit Root
| ler | *Hash | Local Exit Root
| ler_type | LERType (int) | 0->NA, 1->Mainnet , 2-> Local
| ler_type_string | string | string representation of ler_type (e.g. "Mainnet")

## CertificateData
| field | type | desc |
| ------|------|------|
| certificate_id | Hash |
| status | CertificateStatus (int) | Mapped from proto in [agglayer_grpc_client.go:559](aggkit/agglayer/grpc/agglayer_grpc_client.go#L559): 0->`Pending`, 1->`Proven`, 2->`Candidate`, 3->`InError`, 4->`Settled`
| status_string | string | string representation of status (e.g. "Settled")
| error | string | Only set if the proto carries `Error.Message` (relevant for `InError` certs)
| settlement_tx_hash | *Hash |

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
- When the bridge reaches a terminal state — `Claimed` (`tracking_status_string == "finished"`), or the tracker giving up trying to resolve it at all (`tracking_status_string == "error"` with `bridge_status: null` and `error` set) — the server sends the final `status` message and closes the connection with code `1000` (normal closure). A step-level error on an otherwise-resolved bridge (`tracking_status_string == "error"` with `bridge_status` populated) is **not** terminal: the connection stays open and the engine keeps polling in case it clears.

### Errors

An `error` message ([ErrorData](#errordata)) is sent, followed by the closure of the connection with code `1008` (policy violation), only when the path parameters themselves are invalid (`code=400`) — mirrors the REST `400`. This happens before any bridge is registered, so there is no [TrackingData](#trackingdata) yet. Once a bridge is registered, giving up on it (tx not found / not a bridge transaction) is reported as a normal `status` message instead (see [Server messages](#server-messages) above), not as an `error` message.

### Keepalive

The server sends WebSocket `ping` frames periodically; the client must answer with `pong` (handled automatically by most WebSocket libraries). Connections that miss pongs are closed by the server.



## Notes

- **The server is stateful**: calling the REST endpoint (or connecting via WebSocket) registers the tx in the list of supervised bridges, and that list lives in the instance that served the request. This has consequences when deploying behind a proxy / load balancer with more than one instance:
  - A poll may register the tx on instance A and the next poll may land on instance B, which does not know it — the client would get `bridge_status: null` again even though the bridge was already resolved elsewhere, showing an erratic behavior (`bridge_status` flickering between `null` and populated, or statuses that go "backwards" if the instances are not equally synced).
  - A WebSocket connection is tied to the instance that accepted it; on reconnect, the client may land on a different instance and be registered again from scratch.
  - Mitigations to consider: a single instance, sticky sessions (affinity by `tx_hash`) on the proxy, or a shared store for the supervised-bridges list so any instance can answer for any registered tx.
- The `instance_id` / `config_sha1` fields of the [health endpoint](#health) help diagnosing the multi-instance issues above (they identify which instance/configuration answered each request).
- **Terminal bridges are eventually forgotten**: once a bridge reaches a terminal state — `finished`, or `error` because the tracker gave up resolving the tx at all — its status stays queryable for a retention period (default 10 minutes), after which the tracker forgets it to bound memory. Polling or subscribing within that window observes the terminal `tracking_status` normally. Asking about the tx **after** it was forgotten registers it again from scratch (`tracking_status: registered`, `bridge_status: null`) and tracking restarts. This is also how a client **retries** a tx the tracker gave up on (e.g. it was not mined yet back then): wait out the retention and request it again — the new attempt either resolves normally this time or fails again after the unresolved timeout.
