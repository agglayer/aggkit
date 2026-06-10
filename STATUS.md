# E2E Handoff Status

## Current State

The e2e work is partially fixed but not complete. The focused remove-GER and autoclaim failures from
`FIX_TEST.md` were addressed, and docker compose health checks were made less flaky. The full e2e suite
still fails on the backward/forward LET tests.

Current modified files:

- `test/e2e/envs/op-pp/docker-compose.yml`
- `test/e2e/testmain_test.go`
- `tools/remove_ger/diagnosis.go`
- `tools/remove_ger/diagnosis_test.go`

Untracked user-provided file:

- `FIX_TEST.md`

## Done

### Remove-GER classification fix

`tools/remove_ger/diagnosis.go` was changed so that a claim with matching bridge content at the same
`deposit_count`, but different L1 info roots, is classified as `ScenarioCategoryB1` before searching for
same-content bridges at other deposit counts.

This fixes the B.1 vs B.2 ordering issue described in `FIX_TEST.md`.

### Remove-GER unit coverage

`tools/remove_ger/diagnosis_test.go` now has focused classifier tests:

- `TestClassifyClaim_SameIndexMatchingContentWrongRootsIsCategoryB1`
- `TestClassifyClaim_ContentAtDifferentIndexIsCategoryB2`

These use an `httptest` bridge-service stub and cover the key B.1/B.2 distinction.

### Post-test health skip fix

`test/e2e/testmain_test.go` was changed so that when `l1HeadAdvances(env)` is false after tests pass,
the post-test bridge-flow health check is skipped without changing the process exit code to failure.

This avoids failing otherwise-passing focused tests only because the environment is no longer producing
new L1 blocks at teardown time.

### Docker compose health fix

`test/e2e/envs/op-pp/docker-compose.yml` health windows were increased for the services that were timing
out during e2e startup and aggkit restarts:

- `op-geth-001`: `retries: 60`, `start_period: 90s`
- `op-node-001`: `retries: 90`, `start_period: 180s`

The L1 `geth` healthcheck should remain at its original values.

## Validation Already Run

These passed during the investigation:

```bash
go test -c -o /tmp/aggkit-e2e.test ./test/e2e
go test -v ./tools/remove_ger
go test -v -run TestRemoveGER_CategoryB1 -timeout 25m ./test/e2e
go test -v -run TestRemoveGER_CategoryB2 -timeout 30m ./test/e2e
go test -v -run 'TestAutoClaimL1ToL2(AllowAll|APIApprove)' -timeout 30m ./test/e2e
```

`git diff --check` passed before the final compose healthcheck adjustment. Rerun it before handing off or
committing.

## Still Failing

The full e2e suite was run with:

```bash
docker compose -f test/e2e/envs/op-pp/docker-compose.yml down -v --remove-orphans
make test-e2e 2>&1 | tee /tmp/aggkit-full-e2e-final.log
```

It failed in `TestBackwardForwardLET_Case1`.

Observed failure:

- `TestAutoClaimL1ToL2AllowAll` passed.
- `TestAutoClaimL1ToL2APIApprove` passed.
- `TestBackwardForwardLET_Case1` submitted a malicious cert at height `0`.
- `waitForCertificateToSettle` saw:
  - `pendingH=0`
  - status moved from `Candidate` to `InError`
  - `settledH=nil`, `settledLER=nil`, `settledDC=nil`
- It timed out after 5 minutes waiting for certificate height `0` to settle.

The later failures looked cascading:

- `TestBackwardForwardLET_Case2` failed trying to replace the pending/error certificate.
- `TestBackwardForwardLET_Case3`, `Case4`, and `AggsenderAPIFallback` failed because the bridge service
  was not healthy or the environment was left in a bad state after Case1/Case2.
- `TestRemoveGER_CategoryB1` and `TestRemoveGER_CategoryB2` then failed waiting for bridge tx mining in
  the contaminated environment, even though both passed alone on clean stacks.
- `TestGenerateInvalidGER` passed in that same full run.

The root remaining blocker is therefore the BFL Case1 certificate going `InError`, not remove-GER.

## How To Proceed

Start clean:

```bash
docker compose -f test/e2e/envs/op-pp/docker-compose.yml down -v --remove-orphans
docker ps -a --format '{{.Names}}\t{{.Status}}' | rg 'cdk-20260216' || true
go test -c -o /tmp/aggkit-e2e.test ./test/e2e
```

Reconfirm the non-BFL fixes:

```bash
go test -v ./tools/remove_ger
go test -v -run TestRemoveGER_CategoryB1 -timeout 25m ./test/e2e
go test -v -run TestRemoveGER_CategoryB2 -timeout 30m ./test/e2e
go test -v -run 'TestAutoClaimL1ToL2(AllowAll|APIApprove)' -timeout 30m ./test/e2e
```

Then isolate BFL:

```bash
docker compose -f test/e2e/envs/op-pp/docker-compose.yml down -v --remove-orphans
go test -v -run TestBackwardForwardLET_Case1 -timeout 25m ./test/e2e 2>&1 | tee /tmp/aggkit-bfl-case1.log
```

Inspect:

```bash
rg -n 'Case1|sendMaliciousCertificateViaTool|RunSendCert|waitForCertificateToSettle|pendingStatus|InError|error|certificate' /tmp/aggkit-bfl-case1.log
docker logs cdk-20260216-212314-agglayer 2>&1 | tail -n 300
docker logs cdk-20260216-212314-aggkit-001 2>&1 | tail -n 300
```

Likely next places to debug:

- `test/e2e/backwardforwardlet_test.go`
  - `sendMaliciousCertificateViaTool`
  - `buildMaliciousCert`
  - `waitForCertificateToSettle`
- `tools/backward_forward_let/send_cert.go`
- Agglayer logs for why the submitted certificate becomes `InError`

Do not spend more time on remove-GER until BFL Case1 passes in isolation. The remove-GER focused tests
already passed on clean stacks after the classifier fix.

## Final Acceptance

Before considering the work done, run:

```bash
go test -v ./tools/remove_ger
git diff --check
docker compose -f test/e2e/envs/op-pp/docker-compose.yml down -v --remove-orphans
make test-e2e 2>&1 | tee /tmp/aggkit-full-e2e-final.log
docker compose -f test/e2e/envs/op-pp/docker-compose.yml down -v --remove-orphans
docker ps -a --format '{{.Names}}\t{{.Status}}' | rg 'cdk-20260216' || true
```

The work is complete only when `make test-e2e` passes and no `cdk-20260216` containers remain afterward.
