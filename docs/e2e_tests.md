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

Exercises the [remove-GER runbook](./remove_ger_runbook.md) end to end against the `op-pp` env: inject an invalid
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
  the `op-pp` compose env; on a dev machine whose local foundry `cast` cannot open outbound connections to
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

`.github/workflows/test-go-e2e.yml` runs the remove-GER tests on `op-pp` in three dedicated matrix groups,
each under the 20-minute per-job budget (measured passing-path wall-clock is well under 6 minutes for all
five tests combined, run back-to-back in a single env), so the `op-pp / default` group's regex explicitly
excludes them (Go's `-run` has no negation syntax, so the default group is enumerated as a positive,
anchored regex instead):

| Matrix group (`env` / `group`) | Tests |
| --- | --- |
| `op-pp` / `removeger-fast` | `TestRemoveGER_NoProblematicClaims`, `TestRemoveGER_CategoryA`, `TestGenerateInvalidGER` |
| `op-pp` / `removeger-b1` | `TestRemoveGER_CategoryB1` |
| `op-pp` / `removeger-b2` | `TestRemoveGER_CategoryB2` |
| `op-pp` / `default` | Everything else on `op-pp` (positive-regex list, remove-GER tests excluded) |

## Two L2 networks

It involves two L2 networks (and single L1 network), that are attached to the same agglayer.

### Test L2 to L2 bridge

It bridges native tokens from L1 to both L2 networks and claims them. Afterwards, it bridges from L2 (PP2) to L2 (PP1) network and claims it on the destination network.
