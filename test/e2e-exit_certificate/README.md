# exit_certificate end-to-end tests

Scripts run in order by [run_e2e_test.sh](run_e2e_test.sh) against a Kurtosis CDK network (CI:
[.github/workflows/test-e2e-exit_cenrtificate_tool.yml](../../.github/workflows/test-e2e-exit_cenrtificate_tool.yml)).

| Script | What it does |
| ------ | ------------ |
| `10-run_network.sh` | Start / stop the Kurtosis enclave. |
| `20-prepare_network.sh` | Bridges, ERC-20 scenario, a settled certificate. With `DELTA_SCENARIO=1` it also locks ETH in a smart contract on L2. |
| `30-stop_sequencer.sh` | Freeze the L2 (block producers only; the RPC keeps serving). |
| `40-generate_exit_certificate.sh` | Run the exit tool (CHECK → … → SIGN). With `SKIP_SC_LOCKED_VALUE=1` the config sets `skipSCLockedValue`. |
| `42-check_deterministic_certificate.sh`, `43-check_skip_sclocked.sh`, `45-check_exit_certificate.sh` | Determinism, `skipSCLockedValue` and ERC-20 checks. |
| `50-submit_exit_certificate.sh` | Submit the certificate and wait for L1 settlement. |
| `52-generate_delta_certificate.sh [N]` | Delta mode: apply recovery N (`--step recoveries`), then generate, submit and verify delta certificate N, chained on the previous run. |
| `60-claim_exit_certificate_funds.sh` | Start the claimer and claim every exit on L1. |

## Scenarios

```bash
./test/e2e-exit_certificate/run_e2e_test.sh           # regular exit certificate
./test/e2e-exit_certificate/run_e2e_test.sh --delta   # delta mode
```

`--delta` (CI job *Exit-certificate tool E2E (delta mode)*): a chain of two deltas, each one the **recovery of
one SC-locked contract** (the case of a [zkevm-proof-of-ownership-kit](https://github.com/agglayer/zkevm-proof-of-ownership-kit)
submission already verified by the team), applied with the `recoveries` step:

1. `20` deploys **two** L2 contracts that cannot move ETH (runtime code `STOP`) and bridges ~3 ETH to the first
   and ~2 ETH to the second. Two fresh L1 addresses are the "rightful owners" (the `destination` of each recovery).
2. `40` generates the first certificate with `skipSCLockedValue`, so both amounts stay on the L2 (and their
   collateral in the agglayer). `42`/`43` are skipped: they assume the regular certificate.
3. `50` settles the first certificate.
4. `52 1` — **delta 1**: writes recovery case 1 (a kit-style `.tgz` with `recovery.json` + `move.sh`) in
   `tmp/recoveries-cases/` and runs `--step recoveries`. It checks, against the first run's files:
   `delta-exits.json` has the single exit (amount, owner, native token); `step-c` `pendingSCLockedBalance` went down
   and `recoveredSCLockedBalance` went up by exactly that amount; `step-f-checks` moved it from
   `skippedSCLockedAmount` to `certificateAmount`; the ledger has the case; the previous run's files are
   unchanged; re-running gives identical files; and a negative case (more than what is pending) makes the step
   fail without writing anything. Then it runs the pipeline, submits and waits, and checks height,
   `prev_local_exit_root`, only the new exit in the delta certificate, every exit in
   `exit-certificate-full.json`, manifest.
5. `52 2` — **delta 2**: adds case 2 (a directory) next to case 1 and runs `--step recoveries` chained on delta 1's
   `recoveries/` state. Case 1 is already in the ledger, so only case 2's exit is issued (idempotence); the
   balances continue from delta 1's (cumulative `recoveredSCLockedBalance`); a negative case (sink #1 again) is
   refused. Same certificate checks, chained on delta 1.
6. `60` runs the claimer with the **last delta's** config (its `exit-certificate-full.json`) and claims on L1
   every exit — the first certificate's (EOAs) and both deltas' (owners) — against the latest root. Finally each
   owner must hold exactly its recovered amount on L1.

Files per delta (`tmp/`): `recoveries-cases/`, `output-kurtosis-delta-N/recoveries/` (the state of the recoveries
step: `delta-exits.json`, updated `step-c`/`step-f-checks`, ledger, report) and the usual delta outputs in
`output-kurtosis-delta-N/`.

Not covered: claiming some first-certificate exits *before* the delta settles and the rest after (the
index stays the same, so it should not matter for `claimAsset`, but it is not exercised here).
