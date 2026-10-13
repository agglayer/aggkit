# E2E

This package contains tests run against a dockerized environment, loaded from `test/e2e/envs/loader.go`. The tests follow this flow:

1. Test env is loaded by `test/e2e/testmain_test.go`
2. Some sanity checks are performed to assert that the testing env is operating as expected
3. Tests in this package are then run
4. Finally, after the actual tests are run, if they pass, a L1 -> L2 and a L2 -> L1 bridge are going to be sent to validate that the network is still operational after the tests

## Inventory

- `test/e2e/bridge_fee_estimate_test.go`: `TestBridgeFeeEstimateViaProxy` sends the three bridging mechanisms (native ETH, ERC20 lock, wrapped burn) and requires `GET /bridge/v1/bridge-fee-estimate`, answered through the aggkit-proxy, to serve each mechanism's estimate (`cost == gas_units × gas_price`, no margin) and to fail closed with 503 while a mechanism has no samples; unsupported networks and missing/invalid parameters must answer 4xx.
- `test/e2e/claim_fee_estimate_test.go`: `TestClaimFeeEstimateViaProxy` requires `GET /bridge/v1/claim-fee-estimate`, answered through the aggkit-proxy, to serve first-claim (wrapper deployment) and repeat-claim estimates with the documented shape (`cost == gas_units × gas_price`, first claim costing more gas than a repeat) and to fail closed with 503 for buckets with no samples; unsupported networks and missing/invalid parameters must answer 4xx.
- `test/e2e/removeger_test.go`: test the remove GER tool (`tools/remove_ger`)