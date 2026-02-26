# E2E

This package contains tests run against a dockerize environment, loaded from `test/e2e/envs/loader.go`. The tests follows this flow:

1. Test env is loaded by `test/e2e/testmain_test.go`
2. Some sanity checks are performed to assert that the testing env is operating as expected
3. Tests in this package are then run
4. Finally, after the actual tests are run, if they pass, a L1 -> L2 and a L2 -> L1 bridge are going to be sent to validate that the network is still operational after the tests

## Inventory

- `test/e2e/removeger_test.go`: test the remove GER tool (`tools/remove_ger`)