# P4 Step Log

Step P4 — Add kurtosis env-config presets on the kurtosis-cdk branch.

## Outcome

- **Final outcome:** ACCEPTED
- **Validation:** THUMBS_UP on attempt 1
- **Change-request count:** 0

## Summary of work done

Added 4 kurtosis `--args-file` presets plus a README under
`.github/tests/aggkit-e2e-envs/` on the kurtosis-cdk branch
`feat/aggkit-e2e-envs`, committed as `bd3308c9` (5 files, 451 insertions):

- `op-fep.yml` — single OP-succinct L2 (chain `001`), `consensus_contract_type: fep`, op-succinct mock prover (`op_succinct_mock: true`), agglayer integration.
- `op-fep-committee.yml` — as op-fep plus AggOracle committee (`use_agg_oracle_committee: true`, quorum `2`, total members `3`, chain-001 `network_params.seconds_per_slot: 1`).
- `op-pp-2chains.yml` — two OP-PP L2s (`ecdsa-multisig`), multi-document.
- `cdk-erigon-3chains.yml` — three cdk-erigon L2s, multi-document.

Each preset is byte-faithful to the aggkit CI `read-aggkit-args` deep-merge
compositions (`test-e2e.yml` `put_args`, `jq -s 'reduce .[] as $i ({}; . * $i)'`,
last-wins). jq re-derive from source JSONs yields zero diff against each preset
document except the intended, documented deviations.

## Key decisions & deviations

1. **`main.star` deploys exactly ONE L2 per `kurtosis run`** — there is no
   single-file multi-chain arg surface. The two multi-chain envs
   (`op-pp-2chains`, `cdk-erigon-3chains`) are therefore authored as
   **multi-document YAML** (one `---` doc per chain), applied sequentially into
   a shared enclave, mirroring the legacy
   `agglayer/e2e/.github/workflows/aggkit-e2e-multi-chains.yml` flow. The first
   chain (001) deploys the shared L1 + agglayer; later chains set
   `deploy_l1: false` / `deploy_agglayer: false`. In `op-pp-2chains` doc2
   references `el_cl_genesis_data` produced by doc1's L1 deploy — this is the
   expected shared-enclave cross-document dependency (so a standalone dry-run of
   doc2 shows that artifact "does not exist"), **not** a schema error.

2. **Both P1 deviations applied:**
   - `bridge_spammer` dropped from **both** FEP envs
     (`additional_services: []`) for a snapshot-clean enclave (no settlement
     before snapshot). op-pp and cdk-erigon sources already use `[]`.
   - **Custom gas** on cdk-erigon chains **001 and 002** (`gas_token_enabled: true`);
     chain **003** is native (`gas_token_enabled: false`) — mirrors legacy CI
     args-3 / args-4 / args-5 exactly, not a single custom-gas chain.

3. Committee quorum `2` / total `3` and `deploy_l1` / `deploy_agglayer: false`
   toggles were confirmed verbatim against the source JSONs
   (`test_e2e_single_chain_op_succinct_aggoracle_committee_args.json`,
   `test_e2e_op_args_chain_2.json`).

## Changed files

All under `/home/aigent/repos/0xPolygon/kurtosis-cdk/.github/tests/aggkit-e2e-envs/`
(committed `bd3308c9` on `feat/aggkit-e2e-envs`):

- `op-fep.yml`
- `op-fep-committee.yml`
- `op-pp-2chains.yml`
- `cdk-erigon-3chains.yml`
- `README.md`

No aggkit edits. No snapshots committed.

## Commands run

- **Branch/tree checks:** `git branch --show-current`, `git status --short`, `git log --oneline`, `git show --stat HEAD`, `git rev-parse HEAD`.
- **Tooling probes:** `which kurtosis docker`, `kurtosis version`, `kurtosis engine status`, `docker info`.
- **Composition re-derive:** `jq -s 'reduce .[] as $i ({}; . * $i)'` over the 10 aggkit source JSONs, then byte-diff (recursively key-sorted) vs each preset document via python `yaml.safe_load[_all]` — zero diff except the documented FEP `additional_services` deviation.
- **YAML parse:** `yaml.safe_load` / `safe_load_all` of all 4 presets (1/1/2/3 docs).
- **kurtosis dry-run:** `kurtosis run --enclave <tmp> --dry-run --args-file <file|split-doc> .` from the kurtosis-cdk root — EXIT 0 for op-fep, op-fep-committee, and all multi-chain docs; tmp enclaves removed afterward (`kurtosis enclave ls` empty).
- **Snapshot-clean grep:** `grep -rn "bridge_spammer\|additional_services" .../aggkit-e2e-envs/*.yml` — every active `additional_services:` is `[]`; `bridge_spammer` appears only in provenance comments.
- **Commit:** `git add .github/tests/aggkit-e2e-envs && git commit`.

## Blockers

None blocking. Infra-excused follow-up: the heavy live healthy-enclave bring-up
(Tier-3) was deferred due to heavy image pulls / resource cost. Exact resume
commands per env are recorded in the deliverable; no `kurtosis enclave inspect`
output was fabricated. P7–P10 run the live bring-up + snapshot per env.
(Note: validation independently found `aggkit:local` is in fact present in
`docker images` — a minor inaccuracy in the deliverable, not a failure.)

## Future-step updates

For P7–P10 (live bring-up + snapshot per env):

- **Preset paths + invocations:**
  - `op-fep` → `.github/tests/aggkit-e2e-envs/op-fep.yml` →
    `kurtosis run --enclave op-fep --args-file .github/tests/aggkit-e2e-envs/op-fep.yml .`
  - `op-fep-committee` → `.../op-fep-committee.yml` →
    `kurtosis run --enclave op-fep-committee --args-file .../op-fep-committee.yml .`
  - `op-pp-2chains` → `.../op-pp-2chains.yml` — **2 docs**; split with
    `yq 'select(documents()==N)'`, then run sequentially into ONE shared
    enclave (`kurtosis run --enclave op-pp --args-file <doc> .` x2).
  - `cdk-erigon-3chains` → `.../cdk-erigon-3chains.yml` — **3 docs**; split per
    doc, then 3 sequential `kurtosis run --enclave cdk-erigon --args-file <doc> .`.
- **Multi-doc shared-enclave model:** one L2 per `kurtosis run`; chain 001
  deploys L1 + agglayer, later chains set `deploy_l1/agglayer: false` and reuse
  the shared `el_cl_genesis_data` artifact. Apply docs in order into the same
  enclave name.
- **FEP envs:** mock prover (`op_succinct_mock: true`, `op_succinct_agglayer: true`,
  `op_succinct_agg_proof_mode: compressed`); `bridge_spammer` dropped
  (`additional_services: []`); committee adds quorum 2/3 + chain-001
  `seconds_per_slot: 1`.
- **Custom-gas:** cdk-erigon chains **001 and 002** are custom-gas
  (`gas_token_enabled: true`); chain **003** is native.
- **Snapshot `chain_type` keys (ties to P3 per-network tooling):**
  `op-fep`, `op-fep-committee`, `op-pp-2chains` → `chain_type: op-stack`
  (op-reth/op-geth EL; FEP adds op-succinct-proposer captured `settled=false`;
  committee adds a `cdk-data-availability` DAC service);
  `cdk-erigon-3chains` → `chain_type: cdk-erigon`.
- **Image:** all presets reference `aggkit_image: aggkit:local` — a local
  `aggkit:local` build must exist before any live run, or override with
  `--args 'aggkit_image=<tag>'`.
- P3 branch carries snapshot tooling commits `0fe7bf4b` + `5f06bd83`; this P4
  commit `bd3308c9` adds the presets.
