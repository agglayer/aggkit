# REST API

aggkit serves its component APIs over HTTP on two Gin servers, configured by the
`[PublicREST]` and `[AdminREST]` sections:

| Server | Default address | What it serves |
| --- | --- | --- |
| `PublicREST` | `0.0.0.0:5577` | Read-only queries: sync statuses, certificates, claims, L1 info tree lookups, the bridge service and the health check |
| `AdminREST` | `127.0.0.1:5579` | Operator actions: forcing a certificate trigger, generating an aggchain proof, and developer-only helpers |

Response bodies keep the JSON shapes the components have always returned. Errors are a JSON object
`{"error": "..."}` with `400` for invalid input or a limit exceeded, `404` when a query matched nothing
and `500` for any other failure.

The endpoints and their configuration replace the former JSON-RPC server. There is no JSON-RPC
listener any more: port `5576` is not opened and an `[RPC]` section in the config is ignored (aggkit
logs a warning saying so).

## Endpoints

### PublicREST

| Endpoint | Description |
| --- | --- |
| `GET /` | Health check |
| `GET /health` | Health check (same handler as `/`) |
| `GET /aggsender/v1/status` | Aggsender status, including its mode |
| `GET /aggsender/v1/certificate-header[?height=h]` | Certificate header at height `h`; without `height`, the last sent certificate |
| `GET /aggsender/v1/certificate-bridge-exits[?height=h]` | Bridge exits of the certificate at height `h`; without `height`, of the last sent certificate |
| `GET /l1claimsync/v1/status` | L1 claim sync status |
| `GET /l2claimsync/v1/status` | L2 claim sync status |
| `GET /l1claimsync/v1/claims?from_block=&to_block=` | L1 claims indexed in the block range, both ends included |
| `GET /l2claimsync/v1/claims?from_block=&to_block=` | L2 claims indexed in the block range, both ends included |
| `GET /l1claimsync/v1/claims-by-global-index?global_index=` | L1 claims of a global index |
| `GET /l2claimsync/v1/claims-by-global-index?global_index=` | L2 claims of a global index |
| `GET /l1infotreesync/v1/status` | L1 info tree sync status |
| `GET /l1infotreesync/v1/info-by-global-exit-root[?global_exit_root=]` | Leaf of a global exit root; without the parameter, the latest leaf |
| `GET /l1infotreesync/v1/info-by-root[?root=]` | Leaf of an L1 info tree root; without the parameter, the latest leaf |
| `GET /multidownloader/v1/l1/status` | L1 multidownloader status |

The bridge service also serves its routes on `PublicREST` under `/bridge/v1` (see
[the bridge service documentation](./bridge_service.md)).

Parameters:

- `height` is an unsigned integer. An invalid value is a `400`.
- `from_block` and `to_block` are required unsigned integers; `to_block` must not be lower than
  `from_block`. They are subject to the limits described below.
- `global_index` is a decimal integer (it may exceed 64 bits). An invalid value is a `400`; no claim
  for it is a `404`.
- `global_exit_root` and `root` are 32-byte hex values, with or without the `0x` prefix. A malformed
  value is a `400`; an unknown value is a `404`.

Examples:

```bash
curl -sS localhost:5577/aggsender/v1/status | jq .
curl -sS 'localhost:5577/aggsender/v1/certificate-header?height=12' | jq .
curl -sS 'localhost:5577/l2claimsync/v1/claims?from_block=1000&to_block=1100' | jq .
curl -sS 'localhost:5577/l2claimsync/v1/claims-by-global-index?global_index=123456789' | jq .
curl -sS 'localhost:5577/l1infotreesync/v1/info-by-root?root=0x…' | jq .
```

### AdminREST

| Endpoint | Description |
| --- | --- |
| `POST /aggsender/v1/trigger-certificate` | Force a certificate trigger |
| `POST /aggchainproofgen/v1/aggchain-proof` | Generate an aggchain proof for a block range |
| `POST /multidownloader/v1/l1/reorg?block=` | Force a reorg at the given block (developer mode only) |

The aggchain proof request body is:

```json
{
  "last_proven_block": 100,
  "requested_end_block": 200
}
```

Examples:

```bash
curl -sS -X POST localhost:5579/aggsender/v1/trigger-certificate
curl -sS -X POST localhost:5579/aggchainproofgen/v1/aggchain-proof \
  -H 'Content-Type: application/json' \
  -d '{"last_proven_block":100,"requested_end_block":200}' | jq .
```

The multidownloader reorg route is registered only when the downloader runs with
`[L1Multidownloader] DeveloperMode = true`; without it the route does not exist.

## Claims limits

`GET /l1claimsync/v1/claims` and `GET /l2claimsync/v1/claims` are bounded so responses stay small and
predictable. Both limits are configured per syncer, under `[ClaimL1Sync]` and `[ClaimL2Sync]`:

| Key | Default | Description |
| --- | --- | --- |
| `APIMaxBlockRange` | `10000` | Maximum number of blocks a request may span, `to_block - from_block + 1`. `0` means the default |
| `APIMaxClaims` | `1000` | Maximum number of claims a request may return. `0` means the default |

The handler answers `400` when the requested range is wider than `APIMaxBlockRange` or when the range
holds more than `APIMaxClaims` claims; in both cases the message asks the caller to narrow the range.
No claims are truncated or silently dropped: a range that fits is returned in full.

```toml
[ClaimL1Sync]
APIMaxBlockRange = 10000
APIMaxClaims = 1000

[ClaimL2Sync]
APIMaxBlockRange = 10000
APIMaxClaims = 1000
```

## Server configuration

Both servers use the `RESTConfig` fields documented in
[common config artifacts](./common_config.md#restconfig): `Host`, `Port`, `ReadTimeout`, `WriteTimeout`
and `[CORS]`. CORS is disabled by default.

`PublicREST` starts whenever a running component registers routes on it, and serves `/` and `/health`
for every node that starts it. `AdminREST` starts only when a component registers operator routes:
the aggsender (when `[AggSender] EnableRPC = true`), the aggchain proof generation component, or the
L1 multidownloader in developer mode.

`AdminREST` binds `127.0.0.1` by default, so operator actions are only reachable from the machine that
runs aggkit. To reach it remotely, bind it to another interface explicitly:

```toml
[AdminREST]
Host = "0.0.0.0"
Port = 5579
```

Only do that on a network where the admin API is meant to be reachable, and front it with the same
access controls as any other operator interface. aggkit does not rate-limit requests in-process; use
the reverse proxy for per-client throttling.

## Migrating from the JSON-RPC API

| Former JSON-RPC method | Endpoint | Server |
| --- | --- | --- |
| `aggsender_status` | `GET /aggsender/v1/status` | PublicREST |
| `aggsender_getCertificateHeaderPerHeight` | `GET /aggsender/v1/certificate-header` | PublicREST |
| `aggsender_getCertificateBridgeExits` | `GET /aggsender/v1/certificate-bridge-exits` | PublicREST |
| `aggsender_triggerCertificate` | `POST /aggsender/v1/trigger-certificate` | AdminREST |
| `l1claimsync_status`, `l2claimsync_status` | `GET /l1claimsync/v1/status`, `GET /l2claimsync/v1/status` | PublicREST |
| `l1claimsync_getClaims`, `l2claimsync_getClaims` | `GET /l1claimsync/v1/claims?from_block=&to_block=`, `GET /l2claimsync/v1/claims?from_block=&to_block=` | PublicREST |
| `l1claimsync_getClaimsByGlobalIndex`, `l2claimsync_getClaimsByGlobalIndex` | `GET /l1claimsync/v1/claims-by-global-index?global_index=`, `GET /l2claimsync/v1/claims-by-global-index?global_index=` | PublicREST |
| `l1infotreesync_status` | `GET /l1infotreesync/v1/status` | PublicREST |
| `l1infotreesync_getInfoByGlobalExitRoot` | `GET /l1infotreesync/v1/info-by-global-exit-root?global_exit_root=` | PublicREST |
| `l1infotreesync_getInfoByRoot` | `GET /l1infotreesync/v1/info-by-root?root=` | PublicREST |
| `multidownloader-l1_status` | `GET /multidownloader/v1/l1/status` | PublicREST |
| `multidownloader-l1_reorg` | `POST /multidownloader/v1/l1/reorg?block=` | AdminREST (developer mode) |
| `aggkit_generateAggchainProof` | `POST /aggchainproofgen/v1/aggchain-proof` | AdminREST |
| The claim syncer start-block setter methods (L1 and L2) | Removed: the claim syncer's start block is set by the components that use it | — |
