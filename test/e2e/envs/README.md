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

- Generated from kurtosis-cdk branch `feat/aggkit-e2e-envs` @ `bd3308c9`
  (the P4 presets commit; the snapshot tooling extensions for the FEP topology
  are `0fe7bf4b` + `5f06bd83` on the same branch).
- Preset: `.github/tests/aggkit-e2e-envs/op-fep.yml`
- snapshot `chain_type`: `op-stack`; the summary captures an
  `op-succinct-proposer` service marked `settled: false` (FEP prover wired but
  not settled at snapshot time).
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