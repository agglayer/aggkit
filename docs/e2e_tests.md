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
| `anvil-2chains` / `default` | Everything else (positive-regex list, remove-GER tests excluded) |

## Two L2 networks

It involves two L2 networks (and single L1 network), that are attached to the same agglayer.

### Test L2 to L2 bridge

It bridges native tokens from L1 to both L2 networks and claims them. Afterwards, it bridges from L2 (PP2) to L2 (PP1) network and claims it on the destination network.

### Bridge loop tester full cycle

Drives [`tools/bridge_loop_tester`](../tools/bridge_loop_tester/README.md)'s library API through one full circular
cycle of the ring `0 -> 1 -> 2 -> 0` on the `anvil-2chains` env, so a single cycle covers all three bridge
directions (L1->L2, L2->L2, L2->L1). Implemented in `test/e2e/bridgeloop_test.go`:

```bash
go test -v -run TestBridgeLoopFullCycle -timeout 60m ./test/e2e
```

The test builds the tool's `Config` programmatically from the loaded env (network IDs, RPC URLs, bridge addresses
and pre-funded keys all come from `envs.LoadEnv`/`summary.json`, nothing is hardcoded) and runs two loops
concurrently over the same ring: one moving ETH and one moving an ERC20 the tool deploys and mints itself on L1.
Both loops share the orchestrator's own client pool — one `NetworkClient` per (network, signing key) pair — so the
run also exercises the per-instance nonce serialization concurrent loops on one account depend on.

The ring mixes both claim modes, and the topology is what makes that meaningful:

| Hop | Direction | `Claim` | Who claims |
| --- | --- | --- | --- |
| `0 -> 1` | L1 -> L2A | `manual` | the tool, after asserting nothing else claimed it for the whole grace period |
| `1 -> 2` | L2A -> L2B | `auto` | the network-2 Auto Claim service, enabled for this test only |
| `2 -> 0` | L2B -> L1 | `manual` | the tool, again after the negative assertion |

Auto Claim is enabled on the `aggkit-002` node alone (L2ToLx detector plus a single network-2 claimer, using the
same harness machinery as `TestAutoClaimL2ToL2AllowAll`), so no claimer exists for the two manual hops'
destinations. The assertions are made on the `Report` the tool returns — per-hop readiness gates, the injected leaf
index actually used for the claim proof, who claimed each hop, exact ERC20 and tolerant native balance deltas,
per-phase timings, ring closure (the ERC20 balance on L1 is back to exactly its pre-cycle value, and the L1 native
balance is back to its pre-cycle value minus gas), and the persisted state file (one completed cycle per loop, the
resume cursor back at hop 0, nothing left in flight). Finally it feeds the last hop's own `HopCheckpoint` back into
a hop engine as `HopRequest.Resume`, so the checkpoint the run produced is proved to round-trip through the resume
path; the resume states that re-drive real on-chain work are covered by the hop engine's unit tests instead, since
re-driving them against a closed ring would move value the ring no longer has.

Because the two-chain Anvil env runs no batcher or proposer, neither L2's finalized head advances on its own and an
aggsender only certifies up to `min(lastBridgeSyncBlock, lastClaimSyncBlock)`. A hop whose source is an L2
therefore needs an unrelated claim to land on that L2 *after* its bridge, or the bridge's local exit root never
settles to L1. The test drives that background L1->L2 bridge-and-claim activity on both L2s with its own keys, the
same environment precondition `TestAutoClaimL2ToL1AllowAll` and `TestAutoClaimL2ToL2AllowAll` already establish.

#### CI matrix

`.github/workflows/test-go-e2e.yml` runs this test in its own `anvil-2chains` / `bridge-loop` matrix group, so it
gets an isolated compose stack: it restarts `aggkit-002` to enable Auto Claim, exactly the kind of state mutation
the `autoclaim` group is separated for.

## Post-test bridge health-check

After every Go e2e suite run that passed, `TestMain` moves value once around a **closed bridge ring** as a
network-health probe, and `log.Fatalf`s if the value does not come home — deliberately leaving the env standing so
the failure can be debugged against the live network. Set `E2E_SKIP_POSTTEST_BRIDGE_CHECK=true` to opt out (the
`RUN_FORCE_GER_UPDATE_E2E=true` job does, since GER-manipulating tests legitimately leave this signal unhealthy).

The check is driven through `tools/bridge_loop_tester`'s library API, so the tool the repo ships is the probe the
repo uses. The ring is derived from the loaded env's topology:

| Env shape | Ring | Directions covered |
| --- | --- | --- |
| `env.L2B == nil` | `0 -> 1 -> 0` | L1->L2, L2->L1 |
| `env.L2B != nil` | `0 -> 1 -> 2 -> 0` | L1->L2, L2->L2, L2->L1 |

Networks, chain IDs, RPC URLs, bridge addresses and signing keys all come off the loaded env; nothing is
hardcoded. The loop is deliberately **ETH-only and one cycle**, with one hop attempt: an ERC20 loop would need a
token deploy and a mint on every suite run, and a retry would double the worst case of something that runs after
every suite. `TestBridgeLoopFullCycle` (above) is where the ERC20 ring, the `auto` claim mode and the full
assertion surface live.

Every hop uses `Claim = "manual"`, i.e. the tool submits every claim itself. Whether an autoclaim service is
running on a given aggkit node here depends on which tests just ran (`autoclaim_test.go` enables autoclaim and
restores the node's config on cleanup), so an `auto` hop would fail whenever the suite left autoclaim off — a
property of the preceding test, not of the network's health.

Envs that run no `aggkit-proxy` (`op-pp`) fall back to the hand-rolled parallel `BridgeL1ToL2` / `BridgeL2ToL1`
check: the tool observes everything through the proxy's `/bridge/v1` + `/tracker/v1` surface, and the
`/tracker/v1` half exists only in the `aggkit-proxy` binary.
