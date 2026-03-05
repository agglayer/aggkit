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