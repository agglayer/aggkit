# P2 Step Log

**Step:** P2 — Migrate `e2e-pp.bats` → certificate-settlement test

**Final outcome:** BLOCKED (environmental, not a test-code defect). No formal validation was reached — the step was blocked at live verification.

==== STEP P2 IS BEING MARKED AS BLOCKED ====

## Work done

`test/e2e/cert_settlement_test.go` was created and iterated to a correct state. In its final form the test:

1. Drives an **L1→L2 bridge + claim** AND an **L2→L1 bridge exit** to trigger a PP certificate, mirroring `TestMain`'s proven flow:
   - Mints the L2-native `MintableERC20` to the L2 transactor, approves the L2 bridge for that amount, then calls `BridgeL2ToL1(...)`. L2-native tokens are used specifically because they bypass the Local Balance Tree underflow check in the L2 bridge, so they can be bridged out of a fresh env.
   - Uses copies of the shared `env.L1.Transactor` / `env.L2.Transactor` so Value/nonce mutations don't leak into the shared env transactors (exactly as `TestMain` does).
   - The L2→L1 bridge exit is the essential certificate trigger: it changes the L2 local exit root. An L1→L2 bridge alone (a claim/import on L2) does NOT change the local exit root and produces no PP certificate.
2. Detects settlement via the agglayer read RPC `interop_getLatestKnownCertificateHeader` (port 4444), replicating the legacy `agglayer_certificates_monitor.sh` success condition. This is the authoritative settlement signal in this repo — NOT the aggsender SQLite `certificate_info.status==4` path (P1's `waitForSettledCertificate`), which proved unreliable in this env.

Fast checks all pass:
- `go build ./test/e2e/...` → exit 0
- `go vet ./test/e2e/...` → exit 0
- scoped `golangci-lint run ./test/e2e/...` → 0 issues (exit 0)

## Why blocked (root cause)

Across THREE live runs (~45 min total) against fresh op-pp stacks, the agglayer NEVER reported any certificate. All 23/23 `interop_getLatestKnownCertificateHeader` queries returned `{"result":null}` — not even a Pending certificate.

Diagnostics show the aggkit container (version `v0.10.0-rc1-13-gb7779927`, components `aggsender,aggoracle,bridge`) is in a permanent error loop from startup:

```
ERROR claimsync/processor.go:55  failed to insert block 0: InsertBlock 0: meddler.Insert: DB error in Exec: UNIQUE constraint failed: block.num  {"module": "L2ClaimSyncer"}
ERROR sync/evmdriver.go:339      error during processBlock (attempt N): InsertBlock 0: meddler.Insert: DB error in Exec: UNIQUE constraint failed: block.num  {"syncer": "L2ClaimSyncer"}
```

The `L2ClaimSyncer` retry attempt counter climbed monotonically (attempt 3 → 664 over the diagnostic window) and never recovered.

This is NOT stale state: the env wipes the data dir + volumes on every fresh start (`cleanAggkitDataDir` + `docker compose down -v`). Basic L1→L2 bridge+claim works (L2 produces blocks), but no PP certificate is ever submitted or settled. `BridgeL2ToL1` fails at "bridge not included in L1 Info Tree" because the GER / cert-settlement pipeline is stalled by the `L2ClaimSyncer` crash loop.

## Evidence

- Live run 1: `/tmp/follow-plan/20260529/P2/live_test.log` (initial aggsender-DB detection; FAIL after 958s)
- Live run 2: `/tmp/follow-plan/20260529/P2/live_test_fix.log` (agglayer read-RPC detection; agglayer reported no known cert for full 15 min)
- Live run 3: `/tmp/follow-plan/20260529/P2/live_test_fix2.log` (added L2→L1 bridge exit trigger; still no cert)
- Diagnostic poller: `/tmp/follow-plan/20260529/P2/diag.log`
- Key facts: `L2ClaimSyncer` attempt counter 3 → 664; 23/23 `interop_getLatestKnownCertificateHeader` queries returned `result:null`.

## Change-request count

0 — blocked before formal validation. The orchestrator iterated the test 3x based on live evidence (aggsender-DB detection → agglayer read-RPC detection → added L2→L1 bridge exit trigger), but no formal change-request/validation cycle was reached.

## Changed files

- `test/e2e/cert_settlement_test.go` — created. Left in place: it is correct; the environment is the blocker. This is the only file touched.

## Commands run

Live runs (all FAIL / exit 1, ~660–990s each):
- `go test -run TestCertificateSettlement -timeout 30m ./test/e2e/...` ×3

Fast checks (all exit 0):
- `go build ./test/e2e/...` → exit 0
- `go vet ./test/e2e/...` → exit 0
- `golangci-lint run ./test/e2e/...` → 0 issues, exit 0

## Impact on plan

ALL downstream steps P3–P14 depend on P2 and on this environment producing certificates / having a healthy GER-bridge pipeline. The entire chain is therefore BLOCKED pending resolution of the environment issue.

## What human input is needed

1. Is automatic PP certificate settlement expected to work in the op-pp env, or must tests manually send certs via the `bfl` / `RunSendCert` path used by `backwardforwardlet_test.go`?
2. Is the `L2ClaimSyncer` "InsertBlock 0: UNIQUE constraint failed: block.num" crash loop a known issue? Does it require a fixed aggkit image or a config change?
3. Should the migration pause until the env is fixed, or should P2 be re-scoped to use manual cert injection?
