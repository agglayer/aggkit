# P3 Step Log

Step P3 — Extend the kurtosis-cdk snapshot tool for the new topologies.

## Outcome

ACCEPTED. Validation THUMBS_UP on attempt 2. Change-request count: 1.

## Summary of work done

Extended and reconciled the kurtosis-cdk snapshot tool on branch
`feat/aggkit-e2e-envs` so the emitted `summary.json` and `docker-compose.yml`
match the schema the aggkit e2e loader (`test/e2e/envs/loader.go`) and the
committed `op-pp` reference env actually consume, and so the tool can capture
the three previously-unsupported topologies.

- **Iteration 1 (commit `0fe7bf4b`):** op-reth → op-geth key reconciliation
  across the loader-facing emitters. Renamed the emitted L2 EL service key,
  hostnames, compose service/container/hostname, the entrypoint script, and the
  adapted aggkit/agglayer RPC URLs from `op-reth*` to `op-geth*` in
  `generate-summary.sh`, `generate-compose.sh`, the renamed
  `op-geth-entrypoint.sh`, `op-node-entrypoint.sh`, `verify.sh`,
  `verify-healthchecks.sh`, and `adapt-l2-config.sh`. Confirmed (with a synthetic
  two-network fixture) that the existing all-`l2_networks` loop already emits
  every discovered L2 network, each carrying `{chain_id, contracts, services,
  accounts}`, so multi-L2 emission works with the corrected keys.

- **Iteration 2 (commit `5f06bd83`):** added per-topology CAPTURE logic for the
  three missing topologies (FEP/op-succinct, cdk-erigon multi-chain incl.
  custom-gas, committee/DAC). Introduced a per-network `chain_type` field
  (`op-stack` | `cdk-erigon`) recorded by discovery and read by
  extract/compose/summary; absence defaults to `op-stack`, making the change
  additive and backward-compatible. Wired across 5 snapshot scripts.

## Change-request history

- **Attempt 1 — CHANGE_REQUEST** (`validation_result_1.md`): criterion (a)(i)
  per-topology capture was missing. The op-reth→op-geth reconciliation (b),
  all-`l2_networks` multi-L2 emission half of (a), backward-compat (c), shell
  validity (d), and branch/commit hygiene (e) were all already met and verified.
  The validator distinguished the missing code-level capture logic (P3 scope,
  blocking) from the live enclave run (infra-excused, non-blocking).
- **Attempt 2 — THUMBS_UP** (`validation_result_2.md`): the added capture logic
  for FEP/op-succinct, cdk-erigon, and committee/DAC resolved the sole blocker.
  The validator independently re-ran the offline proofs and they reproduced
  exactly; previously-accepted items remained intact.

## Key decisions & deviations

1. **op-reth → op-geth reconciliation (non-obvious split):** loader-facing
   emitted keys and hostnames were switched to `op-geth` to match `loader.go`'s
   `OpGeth` json tag and the committed `op-pp` reference. The Kurtosis-internal
   discovery names were intentionally left as `op-reth`: the container-name grep
   patterns (`op-el-*-op-reth-op-node`), the discovery JSON keys
   (`op_reth_sequencer`/`op_reth_rpc`), and the `OP_RETH_IMAGE` read. Those are
   the real Kurtosis service names and never reach the loader, so renaming them
   would break discovery.
2. **FEP capture** emits an `op-succinct-proposer` summary service with
   `settled: false` — encoding the no-settlement-before-snapshot guarantee.
   Proposer env (`proposer-env.json`), `/app/configs`, and a best-effort
   `pg_dump` of the companion prover postgres are captured; no proof submission
   is triggered (relies on the existing STEP-3 L1-stop-before-extract).
3. **cdk-erigon capture** tars `/home/erigon/data/dynamic-*-sequencer` and copies
   `/etc/cdk-erigon` (config + dynamic chain-config/allocs that carry custom-gas
   settings), emits a distinct `cdk-erigon-<prefix>` EL service plus a
   `cdk-erigon` summary key, and surfaces `contracts.gas_token` and the erigon
   `chain_id` (read from the dynamic chain-config when no rollup.json exists).
4. **committee/DAC capture** discovers `cdk-data-availability`, captures its
   config and `dac.keystore`, emits a DAC compose service plus a
   `cdk-data-availability` summary key and a DAC committee-member account.
5. **Backward-compat preserved:** a discovery.json with no `chain_type` (default
   `op-stack`) produces byte-identical op-pp output — re-proven by the validator.
6. **Scope deviation (iteration 1):** net-new capture code was initially deferred
   on the argument it could not be validated without live enclaves. The validator
   held that capture logic is P3 scope (distinct from the P7–P10 per-env args),
   prompting iteration 2.

## Changed files

All under `0xPolygon/kurtosis-cdk` `snapshot/`. No aggkit edits, no `main.star`,
no CI, no per-env-args files touched.

Iteration 1 (`0fe7bf4b`):
- `snapshot/scripts/generate-summary.sh`
- `snapshot/scripts/generate-compose.sh`
- `snapshot/scripts/op-reth-entrypoint.sh` → `snapshot/scripts/op-geth-entrypoint.sh` (git rename + edits)
- `snapshot/scripts/op-node-entrypoint.sh`
- `snapshot/verify.sh`
- `snapshot/scripts/verify-healthchecks.sh`
- `snapshot/scripts/adapt-l2-config.sh`

Iteration 2 (`5f06bd83`):
- `snapshot/scripts/discover-containers.sh`
- `snapshot/scripts/extract-state.sh`
- `snapshot/scripts/generate-compose.sh`
- `snapshot/scripts/generate-summary.sh`
- `snapshot/scripts/verify-healthchecks.sh`

## Commands run

(Summarized.)
- `bash -n` on every changed script — all OK (both iterations).
- `shellcheck -S warning` on every changed script — zero findings.
- `jq -e` assertions on synthetic discovery.json fixtures (a 2-network op-stack
  fixture in iteration 1; a 3-topology fixture — 001 cdk-erigon+DAC custom-gas,
  002 op-stack, 003 op-stack+FEP — in iteration 2), asserting every
  loader-dereferenced path and the new per-topology service keys.
- `docker compose -f <emitted compose> config` — VALID for the 3-topology fixture
  and the legacy op-pp fixture.
- `bash scripts/generate-summary.sh` / `generate-compose.sh` against fixtures —
  exit 0, output asserted; `grep -c op-reth summary.json` = 0.
- `git mv` for the entrypoint rename; `git add` + `git commit` (commits
  `0fe7bf4b` and `5f06bd83`). Not pushed, no PR (PR/merge is P12).

## Blockers

None blocking acceptance.

Documented, infra-excused follow-up: the LIVE `snapshot.sh` + `verify.sh` enclave
run was NOT performed — there is no live Kurtosis enclave (`kurtosis enclave ls`
is empty; the running `cdk-*` containers are a restored docker-compose snapshot,
not a snapshot-able enclave). It requires the P7–P10 per-env `kurtosis run` args
and must be exercised when those envs are built. No live/enclave/verify output was
fabricated; the validator independently confirmed this and excused the un-run live
pass under its infra rule.

## Future-step updates

- **P4 / P7–P10:** the snapshot tool now keys per-topology behavior off a
  per-network `chain_type` field (`op-stack` default, plus `cdk-erigon`). Emitted
  summary service keys per topology: `op-geth`/`op-node`/`aggkit` (op-stack),
  `op-succinct-proposer` (FEP, with `settled: false`), `cdk-erigon`, and
  `cdk-data-availability`. The LIVE `snapshot.sh` + `verify.sh` pass must be run
  per env in P7–P10 once the per-env args exist (op-pp, FEP, cdk-erigon
  multi/custom-gas, committee), including FEP post-restore settlement progression.
- **P5 / P12:** the emitter now uses `op-geth` (matches `loader.go`) —
  reconciliation is closed for op-pp; confirm per new env. New additive summary
  fields are `chain_type`, `contracts.gas_token`, and service keys `cdk-erigon`,
  `op-succinct-proposer`, `cdk-data-availability` (Go JSON ignores unknown fields,
  so the current loader is unaffected). If P5 wants the loader to drive cdk-erigon
  chains, it must add a service struct field with json tag `cdk-erigon` matching
  the emitted key — coordinate the exact tag with P5.
