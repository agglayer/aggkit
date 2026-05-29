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
- Loader: `EnvOpFEP` (capabilities `Sequencer: op-stack`, `NativeGas: true`,
  `SettlementSupported: false`, single-network/single-aggkit). `LoadEnv` parses
  the single FEP network into `Env.L2` / `PrimaryL2()`; the per-network L2 EL RPC
  is dialed via `L2Config.OpGethRPCURL` (populated from
  `networks.l2_networks.001.services.op-geth.http_rpc.external`). Because
  `SettlementSupported: false`, `testmain_test.go` capability-gates out the
  post-test L2->L1 bridge-settlement assertion for this env (boot/load/checks
  smoke only).
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

## cdk-erigon-3chains

This env has **three cdk-erigon** (pessimistic, `ecdsa-multisig` consensus) L2
networks sharing one L1 + agglayer. It is the only env whose sequencer is
**cdk-erigon** (not op-stack/op-geth/op-reth) and the only **mixed custom-gas**
env: two chains use a custom gas token, one is native ETH.

- chain `001`: `l2_chain_id 2151908`, `l2_network_id 1` — deploys the shared L1 +
  agglayer; **custom gas token** (`gas_token_enabled: true`). NOTE: doc 1 does not
  override `l2_chain_id`, so 001 keeps the cdk-erigon default chain id `2151908`.
- chain `002`: `l2_chain_id 20202`, `l2_network_id 2` — reuses L1 + agglayer
  (`deploy_l1: false` / `deploy_agglayer: false`), `deployment_suffix "-002"`;
  **custom gas token**.
- chain `003`: `l2_chain_id 20203`, `l2_network_id 3` — reuses L1 + agglayer,
  `deployment_suffix "-003"`; **native** gas (`gas_token_enabled: false`).

- Generated from kurtosis-cdk branch `feat/aggkit-e2e-envs` (snapshotted
  2026-05-29; snapshot name `cdk-erigon-20260529-225318`). Built on the P3
  per-topology cdk-erigon capture. Several **bounded, backward-compatible
  cdk-erigon-only** snapshot-tool fixes were required (op-pp / op-fep /
  op-pp-2chains captures unaffected — gated on `chain_type == "cdk-erigon"` or
  on the absence of op-stack artifacts):
  - `discover-containers.sh`: discover the per-network `contracts-<prefix>`
    deployer container.
  - `extract-state.sh`: for cdk-erigon chains also extract the aggkit
    `config.toml` + keystores and the deployment output `combined-<prefix>.json`
    (the authoritative custom gas-token source).
  - `generate-summary.sh`: read the L2 chain id from `dynamic-*-chainspec.json`
    (fallback `zkevm.l2-chain-id` in config.yaml); read `contracts.gas_token`
    from `combined.json` (`gasTokenAddress`, zero address dropped as native);
    add the cdk-erigon L2 mnemonic
    (`lab code glass agree ...`) to the pk map and source L2 accounts from the
    flat `dynamic-*-allocs.json` (no `.alloc` wrapper) so the pre-funded admin
    EOA carries a usable private key.
  - `generate-compose.sh`: cdk-erigon EL service uses `entrypoint: ["cdk-erigon"]`
    + flags-only command, `user: "0:0"` (matches kurtosis `User(uid=0,gid=0)`, so
    the root-owned datadir volume is writable), and network aliases so the
    captured configs resolve the kurtosis hostnames (`el-1-geth-lighthouse` for
    L1; `cdk-erigon-rpc-<prefix>` for the L2 EL). cdk-erigon aggkit runs
    `--components=aggsender,bridge` (the `aggoracle` component is dropped: a
    snapshot-clean cdk-erigon EL boots a fresh datadir and re-derives blocks as a
    sequencer, which does not replay the post-genesis L2 GER-manager
    initialization, so `globalExitRootUpdater()` reverts and aggoracle would
    crash-loop; aggsender + bridge do not need it).
- Preset: `.github/tests/aggkit-e2e-envs/cdk-erigon-3chains.yml` — a
  **multi-document YAML** (three `---` documents, one standalone single-chain
  args-file each), a faithful mirror of the legacy aggkit CI
  `kurtosis-cdk-args-{3,4,5}` compositions (`cdk_erigon_args_base` +
  `multi_chains_args_{2,3}` + `custom_gas_token`). `main.star` deploys one L2 per
  `kurtosis run`, so the three documents are applied **sequentially into one
  shared enclave**: doc 1 deploys L1 + agglayer + L2-001 (custom gas), doc 2
  reuses them and adds L2-002 (custom gas), doc 3 adds L2-003 (native).
- `summary.json` lists `001`, `002`, `003` under `networks.l2_networks`, each
  carrying its L2 RPC under the **`cdk-erigon`** service key (NOT `op-geth`) —
  internal `http://cdk-erigon-<prefix>:8545`, external 001:`:11545` / 002:`:12545`
  / 003:`:13545`. `001` and `002` carry `contracts.gas_token`
  (`0x72ae2643…` / `0xB965D107…`); `003` does not. The compose carries three
  `cdk-erigon-<prefix>` ELs + three `aggkit-<prefix>` + the shared L1
  (geth/beacon/validator) + agglayer.
- Loader: `EnvCDKErigon3Chains` (`Sequencer: cdk-erigon`, `MultiNetwork`,
  `MultiAggkit`). Bounded loader changes:
  - `summaryL2Network.Services` gained a `cdk-erigon` service struct, and
    `Contracts` gained `gas_token`. A new `l2RPCURLForNetwork` helper selects the
    per-network L2 EL RPC (prefer `op-geth`, else `cdk-erigon`); it backs the
    per-network client dial, the `L2Config.OpGethRPCURL` field (still populated —
    sequencer-agnostically — for op-stack byte-compatibility; rename flagged for
    P12), and `waitForServices`.
  - `L2Contracts.GasTokenAddress` surfaces the custom gas-token address when
    present (no ABI binding).
  - Per-network gas model: the env-level `NativeGas` flag means "native deploys
    permitted" (`true` here); the MintableERC20 auto-deploy is gated per network
    on `NativeGas AND (network has no gas_token)`. So `001`/`002` skip the deploy
    and surface their gas token, `003` deploys MintableERC20. op-* envs are
    unchanged (no `gas_token` ⇒ same env-level behavior).
- `CheckEnv` validates all three networks (topology-agnostic). A generic
  per-chain health probe (`test/e2e/envs/zz_p10_probe_test.go`) asserts chain id /
  advancing block / live `NetworkID` for all three and the surfaced gas-token
  address for 001/002. It is a verification harness only — no migrated bridge
  test (the legacy `bridge-e2e-3-chains` / `bridge-e2e-custom-gas` ports are out
  of scope).
- Boot status: `docker compose up -d` brings all 11 services healthy
  (geth/beacon/validator L1, agglayer, and `cdk-erigon`/`aggkit` for 001/002/003);
  the three cdk-erigon ELs re-derive from L1 and produce blocks (chain ids
  2151908 / 20202 / 20203). Proven via `docker compose up -d` and the LoadEnv
  path — NOT `verify.sh` (docker-py host bug).
- Regenerate with (split the multi-doc preset, then apply all three docs IN ORDER
  to one enclave):

```
cd kurtosis-cdk   # branch feat/aggkit-e2e-envs
yq 'select(documents() == 0)' .github/tests/aggkit-e2e-envs/cdk-erigon-3chains.yml > /tmp/cdk-1.yml
yq 'select(documents() == 1)' .github/tests/aggkit-e2e-envs/cdk-erigon-3chains.yml > /tmp/cdk-2.yml
yq 'select(documents() == 2)' .github/tests/aggkit-e2e-envs/cdk-erigon-3chains.yml > /tmp/cdk-3.yml
kurtosis run --enclave cdk-erigon --args-file /tmp/cdk-1.yml .   # chain 001 -> L1 + agglayer + L2-001 (custom gas)
kurtosis run --enclave cdk-erigon --args-file /tmp/cdk-2.yml .   # chain 002 -> reuses L1 + agglayer, adds L2-002 (custom gas)
kurtosis run --enclave cdk-erigon --args-file /tmp/cdk-3.yml .   # chain 003 -> reuses L1 + agglayer, adds L2-003 (native)
./snapshot/snapshot.sh cdk-erigon --skip-verify
# then copy snapshots/cdk-erigon-<TIMESTAMP>/ contents into
# aggkit test/e2e/envs/cdk-erigon-3chains/ (strip the timestamped wrapper dir).
# (If yq is unavailable, split the 3 docs with PyYAML safe_load_all.)
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