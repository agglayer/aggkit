# E2E envs

The different E2E envs found on this directory have been generated using the [snapshot feature](TODO: add link once pushed to kurtosis repo).

## op-pp

This network has a single OP PP network.

- Kurtosis commit `27d6824f604cbcf346457acf155b4161a0c5781d`
- Kurtosis config file:

```yml
args:
  sequencer_type: op-geth
  consensus_contract_type: ecdsa-multisig
  aggkit_image: ghcr.io/agglayer/aggkit:0.8.0

  # aggsender-validator related params (required for aggkit 0.8.0)
  use_agg_sender_validator: True
  agg_sender_validator_total_number: 1
  agg_sender_multisig_threshold: 1

  # Disable Electra to avoid engine_forkchoiceUpdatedV4 (Geth 1.16.8 doesn't support it)
  ethereum_package:
    network_params:
      electra_fork_epoch: 18446744073709551615

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