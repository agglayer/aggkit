# End-to-end tests

This document enumerates and summarizes the e2e tests. The tests are implemented using [Bats framework](https://bats-core.readthedocs.io/en/stable/) and are assuming there is a running cluster to run them against. They are placed in the `test/bats` folder and divided into two major categories:
- the ones that involve single L2 (pessimistic proof) and L1 network. They are found in the `test/bats/pp` folder.
- the ones that involve two L2 (pessimistic proof) and single L1 network. They are found in the `test/bats/pp-multi` folder.
Reusable helper functions are placed in the `test/bats/helpers` folder and they consist of sending and claiming bridge transactions, fetching proofs, sending transactions, querying contracts etc. Most of the functions rely on the cast command from [Foundry](https://book.getfoundry.sh/cast/).

## Single L2 network

It involves single L2 network (and single L1 network), that are attached to the same agglayer.

### Transfer message

Bridges message from L1 to L2, by invoking `bridgeMessage` function on the bridge contract and then claiming once the global exit root is injected to the destination L2 network.

### Native gas token deposit to WETH

Bridges and claims native token from L1 to L2, that is mapped to the WETH token on L2.

### Test Bridge APIs workflow

Bridges the native token from L1 to L2 and then invokes the aggkit bridge service endpoints to verify they are working as expected: `bridge_getBridges`, `bridge_l1InfoTreeIndexForBridge`, `bridge_injectedInfoAfterIndex` and `bridge_claimProof`.

### Custom gas token deposit L1 -> L2

Bridges custom gas token, that pre-exists on L1 and is mapped to a native token on L2, claims it on the L2 and asserts that the native token balance has increased when settled on L2.

### Custom gas token withdrawal L2 -> L1

Bridges and claims native token on L2 network, that is pre-deployed and mapped to custom gas token on an L1 network and asserts that the gas token balance for the receiver address has increased after it got claimed on L1 network.

### ERC20 token deposit L1 -> L2

It deploys the ERC20 token on the L1 and bridges and claims it to the L2. In this process of claiming the bridge, a token representation of given ERC20 token is automatically deployed on the L2.

### Auto Claim L1 -> L2

Validates the L1 to L2 Auto Claim service with the existing e2e environment. The focused Go e2e command is:

```bash
go test -v -run 'TestAutoClaimL1ToL2(AllowAll|APIApprove)' -timeout 30m ./test/e2e
```

`TestAutoClaimL1ToL2AllowAll` enables Auto Claim with the `allow-all` policy and waits for the request to reach
`confirmed` without a manual claim. `TestAutoClaimL1ToL2APIApprove` enables the API, waits for
`manual-approval-required`, approves the request through `POST /autoclaim/v1/bridges/{id}/approve`, and then waits for
`confirmed`.

The e2e environment must be able to start the docker compose stack, which requires enough host resources. If the host
kills `docker compose up` (`signal: killed`) before the tests start, rerun the command on a host with more memory.

### Remove GER (invalid-GER recovery)

Exercises the [remove-GER runbook](./remove_ger_runbook.md) end to end against the `anvil-2chains` env: inject an invalid
GER on L2, confirm l2gersync blocks on it, run the `remove_ger` tool's recovery flow
(`freeze bridge -> removeGlobalExitRoots -> category-specific claim correction -> restore bridge`), and confirm
l2gersync recovers automatically and resumes normal processing. Implemented in `test/e2e/removeger_test.go`:

```bash
go test -v -run 'TestRemoveGER_(NoProblematicClaims|CategoryA|CategoryB1|CategoryB2)|TestGenerateInvalidGER' -timeout 60m ./test/e2e
```

- `TestRemoveGER_NoProblematicClaims` — invalid GER with no problematic claims; recovery is just
  freeze/remove/restore.
- `TestRemoveGER_CategoryA` — invalid GER used by a claim that would under-collateralize the bridge;
  recovery adds an `unsetMultipleClaims` step.
- `TestRemoveGER_CategoryB1` — invalid GER used by a claim with correct bridge content and index but a
  wrong GER; recovery adds a `forceEmitDetailedClaimEvent` step.
- `TestRemoveGER_CategoryB2` — invalid GER used by a claim with correct bridge content but a wrong index;
  recovery adds unset + set claims + force-emit steps.
- `TestGenerateInvalidGER` — exercises the `remove_ger` tool's `generate` subcommand (which crafts and
  injects a synthetic invalid GER via `cast`) as a standalone check of the generation path. This test
  drives `cast send`/`cast call` from the **host** (outside Docker) against the L2 RPC port published by
  the Anvil compose env; on a dev machine whose local foundry `cast` cannot open outbound connections to
  that Docker-published port (while the Go `ethclient` used elsewhere in the harness reaches it fine — a
  machine-local `cast` networking quirk, not an aggkit or test defect), the test detects this via a
  preflight probe and cleanly `t.Skip`s rather than failing. CI installs `cast` fresh and reaches the
  compose network normally, so the test runs in full there.

Each of the four `TestRemoveGER_*` scenarios asserts, via `GET /bridge/v1/sync-status`'s `l2_ger_info`
(see [Bridge service component](./bridge_service.md#sync-status)):

1. l2gersync is genuinely **stalled** on the invalid GER's insert block while the L2 chain head keeps
   advancing (`assertL2GERSyncStalledAt`);
2. after the recovery tool's `removeGlobalExitRoots` call, l2gersync's `last_processed_block` **catches up
   past the actual removal transaction's block** (`waitForL2GERSyncCaughtUp`, targeting the block number
   returned by `remove_ger.ExecuteRecovery`'s `RecoveryResult.RemovalBlock`, not a post-hoc chain-head
   read — an earlier iteration of this assertion targeted an overshot, post-hoc head read and could time
   out a few blocks short of a real, successful recovery);
3. l2gersync is genuinely alive afterwards, via a fresh, valid L1->L2 bridge and claim
   (`assertL2GERSyncStillAlive`).

Complementary log-based detection (`detectInvalidGERFromAggkitLogs`) is kept alongside the `/sync-status`
assertions.

#### CI matrix

`.github/workflows/test-go-e2e.yml` runs the remove-GER tests on `anvil-2chains` in three dedicated matrix groups,
so each group gets an isolated compose stack and cannot leak mutated chain state into the default group. The
`anvil-2chains / default` group's regex explicitly excludes them (Go's `-run` has no negation syntax, so the
default group is enumerated as a positive, anchored regex instead):

| Matrix group (`env` / `group`) | Tests |
| --- | --- |
| `anvil-2chains` / `removeger-fast` | `TestRemoveGER_NoProblematicClaims`, `TestRemoveGER_CategoryA`, `TestGenerateInvalidGER` |
| `anvil-2chains` / `removeger-b1` | `TestRemoveGER_CategoryB1` |
| `anvil-2chains` / `removeger-b2` | `TestRemoveGER_CategoryB2` |
| `anvil-2chains` / `finality-knobs` | `TestAggsenderIndependentL1FinalityKnobs` |
| `anvil-2chains` / `default` | Everything else (positive-regex list, remove-GER tests excluded) |

### Independent L1 finality knobs

`TestAggsenderIndependentL1FinalityKnobs` (`test/e2e/finality_knobs_test.go`) covers aggkit#1846/#1847 on
`anvil-2chains`. It restarts `aggkit-001` with `[L1InfoTreeSync] BlockFinality = "LatestBlock"` and
`[AggSender] BlockFinalityForL1InfoTree = "LatestBlock/-N"` while `[L1Multidownloader]` stays at its
`FinalizedBlock` default, bridges and claims L1 -> L2 twice inside the resulting window, and asserts on the
`aggkit-001` logs and the aggsender RPC that:

- `block finality misconfiguration` is **absent** (the aggsender accepts the asymmetric knobs and starts);
- `is not yet under the selected L1 info root` is **present** (a claim whose GER is not yet under the selected
  root trims the certificate instead of failing);
- `exists on L1 but cannot be proved against selected root` is **absent** (no hard error on that claim);
- a certificate whose `ToBlock` covers the claims reaches `Settled`
  (`aggsender_getCertificateHeaderPerHeight`).

`N` and the per-phase timeouts are derived at runtime from the L1 `latest`-to-`finalized` lag and block
time (`N = 3*lag + ceil(120s / blockTime)`), so the test does not hardcode anvil timing. The original config
is restored (and the service restarted) in `t.Cleanup`.

```bash
AGGKIT_E2E_ENV=anvil-2chains make test-e2e TEST_RUN='^TestAggsenderIndependentL1FinalityKnobs$'
```

### Bridge service health check

`TestBridgeServiceHealthSyncStatus` (`test/e2e/health_test.go`) exercises issue #1689 on `anvil-2chains`: it polls
`GET /` on `aggkit-001`'s bridge service until `sync_status` reaches `"done"`, asserting on every observed response
that the HTTP status is always `200` (even while `sync_status` is still `"pending"`) and that `sync_status` is one
of the three documented values (`"done"`/`"pending"`/`"error"`); once settled, it checks that `details` reports at
least one configured component with no error, then confirms `GET /health` serves the identical handler (same
always-`200` contract, same `sync_status`). See [Bridge service component](./bridge_service.md#health-check).

```bash
AGGKIT_E2E_ENV=anvil-2chains make test-e2e TEST_RUN='^TestBridgeServiceHealthSyncStatus$'
```

## Two L2 networks

It involves two L2 networks (and single L1 network), that are attached to the same agglayer.

### Test L2 to L2 bridge

It bridges native tokens from L1 to both L2 networks and claims them. Afterwards, it bridges from L2 (PP2) to L2 (PP1) network and claims it on the destination network.

### Auto Claim: claimer added after others

`TestAutoClaimClaimerAddedAfterOthers` (`test/e2e/autoclaim_test.go`) proves the fix for issue #1651 end to end on
the two-chain Anvil env. Auto Claim runs on `aggkit-001` with its L2-to-Lx bridge detector watching its own network
as the source. An L1-destination claimer ("network A") is present from the first restart; an L2B-destination
claimer ("network B") is added only on a **second, later** restart, after a bridge has already been sent to network
B while no claimer for it existed yet. The test asserts:

- the bridge sent to network B before its claimer existed is still discovered and claimed once the claimer is added
  (pre-fix, a per-source — not per-(source, destination) — LER cursor would have already advanced past it via
  network A's traffic, silently and permanently losing it with no backfill path); and
- network A's claiming never stalled while network B backfilled: a fresh network-A bridge sent after network B's
  claimer is added still claims within the normal wait window.

See [Auto Claim's "Adding a claimer to a running deployment" / "Legacy upgrade seed"
sections](./autoclaim.md#l2-to-lx-l2-to-l1-and-l2-to-l2) for the underlying model this test exercises, and the
test's own doc comment for why the analogous L1-to-L2 failure mode is covered by the unit suite instead of here.

```bash
go test -v -run 'TestAutoClaimClaimerAddedAfterOthers' -timeout 40m ./test/e2e
```

### Bridge tracker: non-bridge emitter check

`TestBridgeTrackerNotABridge` (`test/e2e/proxy_tracker_test.go`) exercises `BridgeEventSource`'s fail-closed
emitter-address check (issue #1751) on a multi-chain env with `aggkit-proxy`'s tracker enabled: it sends a
transaction to `test/contracts/bridgeeventimpostor` (`BridgeEventImpostor.sol`), a throwaway contract whose only
purpose is to emit a log with the exact same `topic0` as the real bridge contract's `BridgeEvent`, but from a
non-bridge address. The tracker resolves the real, canonical L1 bridge address through the bridge service finder
(`BridgeServiceFinder`/`RollupManagerAddr`), so the impostor log's address never matches it; `FindBridge` finds no
matching log and returns the permanent `ErrBridgeTxNotABridge`, which the tracker surfaces as a terminal
`tracking_status: "error"` with `bridge_status` staying `null`. See `TestBridgeTrackerL1ToL2` for the happy-path
counterpart in the same file, and [Bridge Tracker component](./bridgetracker.md#how-it-works) for the address-check
rule this exercises.

The impostor contract is compiled by `test/contracts/compile.sh` and bound into Go by `test/contracts/bind.sh`
(`gen bridgeeventimpostor`) like every other test-only contract in `test/contracts/`.

```bash
go test -v -run 'TestBridgeTrackerNotABridge' -timeout 5m ./test/e2e
```
