# exit_certificate end-to-end tests

Scripts run in order by [run_e2e_test.sh](run_e2e_test.sh) against a Kurtosis CDK network (CI:
[.github/workflows/test-e2e-exit_cenrtificate_tool.yml](../../.github/workflows/test-e2e-exit_cenrtificate_tool.yml)).

| Script | What it does |
| ------ | ------------ |
| `10-run_network.sh` | Start / stop the Kurtosis enclave. |
| `20-prepare_network.sh` | Bridges, ERC-20 scenario, a settled certificate. With `DELTA_SCENARIO=1` it also locks ETH in two smart contracts on L2 and runs `21-prepare_circle_usdc.sh`. |
| `21-prepare_circle_usdc.sh` | Delta scenario: Circle USDC on L1 (`L1Escrow`) and USDC.e on L2 (`ZkMinterBurner`, `NativeConverter`, owned by a Safe), with holders. |
| `30-stop_sequencer.sh` | Freeze the L2 (block producers only; the RPC keeps serving). |
| `40-generate_exit_certificate.sh` | Run the exit tool (CHECK → … → SIGN). With `SKIP_SC_LOCKED_VALUE=1` the config sets `skipSCLockedValue`. |
| `42-check_deterministic_certificate.sh`, `43-check_skip_sclocked.sh`, `45-check_exit_certificate.sh` | Determinism, `skipSCLockedValue` and ERC-20 checks. |
| `47-check_circle_usdc_not_paid.sh` | Delta scenario: the first certificate reports USDC.e and does not pay it. |
| `50-submit_exit_certificate.sh` | Submit the certificate and wait for L1 settlement. |
| `51-archive_run.sh` | Delta scenario: archive a settled run (`--step upload`) in a GCS emulator (`fake-gcs-server`). |
| `52-generate_delta_certificate.sh [N]` | Delta mode: apply recovery N (`--step recoveries`), then generate, submit and verify delta certificate N (1 or 2), chained on the previous run. |
| `53-generate_circle_usdc_delta.sh` | Delta 3: the Circle USDC.e recovery (kit case in the `recovery.json` v1 format, `Message` exits), chained on delta 2. |
| `60-claim_exit_certificate_funds.sh` | Start the claimer (or use the one already running) and claim every exit on L1. |
| `61-claimer_follow.sh` | Delta scenario: run the claimer on the archive in follow mode (`start`, `wait-height`, `stop`). |
| `62-check_circle_usdc_claims.sh` | Delta scenario: claimer API v2 and L1 checks of the `Message` exits, before and after the claims. |
| `check_recoveries_scale.sh` | Not part of the e2e: `--step recoveries` on the real Circle USDC `recovery.json` (6,620 outputs). See [Scale check](#scale-check-outside-the-e2e). |

## Scenarios

```bash
./test/e2e-exit_certificate/run_e2e_test.sh           # regular exit certificate
./test/e2e-exit_certificate/run_e2e_test.sh --delta   # delta mode
```

Delta mode needs docker compose >= 2.24: [`claimer-follow.compose.yml`](claimer-follow.compose.yml) drops the
claimer's published port with `ports: !reset []`.

`--delta` (CI job *Exit-certificate tool E2E (delta mode)*): a chain of three deltas on top of the first
certificate, each applied with the `recoveries` step from a **real
[zkevm-proof-of-ownership-kit](https://github.com/agglayer/zkevm-proof-of-ownership-kit) submission**: the kit simulates
the recovery on a shadow-fork of the (frozen) L2, the required addresses sign the ownership proof, and the package
it validates is the input of the tool.

- **Deltas 1 and 2** recover ETH locked in a smart contract (`Transfer` exits), with the **legacy** `recovery.json`
  format (`locked_contract` / `destination` / `funds[]`).
- **Delta 3** recovers Circle's USDC.e with bridge **`Message` exits**, with the **version 1** format
  (`outputs[]`, one per `BridgeEvent`). See [Delta 3: Circle USDC](#delta-3-circle-usdc-message-exits).

So one run covers both formats across chained runs and one recoveries ledger.

The two formats come from two versions of the kit, so the scenario needs two checkouts (the CI job checks out
both, pinned), plus docker, `cast`, `jq` and `bc`:

| Variable | Default (next to this repo) | Kit version |
| -------- | --------------------------- | ----------- |
| `KIT_DIR` | `../zkevm-proof-of-ownership-kit` | writes the legacy format (deltas 1 and 2) |
| `KIT_V1_DIR` | `../zkevm-proof-of-ownership-kit-v1` | writes `recovery.json` version 1 (delta 3) |

Every settled run is archived with `--step upload` in a GCS emulator (`51-archive_run.sh`), and each delta reads
its previous run from there (`options.previousRun = gs://…`; delta 3 gives only the archive base and the tool
finds the run of the last settled height).

1. `20` deploys **two** `contracts/Vault.sol` on L2 (owner-only `withdraw`, owner = the deployer, the Kurtosis faucet
   key) and bridges ~3 ETH to the first and ~2 ETH to the second. Two EOAs with a known (public test) key are the
   "rightful owners" (the `destination` of each recovery): the kit requires the destination to sign. Then `21`
   deploys the Circle USDC contracts and funds the USDC.e holders (below).
2. `40` generates the first certificate with `skipSCLockedValue`, so both amounts stay on the L2 (and their
   collateral in the agglayer). `42`/`43` are skipped: they assume the regular certificate. `47` checks that
   USDC.e is reported (an ERC-20 the tool detected) and not paid: it is native to the L2, not in the LBT.
3. `50` settles the first certificate.
4. `52 1` — **delta 1**: builds recovery case 1 with the kit (`recovery.json`, a `move.sh` where the vault owner
   withdraws to the destination, signatures of both, `prepare-submission.sh` and `verify-submission.sh` against a
   fork of the L2, `--allow-test-fork` since it is not zkEVM mainnet), puts the verified `.tgz` in
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
   balances continue from delta 1's (cumulative `recoveredSCLockedBalance`); a negative case (vault #1 again) is
   refused. Same certificate checks, chained on delta 1.
6. `61 start` runs the claimer on **delta 2, read from the archive, in follow mode** (`--run-source <base>
   --run-source-height <h> --follow-interval-seconds 5`, with the message route of the Circle contracts).
7. `53` — **delta 3**, then `51` archives it and `61 wait-height` requires the **same claimer process** to move to
   it by itself.
8. `60` claims on L1 every exit — the first certificate's (EOAs), deltas 1 and 2's (owners) and delta 3's
   (`claimMessage`, each through the holder's own address) — against the latest root. Finally each owner must
   hold exactly its recovered ETH and `62 claimed` checks the USDC.

### Delta 3: Circle USDC (`Message` exits)

The end-to-end proof of the zkEVM-sunset recovery of Circle's USDC.e. USDC.e is not a token wrapped by the bridge:
`L1Escrow` locks L1 USDC and sends a bridge *message*, and `ZkMinterBurner` mints USDC.e on L2 when it is claimed.
The LBT does not track it, so the exit certificate cannot pay it. The recovery makes `ZkMinterBurner` send one
message per holder to `L1Escrow`; each becomes a `Message` exit that the holder claims on L1 with `claimMessage`,
and `L1Escrow` pays it in L1 USDC.

- **Contracts** (`21`): the same code as on mainnet, deployed from the creation bytecode in
  [`contracts/circle-usdc/`](contracts/circle-usdc/README.md) and wired as usdc-lxly's `DeployInit.s.sol` does.
  - L1: USDC (FiatToken v2.1 behind `FiatTokenProxy`) and `L1Escrow`.
  - L2: USDC.e (FiatToken v2.1), `ZkMinterBurner` and `NativeConverter` (UUPS proxies), and a Safe v1.3.0
    (`GnosisSafeL2`, 2 of 3, public test keys) that owns `ZkMinterBurner` and is proxy admin, owner and
    masterMinter of USDC.e.
  - Deposits L1 → L2 through `L1Escrow.bridgeToken`, claimed on L2 with `claimMessage`: three EOAs and one
    contract hold USDC.e, and `L1Escrow` holds the backing L1 USDC. A small `bridgeAsset` of L1 USDC creates the
    bridge-wrapped USDC `NativeConverter` is initialized with (a regular LBT token, paid by the first certificate).
- **Kit case** (`53`, the mechanism of the real recovery package): `move.sh` is
  [`contracts/circle-usdc/move.sh.tpl`](contracts/circle-usdc/move.sh.tpl) rendered for this network. A Safe owner
  deploys the payout list (the three EOAs; the contract holder is left out, as on mainnet), `ZkMinterBurnerPayout`
  and `UsdcRecoveryBurn`; the Safe runs **one `MultiSendCallOnly` transaction** (`approveHash` by one owner,
  `execTransaction` by another): upgrade USDC.e to `UsdcRecoveryBurn`, `ZkMinterBurner.upgradeToAndCall(
  ZkMinterBurnerPayout, payout())`, restore USDC.e. `payout()` burns each holder's USDC.e and calls
  `bridgeMessage(0, L1Escrow, _, abi.encode(holder, amount))`. The kit writes `recovery.json` (version 1) from the
  `BridgeEvent`s; the two Safe owners that sent transactions sign (no holder signs);
  `prepare-submission.sh` and `verify-submission.sh` run with `--allow-test-fork`.
- **Recoveries** (`53`): the `.tgz` joins the two legacy cases in `tmp/recoveries-cases/`. The ledger skips those
  and issues exactly the kit's outputs as delta exits, **field by field and in the same order**; the balance state
  (`step-c`, `step-f-checks`) is unchanged because Circle's messages carry no ETH (`origin_SC_funds` is empty);
  the legacy ledger entries are kept as they were. Each output's `deposit_count` must be the deposit count its
  exit gets (delta 2's exit tree size + its position). The kit does not write it yet, so `53` adds it to the
  kit's `recovery.json` and repacks the `.tgz` (to remove once the kit does). Negative cases: a message carrying
  ETH paid by a contract that held none at the snapshot (per-contract budget), stale deposit counts
  (anti-replay), and a message from another network.
- **Certificate** (`53`): generated, submitted, settled; same chaining checks as deltas 1 and 2.
- **Claimer** (`61`, `62`): started on delta 2 from `gs://…` in follow mode, it must switch to delta 3 without a
  restart (`/claimer/v2/health` reports the new run; same pid or container). Then, through API v2:
  `/bridges?address=<holder>` returns the holder's exit (`claim_method: claimMessage`, `beneficiary`,
  `decoded_amount`, `display_token`), `/claim-params` its `claimMessage` arguments, and API v1 answers 410 Gone.
- **Claims** (`60`, `62`): every holder ends with exactly its amount of L1 USDC, `L1Escrow` drops by the total paid
  and keeps the contract holder's share, every leaf is claimed, and a second claim of each leaf reverts.

Not reproduced: the mainnet state (holders, Safe owners, the 116M-gas transaction), the real archive bucket (IAM,
retention, real credentials) and the claims UI.

Files per delta (`tmp/`): `recoveries-cases/`, `output-kurtosis-delta-N/recoveries/` (the state of the recoveries
step: `delta-exits.json`, updated `step-c`/`step-f-checks`, ledger, report) and the usual delta outputs in
`output-kurtosis-delta-N/`.

Not covered: claiming some first-certificate exits *before* the delta settles and the rest after (the
index stays the same, so it should not matter for `claimAsset`, but it is not exercised here).

## Scale check (outside the e2e)

The e2e pays three holders. The real Circle USDC recovery has 6,620 outputs in one `recovery.json`.
`check_recoveries_scale.sh` feeds that file to `--step recoveries` offline (a synthetic previous run, no RPC):

```bash
./test/e2e-exit_certificate/check_recoveries_scale.sh <zkevm-circle-usdc-upgrade-submission.tgz | dir | recovery.json>
```

The package is not part of this repo. The script checks that the delta exits equal the outputs field by field and
in order, the ledger, duplicates and determinism, and prints the time, the file sizes and an estimate of the
certificate's JSON size. Step F does not run offline (in delta mode it needs the agglayer).

With a live test network whose last run is settled (for instance the Kurtosis network right after
`run_e2e_test.sh --delta`), it can also chain those exits on that run and run the whole delta pipeline (CHECK → F →
G2 → H → I → SIGN), and with `--submit` send the certificate to the agglayer and wait for it:

```bash
./test/e2e-exit_certificate/check_recoveries_scale.sh <package> \
    --delta-config tmp/exit_certificate-kurtosis-delta-3.json --submit
```

The network must have the network ID the outputs come from (1 for zkEVM, as the Kurtosis L2). The destination
contract of the messages does not exist there, so this measures the certificate, not the claims.

## Regenerating `contracts/Vault.bin`

`contracts/Vault.bin` is the creation bytecode of `contracts/Vault.sol` (solc 0.8.24, `evm_version = "shanghai"`),
committed so the e2e does not need to download a compiler:

```bash
mkdir -p /tmp/vault/src && cp test/e2e-exit_certificate/contracts/Vault.sol /tmp/vault/src/
printf '[profile.default]\nsolc_version = "0.8.24"\nevm_version = "shanghai"\n' > /tmp/vault/foundry.toml
(cd /tmp/vault && forge build --root .)
jq -r '.bytecode.object' /tmp/vault/out/Vault.sol/Vault.json | sed 's/^0x//' | tr -d '\n' > test/e2e-exit_certificate/contracts/Vault.bin
```
