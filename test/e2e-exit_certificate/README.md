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
| `52-generate_delta_certificate.sh [N]` | Delta mode: generate, submit and verify delta certificate N, chained on the previous run. |
| `60-claim_exit_certificate_funds.sh` | Start the claimer and claim every exit on L1. |

## Scenarios

```bash
./test/e2e-exit_certificate/run_e2e_test.sh           # regular exit certificate
./test/e2e-exit_certificate/run_e2e_test.sh --delta   # delta mode
```

`--delta` (CI job *Exit-certificate tool E2E (delta mode)*):

1. `20` locks ~3 ETH in an L2 contract that cannot move it (runtime code `STOP`: it accepts ETH, nothing can spend it).
2. `40` generates the first certificate with `skipSCLockedValue`, so the locked ETH stays on the L2 (and its collateral stays in the agglayer). `42`/`43` are skipped: they assume the regular certificate.
3. `50` settles the first certificate.
4. `52 1` recovers half of the locked ETH with a delta certificate and `52 2` the other half, chained on the first delta. Each one is submitted, must settle, and is checked: height, `prev_local_exit_root`, only the new exit in the delta certificate, every exit in `exit-certificate-full.json`, manifest.
5. `60` runs the claimer with the **last delta's** config (its `exit-certificate-full.json`) and claims on L1 every exit — the first certificate's (EOAs) and the deltas' (exitAddress) — against the latest root.

Not covered: claiming some first-certificate exits *before* the delta settles and the rest after (the
index stays the same, so it should not matter for `claimAsset`, but it is not exercised here).
