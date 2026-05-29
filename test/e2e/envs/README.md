# E2E envs

The different E2E envs found on this directory have been generated using the [snapshot feature](https://github.com/0xPolygon/kurtosis-cdk/blob/main/docs/docs/advanced/snapshot.md).

## op-pp

This network has a single OP PP network.

- Kurtosis commit `566ac102b9098f40475c6cc306f03e9750f2ff97`
- Kurtosis config file:

```yml
args:
  l1_electra_fork_epoch: 0
  l1_fulu_fork_epoch: 18446744073709551615

  sequencer_type: op-geth
  consensus_contract_type: ecdsa-multisig
  aggkit_image: ghcr.io/agglayer/aggkit:0.8.0

  # aggsender-validator related params (required for aggkit 0.8.0)
  use_agg_sender_validator: True
  agg_sender_validator_total_number: 1
  agg_sender_multisig_threshold: 1

  # Override additional_services to exclude bridge_spammer
  additional_services:
    - agglogger

  optimism_package:
    chains:
      "001":
        proposer_params:
          enabled: false  # ✅ No state root proposals
        batcher_params:
          max_channel_duration: 999999  # ✅ Delays batching ~11 days
```

## op-fep

This network has a single OP network running in FEP (Full Execution Proof) mode
with an op-succinct **mock** prover and agglayer integration.

- Generated from kurtosis-cdk branch `feat/aggkit-e2e-envs`. The FEP snapshot
  tooling lives in commits `0fe7bf4b` + `5f06bd83` (per-topology capture) and
  `05f04196` (op-reth EL entrypoint). The bootability fixes for the op-reth EL
  restore path — op-reth healthcheck (JSON-RPC POST), op-succinct proposer
  hostname rewrite + writable `/app/configs`, and the Fulu-spec / best-effort
  genesis.ssz patch in the baked beacon — are in commit `b3e13ba9` on the
  same branch. The compose in this dir is
  regenerated deterministically by `snapshot/scripts/generate-compose.sh` from
  the captured snapshot state.
- Preset: `.github/tests/aggkit-e2e-envs/op-fep.yml`
- snapshot `chain_type`: `op-stack`; the summary captures an
  `op-succinct-proposer` service marked `settled: false` (FEP prover wired but
  not settled at snapshot time). The L2 EL is op-reth (`op-reth:v2.2.5`) run
  with `op-reth-entrypoint.sh`; the summary logical service key stays `op-geth`
  for loader compatibility.
- Boot status: `docker compose up -d` brings all core services healthy
  (geth/beacon/validator L1, op-reth EL, op-node, agglayer, aggkit, postgres);
  `E2E_ENV=op-fep go test ./test/e2e/... -run TestMain` passes LoadEnv + CheckEnv
  and a full L1->L2 ERC20 bridge round-trip. The L2->L1 direction needs FEP
  proof settlement, which the op-succinct proposer cannot perform on a restored
  snapshot (it enforces an on-chain rollup-config-hash that the snapshot's
  genesis-time re-anchoring changes) — consistent with `settled: false`.
- Kurtosis config (key args, faithful mirror of the aggkit CI `op_succinct_args`
  composition minus `bridge_spammer`):

```yml
deployment_stages:
  deploy_op_succinct: true
  deploy_cdk_bridge_infra: false

args:
  aggkit_image: aggkit:local
  consensus_contract_type: fep
  use_agg_sender_validator: true
  agg_sender_multisig_threshold: 2
  agg_sender_validator_total_number: 3
  # Override additional_services to exclude bridge_spammer (snapshot-clean)
  additional_services: []
  binary_name: aggkit
  aggkit_components: aggsender,aggoracle
  l2_chain_id: 20201
  l2_network_id: 1
  op_succinct_mock: true
  op_succinct_agglayer: true
  op_succinct_agg_proof_mode: compressed
  op_succinct_submission_interval: "1"

optimism_package:
  chains:
    "001":
      proposer_params:
        enabled: false
      network_params:
        network_id: "20201"
```

- Regenerate with:

```
cd kurtosis-cdk   # branch feat/aggkit-e2e-envs
kurtosis run --enclave op-fep --args-file .github/tests/aggkit-e2e-envs/op-fep.yml .
./snapshot/snapshot.sh op-fep
./snapshot/verify.sh snapshot/snapshots/op-fep-<TIMESTAMP>/
# then copy snapshot/snapshots/op-fep-<TIMESTAMP>/ contents into
# aggkit test/e2e/envs/op-fep/ (strip the timestamped wrapper dir).
```

## op-fep-committee

This network is the `op-fep` topology (single OP network in FEP mode, op-succinct
**mock** prover, agglayer integration) **plus an AggOracle committee**: the
AggOracle is governed by an on-chain `AggOracleCommittee` proxy with a **2-of-3**
quorum (`use_agg_oracle_committee: true`, `agg_oracle_committee_quorum: 2`,
`agg_oracle_committee_total_members: 3`).

- Generated from kurtosis-cdk branch `feat/aggkit-e2e-envs`, commit `d71f4265`.
  It inherits all the op-fep op-reth/Teku/proposer bootability fixes (`b3e13ba9`)
  and adds (in `d71f4265`) a minimal, backward-compatible committee-capture path
  to the snapshot tool
  (`snapshot/scripts/discover-containers.sh`, `extract-state.sh`,
  `generate-compose.sh`, `generate-summary.sh`): the extra AggOracle committee
  member services (kurtosis `aggkit-001-aggoracle-committee-00N`, each an aggkit
  `--components=aggoracle` service with its own `aggoracle-N.keystore`) are
  discovered, their `/etc/aggkit` (config + keystores) captured under
  `config/001/committee/00N/etc`, re-emitted as compose services, and the
  `AggOracleCommittee` proxy address is recorded in `summary.json` under
  `networks.l2_networks.001.contracts.aggoracle_committee`. The same commit also
  lengthens the L1 geth SIGTERM grace in `extract-state.sh` so geth flushes its
  full chain head to disk before capture (a short grace produced a committee
  snapshot whose restored geth lagged its rollup L1-origin, so op-reth could
  never reach the origin block).
- Preset: `.github/tests/aggkit-e2e-envs/op-fep-committee.yml` (faithful mirror
  of the aggkit CI `single_chain_op_succinct_aggoracle_committee_args`
  composition minus `bridge_spammer`; `additional_services: []`).
- Difference vs `op-fep`: adds two `aggkit-001-aggoracle-committee-000/001`
  services + their captured keystores, and the `contracts.aggoracle_committee`
  address. Everything else (op-reth EL, op-node, agglayer, postgres,
  op-succinct-proposer marked `settled: false`) is identical to op-fep.
- Loader: the `EnvOpFEPCommittee` ENVName (`NativeGas: true`) binds the on-chain
  `AggOracleCommittee` contract (`L2Contracts.AggOracleCommittee` /
  `AggOracleCommitteeAddress`) when the summary carries the committee address,
  exposing `Quorum()` / `GetAllAggOracleMembers()` / `GetAggOracleMembersCount()`
  to callers. A minimal load+read probe
  (`test/e2e/committee_probe_test.go::TestAggOracleCommitteeQuorumProbe`)
  verifies the 2-of-3 quorum on-chain; it is inert (skips) for non-committee envs.
- Boot status: `docker compose up -d` brings all core services healthy
  (geth/beacon/validator L1, op-reth EL, op-node, agglayer, aggkit, postgres) plus
  the two committee member aggkit services; the op-succinct proposer crash-loops
  on FEP settlement (same `settled: false` limitation as op-fep — architectural,
  out of scope).
- Kurtosis config (key args):

```yml
deployment_stages:
  deploy_op_succinct: true
  deploy_cdk_bridge_infra: false

args:
  aggkit_image: aggkit:local
  consensus_contract_type: fep
  use_agg_sender_validator: true
  agg_sender_multisig_threshold: 2
  agg_sender_validator_total_number: 3
  additional_services: []
  binary_name: aggkit
  aggkit_components: aggsender,aggoracle
  l2_chain_id: 20201
  l2_network_id: 1
  op_succinct_mock: true
  op_succinct_agglayer: true
  op_succinct_agg_proof_mode: compressed
  op_succinct_submission_interval: "1"
  use_agg_oracle_committee: true
  agg_oracle_committee_quorum: 2
  agg_oracle_committee_total_members: 3

optimism_package:
  chains:
    "001":
      proposer_params:
        enabled: false
      network_params:
        network_id: "20201"
        seconds_per_slot: 1
```

- Regenerate with:

```
cd kurtosis-cdk   # branch feat/aggkit-e2e-envs
kurtosis run --enclave op-fep-committee --args-file .github/tests/aggkit-e2e-envs/op-fep-committee.yml .
./snapshot/snapshot.sh op-fep-committee
# then copy snapshots/op-fep-committee-<TIMESTAMP>/ contents into
# aggkit test/e2e/envs/op-fep-committee/ (strip the timestamped wrapper dir).
```

## op-pp-2chains

This env has **two** OP-PP (pessimistic, `ecdsa-multisig` consensus) L2 networks
sharing one L1 + agglayer — the same topology as the single-chain `op-pp`, just
two chains:

- chain `001`: `l2_chain_id 20201`, `l2_network_id 1` — deploys the shared L1 +
  agglayer.
- chain `002`: `l2_chain_id 20202`, `l2_network_id 2` — reuses the L1 + agglayer
  (`deploy_l1: false` / `deploy_agglayer: false`), `deployment_suffix "-002"`.

- Generated from kurtosis-cdk branch `feat/aggkit-e2e-envs`, commit `d71f4265`
  (snapshotted 2026-05-29; snapshot name `op-pp-2chains-20260529-220608`). The
  multi-L2 snapshot capture (P3) and the op-reth EL + L1-geth archive-flush
  bootability fixes (inherited from the op-fep work) worked unchanged for this
  topology — no kurtosis-cdk source changes were needed.
- Preset: `.github/tests/aggkit-e2e-envs/op-pp-2chains.yml` — a **multi-document
  YAML** (one `---` document per chain). `main.star` deploys exactly one L2 per
  `kurtosis run`, so the two documents are applied **sequentially into one shared
  enclave** (the legacy `aggkit-e2e-multi-chains.yml` model): doc 1 deploys the
  L1 + agglayer + L2-001, doc 2 reuses them and adds L2-002.
- `summary.json` lists **both** `001` and `002` under `networks.l2_networks` with
  distinct ports (001: op-geth `:11545` / aggkit `:11576`; 002: op-geth `:12545`
  / aggkit `:12576`), each with its own contracts and accounts. The compose
  carries both L2 stacks (`op-geth-001`/`op-node-001`/`aggkit-001` and the `-002`
  equivalents) plus the single shared L1 (geth/beacon/validator) and agglayer.
  The L2 EL is op-reth (`op-reth:v2.2.5`) run with `op-reth-entrypoint.sh`; the
  summary logical service key stays `op-geth` for loader compatibility.
- Loader: `EnvOpPP2Chains` (`NativeGas: true`). `LoadEnv` parses both networks
  into `Env.L2s` (`len >= 2`); `Env.L2` / `PrimaryL2()` is `001`,
  `L2ByNetworkID(2)` is `002`. A minimal in-worktree loader fix was made so
  multi-network connectivity checks dial each network's **op-geth** RPC: a new
  `L2Config.OpGethRPCURL` field is populated from
  `networks.l2_networks.<key>.services.op-geth.http_rpc.external`, and
  `clientForNetwork` now prefers it (previously non-primary networks fell back to
  the aggkit node RPC, which does not serve `eth_*`). `CheckEnv` validates both
  networks; a generic per-chain health probe
  (`test/e2e/envs/zz_p9_probe_test.go`) asserts chain id / advancing block /
  non-zero balance for both 001 and 002. No L2↔L2 bridging test is included.
- Boot status: `docker compose up -d` brings all 10 services healthy
  (geth/beacon/validator L1, agglayer, and op-geth/op-node/aggkit for both 001
  and 002); both L2s restore from baked state and produce blocks
  (chain ids 20201 / 20202).
- Kurtosis config (the two `---` documents, faithful mirror of the aggkit CI
  `kurtosis-cdk-args-1` / `-args-2` compositions minus `bridge_spammer`):

```yml
# === Document 1: chain "001" (deploys shared L1 + agglayer) ===
deployment_stages:
  deploy_op_succinct: false
  deploy_cdk_bridge_infra: false
args:
  aggkit_image: aggkit:local
  consensus_contract_type: ecdsa-multisig
  use_agg_sender_validator: true
  agg_sender_multisig_threshold: 2
  agg_sender_validator_total_number: 3
  additional_services: []
  binary_name: aggkit
  aggkit_components: aggsender,aggoracle
  l2_chain_id: 20201
  l2_network_id: 1
optimism_package:
  predeployed_contracts: true
  chains:
    "001":
      proposer_params:
        enabled: false
      network_params:
        network_id: "20201"
---
# === Document 2: chain "002" (reuses the shared L1 + agglayer) ===
deployment_stages:
  deploy_op_succinct: false
  deploy_cdk_bridge_infra: false
  deploy_l1: false
  deploy_agglayer: false
args:
  aggkit_image: aggkit:local
  consensus_contract_type: ecdsa-multisig
  use_agg_sender_validator: true
  agg_sender_multisig_threshold: 2
  agg_sender_validator_total_number: 3
  additional_services: []
  binary_name: aggkit
  aggkit_components: aggsender,aggoracle
  l2_chain_id: 20202
  l2_network_id: 2
  deployment_suffix: "-002"
optimism_package:
  chains:
    "002":
      proposer_params:
        enabled: false
      network_params:
        network_id: "20202"
```

- Regenerate with (split the multi-doc preset, then apply both docs IN ORDER to
  one enclave):

```
cd kurtosis-cdk   # branch feat/aggkit-e2e-envs
yq 'select(documents() == 0)' .github/tests/aggkit-e2e-envs/op-pp-2chains.yml > /tmp/op-pp-1.yml
yq 'select(documents() == 1)' .github/tests/aggkit-e2e-envs/op-pp-2chains.yml > /tmp/op-pp-2.yml
kurtosis run --enclave op-pp-2chains --args-file /tmp/op-pp-1.yml .   # chain 001 -> L1 + agglayer + L2-001
kurtosis run --enclave op-pp-2chains --args-file /tmp/op-pp-2.yml .   # chain 002 -> reuses L1 + agglayer, adds L2-002
./snapshot/snapshot.sh op-pp-2chains
# then copy snapshots/op-pp-2chains-<TIMESTAMP>/ contents into
# aggkit test/e2e/envs/op-pp-2chains/ (strip the timestamped wrapper dir).
```

## Debug

In order to debug (VS Code):

1. Run `make build-docker-debug` (from the root of the repo)
2. Uncomment the commented `aggkit-001` of the `docker-compose.yml` and comment the uncommented one
3. Run the E2E test
4. You will need to start the debugger every single time that the aggkit docker starts. It doesn't run otherwise. Add the following content to your `.vscode/launch.json`:

```json
{
    "version": "0.2.0",
    "configurations": [
        {
            "name": "Attach to aggkit in Docker (dlv)",
            "type": "go",
            "request": "attach",
            "mode": "remote",
            "host": "127.0.0.1",
            "port": 40000,
            "apiVersion": 2,
            "showLog": true
        }
    ]
}
```