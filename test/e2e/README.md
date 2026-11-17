# E2E

This package contains tests run against a dockerized environment, loaded from `test/e2e/envs/loader.go`. The tests follow this flow:

1. Test env is loaded by `test/e2e/testmain_test.go`
2. Some sanity checks are performed to assert that the testing env is operating as expected
3. Tests in this package are then run
4. Finally, after the actual tests are run, if they pass, a L1 -> L2 and a L2 -> L1 bridge are going to be sent to validate that the network is still operational after the tests

## Inventory

- `test/e2e/bridge_fee_estimate_test.go`: `TestBridgeFeeEstimateViaProxy` sends the three bridging mechanisms (native ETH, ERC20 lock, wrapped burn) and requires `GET /bridge/v1/bridge-fee-estimate`, answered through the aggkit-proxy, to serve each mechanism's estimate (`cost == gas_units × gas_price`, no margin) and to fail closed with 503 while a mechanism has no samples; unsupported networks and missing/invalid parameters must answer 4xx.
- `test/e2e/claim_fee_estimate_test.go`: `TestClaimFeeEstimateViaProxy` requires `GET /bridge/v1/claim-fee-estimate`, answered through the aggkit-proxy, to serve first-claim (wrapper deployment) and repeat-claim estimates with the documented shape (`cost == gas_units × gas_price`, first claim costing more gas than a repeat) and to fail closed with 503 for buckets with no samples; unsupported networks and missing/invalid parameters must answer 4xx.
- `test/e2e/removeger_test.go`: test the remove GER tool (`tools/remove_ger`)
- `test/e2e/reorg_detection_test.go`: `TestReorgDetectionSmoke` provokes reorgs on L1 and on the L2 of `aggkit-001` (which syncs at `LatestBlock`, so it serves non-final data) and requires its bridge-service to serve a bridge of a non-final block with `block_finalized=false`, to report each reorg through `GET /bridge/v1/last-reorg-event` while serving the bridge unchanged (reorg that keeps the transaction) or no longer (reorg that drops it), to stay consistent and in sync, and to switch `block_finalized` to `true` once enough blocks are mined. Anvil envs only (skipped otherwise). It pauses block production, so it runs in its own CI group (`reorg-detection`).

## Reorg helpers (anvil envs)

`test/e2e/reorg_utils.go` has the helpers to provoke and observe reorgs in the anvil-backed envs (`anvil-2chains`). They are plain package functions, usable from any e2e test.

### Driving a chain: `AnvilChain`

`NewAnvilChain(name, ethclient)` wraps the anvil node behind any client of the env (`testEnv.Clients.L1`, `testEnv.L2.Client`, `testEnv.L2B.Client`). `Supported(ctx)` tells whether the node answers the `anvil_*` RPCs, to skip a test on any other env.

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

A syncer only tracks, in the reorg detector, the blocks that contain events of the syncer plus the head it polled (every 3s). Reorging untracked blocks of a chain that keeps mining is therefore detected only by chance (measured on the snapshot env: 2 of 3 L1 reorgs of depth 3 were detected, 0 of 2 on L2-001). To get a deterministic detection, pause the chain, mine the block with the event and one more block on top, wait until the bridge-service serves the event, and only then reorg both blocks: the block with the event is then tracked under its old hash and nothing but the reorg detector looks at it again. Once the tracked hash differs the event shows up in about 2s (the reorg detector tick). `Rollback` and `Revert` (blocks dropped, none built in their place) are not detected while the chain stays shorter than the tracked block: the reorg detector cannot read that header, so it flags nothing and the bridge-service keeps serving the bridge of a block that no longer exists (measured on the snapshot env, 2026-11-17, with `anvil_rollback` and with `evm_revert`). The event is reported about 2s after mining brings the chain back up to the tracked height with a different hash, so mine after a rollback/revert. Only syncers that process non-final blocks see such reorgs: `aggkit-001` syncs its L2 at `LatestBlock` (both aggkits sync the L1 bridge at `LatestBlock`, the default), while `aggkit-002` syncs its L2 at `FinalizedBlock`, so by configuration it neither serves nor tracks non-final L2 blocks (the control case; the smoke test does not assert it).
