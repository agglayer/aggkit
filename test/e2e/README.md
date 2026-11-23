# E2E

This package contains tests run against a dockerized environment, loaded from `test/e2e/envs/loader.go`. The tests follow this flow:

1. Test env is loaded by `test/e2e/testmain_test.go`
2. Some sanity checks are performed to assert that the testing env is operating as expected
3. Tests in this package are then run
4. Finally, after the actual tests are run, if they pass, a L1 -> L2 and a L2 -> L1 bridge are going to be sent to validate that the network is still operational after the tests

## Inventory

- `test/e2e/bridge_fee_estimate_test.go`: `TestBridgeFeeEstimateViaProxy` sends the three bridging mechanisms (native ETH, ERC20 lock, wrapped burn) and requires `GET /bridge/v1/bridge-fee-estimate`, answered through the aggkit-proxy, to serve each mechanism's estimate (`cost == gas_units × gas_price`, no margin) and to fail closed with 503 while a mechanism has no samples; unsupported networks and missing/invalid parameters must answer 4xx.
- `test/e2e/bridges_from_address_test.go`: `TestBridgesMalformedAddressFilterIsRejected` requires the bridge service of `aggkit-001` (L1 and L2 syncer) and the aggkit-proxy to answer `400` naming the parameter to a `from_address` (`GET /bridge/v1/bridges`) or an `origin_token_address` (`GET /bridge/v1/token-mappings`) that is not a hex address, instead of ignoring the filter and serving the unfiltered page, and a well formed address that matches nothing to answer an empty `200`.
- `test/e2e/claim_fee_estimate_test.go`: `TestClaimFeeEstimateViaProxy` requires `GET /bridge/v1/claim-fee-estimate`, answered through the aggkit-proxy, to serve first-claim (wrapper deployment) and repeat-claim estimates with the documented shape (`cost == gas_units × gas_price`, first claim costing more gas than a repeat) and to fail closed with 503 for buckets with no samples; unsupported networks and missing/invalid parameters must answer 4xx.
- `test/e2e/removeger_test.go`: test the remove GER tool (`tools/remove_ger`)
- `test/e2e/reorg_detection_test.go`: `TestReorgDetectionSmoke` provokes reorgs on L1 and on the L2 of `aggkit-001` (which syncs at `LatestBlock`, so it serves non-final data) and requires its bridge-service to serve a bridge of a non-final block with `block_finalized=false`, to report each reorg through `GET /bridge/v1/last-reorg-event` while serving the bridge unchanged (reorg that keeps the transaction) or no longer (reorg that drops it), to stay consistent and in sync, and to switch `block_finalized` to `true` once enough blocks are mined. Anvil envs only (skipped otherwise). It pauses block production, so it runs in its own CI group (`reorg-detection`).
- `test/e2e/proxy_reorg_*_test.go`: the reorg acceptance scenarios of the aggkit-proxy (`TestProxyReorgActivity`, `TestProxyReorgClaims`, `TestProxyReorgTracker`, `TestProxyReorgRevision`, `TestProxyReorgResilience`), see [Proxy reorg scenarios](#proxy-reorg-scenarios-anvil-envs).

## Proxy reorg scenarios (anvil envs)

The `proxy_reorg_*_test.go` files check how the aggkit-proxy reacts to real reorgs (agglayer/aggkit#1972): what `GET /tracker/v1/activity/from/{address}` and the tracker endpoints report before and after a reorg removes, moves or replaces a bridge, a claim or a GER injection that was not final yet. The contract they assert is `docs/bridgetracker/API.md`.

Every scenario goes in the same order, so a failure points at the right component: the fact under test is mined by hand on a paused chain, in a block that is not final; aggkit's bridge service must serve it and the proxy must report it as not final; the reorg is provoked and **aggkit must report it first** (`last-reorg-event`, asked directly, not through the proxy); only then the proxy is required to converge to what the chain holds. Waits poll a condition with a timeout, never a fixed time. Each scenario uses a sender created for it, so counts and revisions are exact.

| Test (CI group) | Scenarios |
| --- | --- |
| `TestProxyReorgActivity` (`proxy-reorg-activity`) | A1 bridge from network 1 reorged out; A2 same bridge re-included in another block; A3 bridge replaced by another one with the same deposit count; A4 L1 -> L2 bridge reorged out of L1; A5 bridge of network 2 seen only through the RPC fallback, reorged out; A6 final bridge, shallower reorg |
| `TestProxyReorgClaims` (`proxy-reorg-claims`) | B1 claim reorged out, claimed again, final; B2 reorg removes the claim and the GER injection; B3 final claim stays `claimed_finalized` and is not rechecked; B4 claim re-included in another block; C1 reorg removes only the GER injection |
| `TestProxyReorgTracker` (`proxy-reorg-tracker-revision`) | D1 origin transaction moves and then disappears while tracked; D2 `Claimed` goes back to `WaitingClaim` and a WebSocket subscriber is told, then the connection closes once the claim is final |
| `TestProxyReorgRevision` (`proxy-reorg-tracker-revision`) | E1 `revision` unchanged by refreshes and by a not-final -> final transition; E2 and E3 a claim on page 2 reorged out while a client paginates; E4 same `revision` on `aggkit-proxy-001` and `-002`, and after a restart |
| `TestProxyReorgResilience` (`proxy-reorg-resilience`) | F1 bridge service answers `503` during the reorg; F2 proxy with `DBPath` down during the reorg; F3 bridge service omits `block_finalized`; F4 burst of reorgs with bridges in flight |

Run one group against a fresh stack (build the image first, the env runs `aggkit:local`):

```bash
make build-docker
make test-e2e TEST_RUN='^TestProxyReorgActivity$'
```

Things to know before adding a scenario:

- The non-final window of the anvil chains is two blocks (`finalized = latest - 2`), whatever the pause. A scenario that needs two facts to be reorged together (B2: the GER injection and the claim) mines them in consecutive blocks. `captureGERInjection` mines the aggoracle's injection by hand: pause the L2 **before** sending the bridge on L1. aggkit serves an injection only once another block is mined on top of it. After a reorg removes an injection the aggoracle does not inject that GER again (agglayer/aggkit#1992), so B2 and C1 recover the bridge by forcing a later GER on L1 (`forceNewGER`); that is a workaround for #1992.
- A bridge from an L2 moves to `readyToClaim` on its own when its certificate settles, and one from L1 when the aggoracle injects its GER; both change `revision` legitimately. A scenario that asserts that `revision` does **not** change must first bring the bridge to a state nothing else can move (A6 waits for `readyToClaim` of a bridge nobody claims; E1 pauses the L2 so that no injection is mined).
- Every test starts with `holdAggsender`: the aggsender of `aggkit-001` builds certificates from the same non-final data its bridge service serves, so it would settle a certificate carrying a bridge that the scenario then reorgs out, and could never build another one (the settled local exit root is gone from the L2), which the post-test bridge check of `TestMain` reports. The helper runs `aggkit-001` with `[AggSender] MaxL2BlockNumber` at the current head and puts it back on its own config when the test ends.
- These tests never write to the config files tracked in the repo, so a killed run leaves the tree clean: a changed config is rendered in a temporary directory and the service is recreated with the compose variable that points at it (`AGGKIT_001_CONFIG`, `AGGKIT_PROXY_002_CONFIG` in `docker-compose.yml`, `Env.RecreateService`).
- `aggkit-002` syncs network 2 at `FinalizedBlock`: it neither serves nor tracks non-final blocks, so in A5 there is no reorg for aggkit to report. The test asserts that instead (the bridge service never served the bridge, `last-reorg-event` did not change) and that the proxy forgot the bridge on its own.
- The bridge service cannot be made to answer `503` or to omit `block_finalized` on demand. F1 and F3 inject both on the wire: the test runs a reverse proxy on the host (`bridgeServiceInterposer`) and points `aggkit-proxy-002` at it (it reaches the host as `host.docker.internal`, see `docker-compose.yml`). It changes a status code or removes one JSON field; every other byte is aggkit's answer. F2 uses the same instance with `DBPath` set, and reads the SQLite file out of the stopped container to check that the stale entry really was persisted.
- The env sets `ActivityPollInterval = "5s"` for both proxies (the default is 30s), so the activity scenarios wait one short refresh. The tracker tick (10s) and its `UnresolvedTimeout` (30s) are not configurable.
- Not covered: D3 (L1 reorg of the settlement transaction of an L2 -> L1 bridge, optional in the issue), and an L1 reorg that removes an L1 info tree leaf (not observable here: `L1InfoTreeSync` follows `FinalizedBlock`).

## Reorg helpers (anvil envs)

`test/e2e/reorg_utils.go` has the helpers to provoke and observe reorgs in the anvil-backed envs (`anvil-2chains`). They are plain package functions, usable from any e2e test.

### Driving a chain: `AnvilChain`

`NewAnvilChain(name, ethclient)` wraps the anvil node behind any client of the env (`testEnv.Clients.L1`, `testEnv.L2.Client`, `testEnv.L2B.Client`). `Supported(ctx)` tells whether the node answers the `anvil_*` RPCs, to skip a test on any other env: it returns `false` only when the node says the method does not exist, and an error for any other failure, so an unhealthy env fails the test instead of skipping it.

| Need | Helper | anvil RPC |
| --- | --- | --- |
| Heads (number/hash) | `Head`, `FinalizedHead`, `FinalityLag` | `eth_getBlockByNumber` (`latest`/`finalized`) |
| Stop interval mining | `PauseMining` (returns the restore func), `PauseMiningForTest` (restores in `t.Cleanup`), `MiningMode`/`SetMiningMode` | `evm_setIntervalMining`, `evm_setAutomine`, `anvil_getIntervalMining`, `anvil_getAutomine` |
| Mine N blocks | `Mine`, `MineSpaced` (blocks at least N seconds apart), `MineUntilFinalized` | `anvil_mine` |
| Reorg of N blocks | `Reorg(depth, txs...)` (empty replacement blocks plus the given raw txs), `ReorgKeepingTxs(depth, drop...)` (re-includes every tx of the replaced blocks, in the same block offset, except `drop`), `TxsInLastBlocks`, `RawTransaction` | `anvil_reorg`, `eth_getRawTransactionByHash` |
| Rollback | `Rollback(depth)` | `anvil_rollback` |
| Snapshot / revert | `Snapshot`, `Revert(id)` | `evm_snapshot`, `evm_revert` |

`anvil_reorg` keeps the head number and changes the hashes of the last `depth` blocks. The transactions of the replaced blocks are dropped unless they are re-included; the raw transaction goes as a plain hex string (an object would be read as a call request). Prefer `ReorgKeepingTxs` on chains other actors also send transactions to (agglayer on L1, the aggoracle on the L2s): an empty `Reorg` would drop theirs too.

A reorg must build blocks different from the replaced ones, and anvil builds them deterministically: the first replacement block is timestamped like the common ancestor, so replaying the same transactions in a block that was mined in the same second as its ancestor gives back the very same block (same hash, nothing happened). `Reorg` fails with `ErrReorgNoop` in that case instead of silently doing nothing. Drop a transaction, move it to another block offset, or mine the block to be replaced with `MineSpaced(ctx, 1, 2)` (the smoke test does) for a reorg that always changes the hashes.

`MineSpaced` moves the chain clock forward and nothing moves it back: block timestamps stay ahead of the wall clock (up to the spacing for every spaced block that is not reorged away; the smoke test leaves a few seconds per chain). The CI group `reorg-detection` runs the smoke test on a stack of its own; on a local run that shares the stack with other tests (`make test-e2e` without `TEST_RUN`), keep that drift in mind for tests that compare block time with wall-clock time.

### Holding data in a non-final state

In the anvil snapshot images `finalized` is `latest - 2*slots_in_an_epoch`, and they run with `--block-time 1 --slots-in-an-epoch 1`: `finalized` trails `latest` by 2 blocks, always, as a function of the head number and not of time. A block therefore stays non-final for as long as nobody mines on top of it, so a test controls finality by controlling mining, with no env change:

```go
chain := NewAnvilChain("L2", testEnv.L2.Client)
chain.PauseMiningForTest(ctx, t)  // no block is produced from here on; pending txs wait
// send the tx under test (it stays pending), then
require.NoError(t, chain.Mine(ctx, 1)) // the tx is in block B = head: not final
// ... assert what aggkit serves for non-final data ...
_, err := chain.MineUntilFinalized(ctx, B) // mines until head-lag >= B
```

While a chain is paused every other actor of the env waits with it (agglayer's L1 transactions, the aggoracle's GER injections): keep the pause short and let `PauseMiningForTest` restore it.

### Observing what aggkit reports

- `LastReorgEvent` / `WaitForReorgEvent(bs, networkID, baseline, timeout)` read `GET /bridge/v1/last-reorg-event` (`networkID` 0 is L1). Read the baseline with `LastReorgEvent` before the reorg; the wait returns the first event different from it (the endpoint answers the zero value while no reorg was ever seen). Pass the **direct** bridge-service client (`testEnv.L2.BridgeService`) to check the syncers and `testEnv.Clients.BridgeServiceProxy` to check the same through the aggkit-proxy: if the direct one reports the reorg and the proxy does not, it is a proxy problem, not a syncer one.
- `WaitForBridgeBlockFinalized`, `WaitForBridgeAbsent`, `WaitForBridgeStable`, `RequireBridgeStaysAbsent`, `WaitForBridgeSynced` and `ListServedBridges` poll the bridge-service for the bridge of a given deposit count, its `block_finalized` flag, and the syncer status.

### What aggkit detects (and what it does not)

A syncer only tracks, in the reorg detector, the blocks that contain events of the syncer plus the head it polled (every 3s). Reorging untracked blocks of a chain that keeps mining is therefore detected only by chance (measured on the snapshot env: 2 of 3 L1 reorgs of depth 3 were detected, 0 of 2 on L2-001). To get a deterministic detection, pause the chain, mine the block with the event and one more block on top, wait until the bridge-service serves the event, and only then reorg both blocks: the block with the event is then tracked under its old hash and nothing but the reorg detector looks at it again. Once the tracked hash differs the event shows up in about 2s (the reorg detector tick). `Rollback` and `Revert` (blocks dropped, none built in their place) are detected without mining: a tracked block that is above the chain head in two consecutive checks of the reorg detector is reported as a reorg from the first missing tracked block, so the event shows up in about 4s (two ticks; the second check avoids flagging a block that an RPC node lagging behind has not seen yet). This is covered by the unit tests of `reorgdetector` and has not been re-measured on the snapshot env; before #1983 nothing was flagged until mining brought the chain back up to the tracked height with a different hash. `GET /bridge/v1/last-reorg-event` returns the reorg of the last detection pass in which the bridge syncer or the claim syncer of that network has an event (if both have one in that pass, `from_block` is the lowest and `to_block` the highest of the two), not the one of another syncer that shares the reorg detector. Only syncers that process non-final blocks see such reorgs: `aggkit-001` syncs its L2 at `LatestBlock` (both aggkits sync the L1 bridge at `LatestBlock`, the default), while `aggkit-002` syncs its L2 at `FinalizedBlock`, so by configuration it neither serves nor tracks non-final L2 blocks (the control case; the smoke test does not assert it).
