# Auto Claim Service

The Auto Claim service automates L1 to L2 bridge claims for the configured L2 destination networks. It discovers
eligible L1 bridge exits from `l1bridgesync`, stores each request in a local database, evaluates the configured policy,
prepares the L1 claim proof, submits the destination-chain claim transaction through `EthTxManager`, and records the
request through confirmation or failure.

Auto Claim is disabled by default. The first supported scope is L1 to L2 only, where `origin_network` is `0`. L2 to Lx
Auto Claim is not implemented and must remain disabled.

## Runtime Requirements

Run Aggkit with the `autoclaim` component selected and set `[AutoClaim].Enabled = true`. Auto Claim startup requires
both `l1bridgesync` and `l1infotreesync` to be available because the L1 to L2 watchdog reads L1 bridge exits and the
claimer prepares L1 info tree proofs in-process.

When `[AutoClaim.L1ToL2Watchdog].Enabled = true`, Auto Claim also requires `l2gersync`. The watchdog must know which
Global Exit Root has actually been injected on the destination network before it queues a claim. This follows the bridge
service flow: find the L1 info tree index that includes the bridge, then use the first destination-injected GER at or
after that index as the claim proof anchor.

Auto Claim does not require the optional API to submit claims. Enable the API only when operators need request
inspection or manual approval.

## Configuration

Minimal L1 to L2 configuration:

```toml
[AutoClaim]
Enabled = true
StoragePath = "/var/lib/aggkit/autoclaim.sqlite"

[AutoClaim.API]
Enabled = true
Host = "0.0.0.0"
Port = 5579

[AutoClaim.L1ToL2Watchdog]
Enabled = true
PollInterval = "3s"
RetryAfterErrorPeriod = "1s"
MaxRetryAttemptsAfterError = -1
EtrogL1UpgradeBlock = 0

[AutoClaim.L2ToLxWatchdog]
Enabled = false

[[AutoClaim.Claimers]]
Enabled = true
ID = "l2-primary"
NetworkType = "EVM"
NetworkID = 1
URLRPC = "http://l2-rpc:8545"
BridgeAddr = "0x0000000000000000000000000000000000000000"
PolicyName = "api-approve"
GasOffset = 100000
WaitPeriod = "1s"
RetryAfter = "1s"
MaxRetries = 30

[AutoClaim.Claimers.Policy]
AllowMessageClaims = false
AllowedOrigins = [0]
AllowedTokens = []
ManualFallback = false
MaxGas = 500000

[AutoClaim.Claimers.EthTxManager]
FrequencyToMonitorTxs = "1s"
WaitTxToBeMined = "2s"
WaitReceiptMaxTime = "250ms"
WaitReceiptCheckInterval = "1s"
PrivateKeys = [
    { Method = "local", Path = "/etc/aggkit/autoclaim.keystore", Password = "change-me" },
]
ForcedGas = 0
GasPriceMarginFactor = 1
MaxGasPriceLimit = 0
StoragePath = "/var/lib/aggkit/ethtxmanager-autoclaim-l2-primary.sqlite"
ReadPendingL1Txs = false
SafeStatusL1NumberOfBlocks = 0
FinalizedStatusL1NumberOfBlocks = 0
EstimateGasMaxRetries = 1

[AutoClaim.Claimers.EthTxManager.Etherman]
URL = "http://l2-rpc:8545"
MultiGasProvider = false
L1ChainID = 2151908
HTTPHeaders = {}
```

Replace `BridgeAddr`, `NetworkID`, `URLRPC`, `L1ChainID`, storage paths, and signer settings with values for the target
L2. Use the existing `EthTxManager` configuration style for private keys; do not put secrets in logs or checked-in
configuration.

### Top-Level Keys

| Key | Default | Required when enabled | Description |
| --- | --- | --- | --- |
| `AutoClaim.Enabled` | `false` | Yes | Enables Auto Claim runtime startup. The `autoclaim` component must also be selected. |
| `AutoClaim.StoragePath` | `{{PathRWData}}/autoclaim.sqlite` | Yes | SQLite database for requests, cursors, decisions, proofs, and transaction attempts. |
| `AutoClaim.API.Enabled` | `false` | No | Starts the optional REST API for inspection and manual decisions. |
| `AutoClaim.API.Host` | `0.0.0.0` | When API enabled | API listen host. |
| `AutoClaim.API.Port` | `5579` | When API enabled | API listen port. |
| `AutoClaim.L1ToL2Watchdog.Enabled` | `true` | No | Enables L1 bridge discovery for configured L2 claimers. |
| `AutoClaim.L1ToL2Watchdog.PollInterval` | `3s` | Yes | How often the watchdog polls `l1bridgesync`. |
| `AutoClaim.L1ToL2Watchdog.RetryAfterErrorPeriod` | `1s` | Yes | Reserved retry delay for watchdog errors. |
| `AutoClaim.L1ToL2Watchdog.MaxRetryAttemptsAfterError` | `-1` | No | Reserved retry limit. `-1` means unlimited. |
| `AutoClaim.L1ToL2Watchdog.EtrogL1UpgradeBlock` | `0` | No | L1 block where Etrog global-index encoding becomes active for legacy zkEVM destination network `1`; `0` treats bridges as post-Etrog. |
| `AutoClaim.L2ToLxWatchdog.Enabled` | `false` | Must stay `false` | Reserved for future L2 to Lx support. This direction is not implemented. |

### Claimer Keys

Each enabled `[[AutoClaim.Claimers]]` entry owns one destination network.

| Key | Required | Description |
| --- | --- | --- |
| `Enabled` | Yes | Disabled claimers are ignored. |
| `ID` | Yes | Unique operator-readable claimer ID. Duplicate enabled IDs are rejected. |
| `NetworkType` | Yes | Must be `EVM`. |
| `NetworkID` | Yes | Destination network ID. Duplicate enabled network IDs are rejected. |
| `URLRPC` | Yes | Destination-chain JSON-RPC URL used for claim state checks and transaction submission. |
| `BridgeAddr` | Yes | Destination bridge contract address. |
| `PolicyName` | Yes | One of `allow-all`, `api-approve`, `no-message`, or `basic-filter`. |
| `Policy` | Policy-dependent | Static policy configuration. |
| `GasOffset` | No | Extra gas passed to `EthTxManager.Add` for claim transactions. |
| `WaitPeriod` | Yes | Claimer poll period and transaction-result polling interval. Must be greater than zero. |
| `RetryAfter` | No | Retry delay after a failed claim attempt. Defaults to `WaitPeriod` when omitted or zero. |
| `MaxRetries` | No | Maximum claim submission attempts before the request is marked failed. `0` preserves immediate-failure behavior. |
| `EthTxManager` | Yes | Independent transaction-manager configuration and storage path for this claimer. |

## Policies

| Policy | Behavior |
| --- | --- |
| `allow-all` | Approves every eligible L1 to L2 request automatically. |
| `api-approve` | Stores the request as `manual-approval-required`; an operator must approve or reject through the API. |
| `no-message` | Rejects message bridge leaves and approves asset bridge leaves. |
| `basic-filter` | Uses target-chain simulation when available. It rejects claims whose simulated gas exceeds `MaxGas`, rejects detected nested bridge calls, approves when checks pass, and falls back to manual review when simulation or nested-call inspection is unavailable. |

`Policy.AllowMessageClaims`, `Policy.AllowedOrigins`, `Policy.AllowedTokens`, `Policy.ManualFallback`, and
`Policy.MaxGas` are policy configuration inputs. The current runtime wires the named policies directly; operators
should verify policy-specific behavior before relying on filters beyond the table above.

## Request Lifecycle

Auto Claim stores requests using `origin_network + destination_network + deposit_count` as the unique key. For the
current L1 to L2 scope, `origin_network` is always `0`.

The normal lifecycle is:

1. The L1 to L2 watchdog polls `l1bridgesync` and filters bridge exits with `origin_network = 0`.
2. For each bridge that matches an enabled destination claimer, the watchdog finds the L1 info tree index where the L1
   bridge is included.
3. The watchdog queries destination GER sync for the first injected GER with `l1_info_tree_index` greater than or equal
   to the bridge inclusion index. If none exists yet, the watchdog does not enqueue the bridge and does not advance its
   cursor; it retries the same bridge window later.
4. Once the destination-injected GER is available, the watchdog stores its L1 info tree index on the request and passes
   the bridge to the matching claimer.
5. The matching enabled claimer stores the request as `detected`.
6. The claimer evaluates the configured policy and moves the request to `policy-approved`, `policy-rejected`, or
   `manual-approval-required`.
7. Approved requests move to `queued` and then `sending`.
8. The claimer prepares proofs from `l1infotreesync` and the L1 bridge sync data using the watchdog-selected L1 info
   tree index. If proof data is not ready, the request returns to `queued` and is retried later.
9. The sender checks whether the target bridge already marks the global index as claimed. If it is already claimed,
   the request becomes `confirmed` without submitting a duplicate transaction.
10. The sender packs `claimAsset` for asset leaves or `claimMessage` for message leaves, submits the transaction through
   `EthTxManager`, and records each transaction attempt.
11. Transaction-manager statuses `Created` and `Sent` keep the request in flight, while `Mined`, `Safe`, or `Finalized`
   mark it `confirmed`. `Failed` and `Evicted` move the request back to `queued` while retry budget remains, otherwise
   the request becomes `failed`.

Terminal statuses are `policy-rejected`, `confirmed`, and `failed`.

## API

The optional API uses the `/autoclaim/v1` prefix and is independent from the bridge service `/bridge/v1` prefix.

| Method and path | Purpose |
| --- | --- |
| `GET /autoclaim/v1/bridges` | List tracked requests. |
| `GET /autoclaim/v1/bridges/{id}` | Inspect one request by Auto Claim request ID. |
| `POST /autoclaim/v1/bridges/{id}/approve` | Approve a request currently in `manual-approval-required`. |
| `POST /autoclaim/v1/bridges/{id}/reject` | Reject a request currently in `manual-approval-required`. |

List filters:

- `origin_network`
- `destination_network`
- `status`
- `policy_status` or `policy_result`
- `bridge_tx_hash`
- `claim_tx_hash`
- `from_block`
- `to_block`
- `page_number`
- `page_size`

Supported request statuses are `detected`, `policy-approved`, `policy-rejected`, `manual-approval-required`, `queued`,
`sending`, `sent`, `confirmed`, and `failed`. Supported policy statuses are `approved`, `rejected`, and `manual`.

Manual approval and rejection bodies are optional JSON objects:

```json
{
  "reason": "approved by operator",
  "metadata": {
    "ticket": "OPS-123"
  },
  "decider": "operator",
  "decider_id": "alice"
}
```

The API returns request fields including `id`, `status`, bridge identifiers, `global_index`, `bridge_tx_hash`,
`claim_tx_hash`, `tx_manager_id`, `l1_info_tree_index`, retry counters, policy decision metadata, manual decision
metadata, timestamps, and `last_error`.

Example workflow for `api-approve`:

```bash
curl "http://localhost:5579/autoclaim/v1/bridges?status=manual-approval-required"
curl "http://localhost:5579/autoclaim/v1/bridges/0:1:42"
curl -X POST "http://localhost:5579/autoclaim/v1/bridges/0:1:42/approve" \
  -H "Content-Type: application/json" \
  -d '{"reason":"approved after bridge review","decider":"operator","decider_id":"alice"}'
```

Approving or rejecting any status other than `manual-approval-required` returns a conflict response.

## Operational Notes

- Disable Auto Claim by setting `[AutoClaim].Enabled = false` or by not selecting the `autoclaim` component.
- Disable the API independently with `[AutoClaim.API].Enabled = false`; automatic claiming continues for non-manual
  policies.
- Use separate `StoragePath` values for Auto Claim storage and each claimer's `EthTxManager.StoragePath`.
- The watchdog uses a durable cursor and overlap-safe polling. Duplicate bridge exits are deduplicated by
  `origin_network + destination_network + deposit_count`.
- The L1 to L2 watchdog intentionally holds its cursor when a matched bridge is not yet backed by a destination-injected
  GER. This can re-read the same bridge window on the next poll; enqueue is idempotent, and holding the cursor prevents
  skipping a claim that becomes ready later.
- The sender does not independently poll the target GER map before submitting. GER readiness is established by the
  watchdog through `l2gersync`, and the proof is built from that injected L1 info tree leaf.
- Auto Claim logs startup, API startup, watchdog polling errors, claimer recovery errors, and per-request errors through
  the existing Aggkit logger. Request-level error details are also stored in `last_error` and exposed by the API.
- Failed or evicted transaction-manager results are retried while retry budget remains. Exhausted requests become
  `failed` and require operator investigation.
- Use `api-approve` when an operator must explicitly inspect each request before claim submission. Expose the API only
  on trusted networks or behind access controls; it can approve or reject pending manual requests.
- `basic-filter` is conservative when target simulation is unavailable and returns manual review instead of automatic
  approval.

## Validation

Run the standard validation before merging Auto Claim changes:

```bash
make build
make lint
make test-unit
go test -v -run 'TestAutoClaimL1ToL2(AllowAll|APIApprove)' -timeout 30m ./test/e2e
```

The e2e command requires the existing dockerized e2e environment. If the host kills `docker compose up` before tests
start, record the command and the `signal: killed` evidence as an environment blocker rather than a test failure.

## Execution Status

- P1-P13 status: completed.
- P14 status: blocked.
- Current GER-anchor follow-up: implementation complete, focused unit tests and Docker image build pass, focused e2e is
  blocked before Auto Claim claim submission by bridge-service L1 info tree readiness.

### P14 Blocked rationale

P14 exhausted the maximum three validation change requests. `make build` passed and `make test-unit` passed on a clean
rerun, but `make lint` still fails with non-external source/test lint findings, and the focused Auto Claim e2e command
fails after the stack starts because the allow-all request reaches `failed` and the API approval path returns HTTP 500.
See `docs/autoclaim/P14_LOG.md` for the full validation log and commands.

### Current follow-up status

The latest follow-up changed Auto Claim to anchor L1 to L2 claims on destination-injected GERs, matching the bridge
service flow:

1. Determine the L1 info tree index where the L1 bridge is included.
2. Query destination `l2gersync` for the first injected GER at or after that index.
3. Store that injected GER's L1 info tree index on the Auto Claim request.
4. Build the claim proof from that selected L1 info tree leaf.

Validation completed in this follow-up:

```bash
go test -v ./autoclaim/proof ./autoclaim/watchdog ./autoclaim/claimer ./autoclaim/sender ./autoclaim/runtime ./autoclaim/types ./autoclaim/config ./config ./cmd
docker build -t aggkit:local .
```

Both commands passed on branch `feat/autoclaim-plan` at commit `2a11a0e0`.

Focused e2e status:

```bash
go test -v -run 'TestAutoClaimL1ToL2(AllowAll|APIApprove)' -timeout 30m ./test/e2e
go test -v -run 'TestAutoClaimL1ToL2AllowAll' -timeout 20m ./test/e2e
```

Both e2e attempts failed before exercising claim submission. In the combined run, `TestAutoClaimL1ToL2AllowAll` timed
out because bridge-service never returned an L1 info tree index for the bridge within 60 attempts:
`bridge not included in L1 Info Tree`. `TestAutoClaimL1ToL2APIApprove` then failed waiting for the bridge transaction
to be mined. In the single allow-all rerun, the same L1 info tree inclusion timeout repeated.

Live aggkit logs during the rerun showed `l1infotreesync` repeatedly starting from L1 block `384` with no L1 info tree
logs available yet, while bridge-service continued returning "this bridge has not been included on the L1 Info Tree
yet" for `network_id=0&deposit_count=1`. This is upstream of Auto Claim enqueueing and distinct from the earlier
`GlobalExitRootInvalid` claim submission failure.
