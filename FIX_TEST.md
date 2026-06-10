# E2E Test Fix Plan

This plan is constrained to the autoclaim branch delta and the code already modified on this branch vs `develop`.

The current evidence points to two separate problems:

1. Base e2e config had autoclaim enabled globally. That let autoclaim race tests that intentionally create unclaimed bridges, especially remove-GER B.1/B.2. Keep base autoclaim disabled and enable it only inside autoclaim-specific tests.
2. After disabling base autoclaim, `TestRemoveGER_CategoryB1` no longer failed at `ClaimAsset`; it failed because diagnosis returned `category_b2` instead of `category_b1`. That points at remove-GER classification/test setup, not autoclaim runtime.

## Ground Rules

Only touch these areas first:

- `test/e2e/envs/op-pp/config/001/aggkit-config.toml`
- `test/e2e/autoclaim_test.go`
- `test/e2e/testmain_test.go`
- `test/e2e/removeger_test.go`
- `tools/remove_ger/diagnosis.go`
- `tools/remove_ger/*_test.go`

Do not touch these unless a focused test proves they are the root cause:

- Autoclaim runtime, sender, claimer, watchdog, or storage code.
- Bridge service, L1 info tree sync, reorg detector, downloader, or contracts.
- Docker compose topology. Keep the autoclaim component available; tests should opt in through config.
- Test timeouts as the primary fix. Timeouts can be adjusted only after the actual wait condition is understood.
- Remove-GER recovery code before diagnosis classification is correct.

## Baseline And Cleanup

Start each investigation from a clean e2e stack:

```bash
docker compose -f test/e2e/envs/op-pp/docker-compose.yml down -v
docker ps -a --format '{{.Names}}\t{{.Status}}' | rg 'cdk-20260216' || true
go test -c -o /tmp/aggkit-e2e.test ./test/e2e
```

The test binary compile must pass before spending time on docker runs.

## Step 1: Preserve Autoclaim Isolation

Verify base config stays disabled:

```bash
rg -n '^\[AutoClaim\]|^Enabled = ' test/e2e/envs/op-pp/config/001/aggkit-config.toml
```

Expected:

```toml
[AutoClaim]
Enabled = false
```

Run only autoclaim e2e tests:

```bash
go test -v -run 'TestAutoClaimL1ToL2(AllowAll|APIApprove)' -timeout 30m ./test/e2e 2>&1 | tee /tmp/aggkit-autoclaim-e2e.log
```

Expected:

- `TestAutoClaimL1ToL2AllowAll` passes.
- `TestAutoClaimL1ToL2APIApprove` passes.
- The post-test bridge check uses the manual bridge path when base config is disabled.

If this fails, fix only `test/e2e/autoclaim_test.go` or `test/e2e/testmain_test.go` first. Do not change autoclaim production code unless the focused failure shows an autoclaim runtime bug.

The deleted `test/e2e/envs/op-pp/config/001/autoclaim-allow-all.toml` should stay removed. The autoclaim tests patch the main config at runtime, so a second config file is unnecessary and can hide which config the docker stack is actually using.

## Step 2: Fix B.1 Diagnosis Before B.2

Current likely RCA:

- `TestRemoveGER_CategoryB1` creates a claim using the real bridge global index, deposit count, and bridge content, but with invalid GER roots/proofs.
- `tools/remove_ger/diagnosis.go:332` searches for bridges with the same content at other deposit counts before checking whether the same-index bridge has different roots.
- In the full run, diagnosis returned `category_b2` for B.1 after seeing same-content bridge data. If the runbook defines B.1 as "same deposit count/content, wrong roots/GER", the classifier order is wrong.

First add or adjust focused unit coverage in `tools/remove_ger/*_test.go`:

- Same `deposit_count` exists.
- Claim content matches the bridge at that `deposit_count`.
- Claim `MainnetExitRoot` or `RollupExitRoot` differs from the L1 info leaf.
- Expected diagnosis: `category_b1`.

Also keep or add a B.2 unit case:

- Claim content matches a bridge at a different `deposit_count`.
- There is no matching bridge at the claimed `deposit_count`, or content at the claimed index is different.
- Expected diagnosis: `category_b2`.

Then edit `tools/remove_ger/diagnosis.go` only if the unit test confirms the mismatch:

- In `classifyClaim`, after `GetBridgeByDepositCount` succeeds and content matches at the claimed `deposit_count`, check the L1 info leaf/root mismatch before the `GetBridgesByContent` same-content scan.
- Return `category_b1` when the same-index bridge is valid but the claim roots differ.
- Keep B.2 for wrong-index/content search cases.
- Do not change `classifyByClaimContent` unless the B.2 unit case proves it is wrong.

Run remove-GER unit tests:

```bash
go test -v ./tools/remove_ger
```

Then run only B.1 e2e:

```bash
docker compose -f test/e2e/envs/op-pp/docker-compose.yml down -v
go test -v -run TestRemoveGER_CategoryB1 -timeout 25m ./test/e2e 2>&1 | tee /tmp/aggkit-b1-e2e.log
```

Expected:

- `ClaimAsset (B.1)` succeeds.
- `detectInvalidGERFromAggkitLogs` detects the injected GER.
- `Diagnose` returns `category_b1`.
- Recovery completes and emergency state is false.

If B.1 still fails, inspect:

```bash
rg -n 'B1|classify|category_|failed to fetch l1 info tree|not found|ClaimAsset' /tmp/aggkit-b1-e2e.log
```

Do not proceed to B.2 until B.1 is deterministic.

## Step 3: Isolate B.2

Run B.2 alone on a clean stack:

```bash
docker compose -f test/e2e/envs/op-pp/docker-compose.yml down -v
go test -v -run TestRemoveGER_CategoryB2 -timeout 30m ./test/e2e 2>&1 | tee /tmp/aggkit-b2-e2e.log
```

Expected:

- Real bridge is created without normal claim.
- Fake GER is injected.
- Fake proof claim at wrong `deposit_count` succeeds.
- Bridge service indexes the claim by global index and by GER.
- `Diagnose` returns `category_b2`.
- Recovery completes.

If B.2 hangs or times out, inspect the wait condition first:

```bash
rg -n 'B2|wait|GetInjectedL1InfoLeaf|InjectedInfoAfterIndex|failed to fetch l1 info tree|not found|detect invalid GER' /tmp/aggkit-b2-e2e.log
```

Feasible B.2 hypotheses to check in order:

- Inter-test contamination: B.2 passes alone but fails after B.1. Fix cleanup in `test/e2e/removeger_test.go`, especially emergency-state cleanup and aggkit restart/DB visibility assumptions.
- Bridge-service indexing wait is wrong: the test waits for a claim before the bridge service has indexed the fake GER. Fix the test wait helper or add a targeted sync wait in `test/e2e/removeger_test.go`.
- L1 info tree injection is not visible yet: logs show `GetInjectedL1InfoLeaf` or `failed to fetch l1 info tree ... not found`. Add a test-level wait for the specific injected GER/index, not a global sleep.
- Wrong B.2 fixture: `wrongDepositCount1 := uint32(42069)` could collide with assumptions in diagnosis or service pagination. Prefer an explicit wrong index that is guaranteed absent and documented in the test.

Only touch bridge-service or L1 info tree sync code if B.2 fails alone on a clean stack and logs prove the service is returning incorrect data after the chain event is finalized/indexed.

## Step 4: Rebuild Docker Only When Needed

If the fix touches Go code that runs inside the aggkit container, rebuild the docker image before rerunning e2e:

```bash
make build-docker
```

Examples that require rebuild:

- `autoclaim/*`
- `bridgeservice/*`
- `l1infotreesync/*`
- `aggsender/*`
- runtime code used by the aggkit container

Examples that usually do not require rebuild:

- `test/e2e/*_test.go`
- `tools/remove_ger/*` when the tool is executed from host `go test`
- TOML test config changes

When unsure, rebuild. The cost is lower than debugging stale container code.

## Step 5: Focused Test Order

Use this order after edits:

```bash
go test -v ./tools/remove_ger

docker compose -f test/e2e/envs/op-pp/docker-compose.yml down -v
go test -v -run TestRemoveGER_CategoryB1 -timeout 25m ./test/e2e 2>&1 | tee /tmp/aggkit-b1-e2e.log

docker compose -f test/e2e/envs/op-pp/docker-compose.yml down -v
go test -v -run TestRemoveGER_CategoryB2 -timeout 30m ./test/e2e 2>&1 | tee /tmp/aggkit-b2-e2e.log

docker compose -f test/e2e/envs/op-pp/docker-compose.yml down -v
go test -v -run 'TestAutoClaimL1ToL2(AllowAll|APIApprove)' -timeout 30m ./test/e2e 2>&1 | tee /tmp/aggkit-autoclaim-e2e.log
```

Do not rely on `go test -run TestMain`. It is too shallow for this failure mode.

## Step 6: Full E2E At The End

Run the full suite only after the focused tests pass:

```bash
docker compose -f test/e2e/envs/op-pp/docker-compose.yml down -v
make test-e2e 2>&1 | tee /tmp/aggkit-full-e2e-final.log
```

If the full suite appears stuck, first check whether the log is still advancing:

```bash
tail -n 80 /tmp/aggkit-full-e2e-final.log
```

Only capture a SIGQUIT stack after at least 10 minutes with no meaningful progress past a test's own wait context:

```bash
pgrep -af 'test/e2e.*test|aggkit-e2e|go test'
kill -QUIT <pid>
```

Keep the stack trace in the log. It is useful for identifying the exact wait helper or RPC call that is blocked.

## Acceptance Criteria

- Base e2e config has `[AutoClaim] Enabled = false`.
- No `autoclaim-allow-all.toml` file is needed.
- Autoclaim focused e2e tests pass.
- `go test -v ./tools/remove_ger` passes.
- `TestRemoveGER_CategoryB1` passes alone on a clean stack.
- `TestRemoveGER_CategoryB2` passes alone on a clean stack.
- `make test-e2e` passes.
- No `cdk-20260216` containers remain after cleanup:

```bash
docker ps -a --format '{{.Names}}\t{{.Status}}' | rg 'cdk-20260216' || true
```
