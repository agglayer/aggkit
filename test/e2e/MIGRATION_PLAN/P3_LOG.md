# P3 Step Log

**Step:** P3 — Migrate `bridge-e2e.bats` → core bridge happy paths

**Final outcome:** Completed. Migration code is done; verification followed the agreed
strategy — fast checks (build/vet/scoped-lint) plus isolated live runs for the non-L2→L1
legs. Authoritative full-suite verification is deferred to the new step **P10b**.

## Work done

Created `test/e2e/bridge_test_core_test.go` with a single top-level `TestBridgeCore` driving
the four legacy bats cases as Go subtests against the shared op-pp env:

- `TransferMessageL1ToL2` (ports "Transfer message")
- `ERC20DepositL1ToL2` (ports "ERC20 token deposit L1 → L2")
- `ERC20DepositL2ToL1` (ports "ERC20 token deposit L2 → L1")
- `NativeTransferL1ToL2` (ports "Native token transfer L1 → L2")

Both ERC20 L1→L2 and native L1→L2 subtests include the load-bearing **double-claim-fails**
assertions (re-issue the settled `ClaimAsset`, accept either a send-time revert or a mined
failed receipt, and assert the balance is unchanged). All bridge/claim machinery is reused
from the P1 helpers (`helpers_test.go`) and `bridge_utils.go` rather than duplicated; pooled
keys are returned in every subtest and `assertNetworkHealthy` runs after the subtests.

## Live verification results

- **TransferMessageL1ToL2 — PASS** (live-verified in isolation; run2).
- **ERC20DepositL1ToL2 — PASS** (live-verified in isolation; both runs).
- **NativeTransferL1ToL2 —** real bugs surfaced via live runs:
  - run1: recipient was the gas-payer, so the exact `+amount` assertion failed
    (`got 99719390182651 want 100000000000000`).
  - run2: after switching to gas-accounting it was off by only ~1013 wei
    (`got 99865879096247 want 99865879097260`) because this is an OP-Stack L2 (op-geth) that
    charges an **L1 data fee** the go-ethereum receipt does not surface, breaking exact gas
    accounting.
  - Fixed across **2 change cycles** to a fee-tolerant assertion (strict increase, `delta <=
    amount`, shortfall `< amount/1000`). Build/vet/scoped-lint green; **not yet
    live-reverified** (deferred to P10b).
- **ERC20DepositL2ToL1 —** code is faithful but is **NOT live-verifiable in isolation**: it
  timed out waiting for L1-Info-Tree inclusion at both **25 min** (run1) and **40 min** (run2)
  due to systemic L2→L1 settlement latency
  (`bridge not included in L1 Info Tree (L2->L1) deposit_count=0: context deadline exceeded`).
  Deferred to the **P10b** full-suite gate (the same L2→L1 path passed in a full-suite
  post-suite run).

## Deviations / decisions not explicit in the step

- (a) Bridged the message to a distinct, freshly-generated recipient (gas-free) so an exact
  `balanceAfter - balanceBefore == amount` assertion holds.
- (b) Made the native balance assertion tolerance-based to absorb OP-Stack L1 data fees.
- (c) Set the ERC20 L2→L1 context budget to 40 min.
- (d) Per user decision, per-step live verification of L2→L1 legs is deferred to the new
  **P10b** full-suite green gate.

## Change-request count

**2** — (1) balance-assertion fixes (distinct recipient + gas accounting + L2→L1 budget bump);
(2) native L1-data-fee fix (tolerance-based assertion).

## Changed files

- `test/e2e/bridge_test_core_test.go` (created).

No production or helper files were touched (`bridge_utils.go`, `helpers_test.go`,
`bridge_test.go`, env/compose, CI all unchanged).

## Commands run

- Fast checks each cycle (from `aggkit/`):
  - `go build ./test/e2e/...` → exit 0
  - `go vet ./test/e2e/...` → exit 0
  - `golangci-lint run ./test/e2e/...` → `0 issues.` exit 0
- Orchestrator live runs:
  - run1: `go test -v -run TestBridgeCore -timeout 60m ./test/e2e/...` → FAIL
    (TransferMessage, ERC20-L2→L1, Native failed)
  - run2: `go test -v -run TestBridgeCore -timeout 90m ./test/e2e/...` → FAIL only on
    ERC20-L2→L1 timeout + native ~1013-wei near-miss (since fixed)

## Blockers / notes for future steps

- **SYSTEMIC L2→L1 settlement latency** makes isolated verification of any L2→L1-claiming
  step (P4–P8) unreliable: rely on fast checks during migration and the **P10b** full-suite
  gate for authoritative verification. Do not keep enlarging timeouts.
- Reusable patterns established:
  - distinct freshly-generated recipient for exact ETH-credit assertions;
  - tolerance-based assertion for OP-Stack L1 data fees;
  - `bridgeMessage*` takes a destination address;
  - `BridgeL2ToL1` wait is context-driven — budget the caller deadline generously (>= ~20 min).
