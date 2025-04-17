COMMON_MOCKERY_PARAMS=--disable-version-string --with-expecter --exported

.PHONY: generate-mocks
generate-mocks:	
	mockery ${COMMON_MOCKERY_PARAMS}

.PHONY: test-e2e-fork12-pessimistic
test-e2e-fork12-pessimistic: stop
	L2_ETH_RPC_URL=$$( { ./run-e2e.sh fork12 pessimistic cdk-erigon-rpc-001 2>&1 | tee /dev/stderr; } | tail -n1 ) && \
	export BATS_TEST_TIMEOUT=1800 && \
	export L2_ETH_RPC_URL=$$L2_ETH_RPC_URL && \
	bats bats/pp/bridge-native-token-e2e.bats \
		bats/pp/bridge-e2e-msg.bats \
		bats/pp/e2e-pp.bats

.PHONY: test-e2e-fork12-op-succint
test-e2e-fork12-op-succint: stop
	L2_ETH_RPC_URL=$$( { ./run-e2e.sh fork12 op-succint op-el-1-op-geth-op-node-001 2>&1 | tee /dev/stderr; } | tail -n1 ) && \
	export BATS_TEST_TIMEOUT=1800 && \
	export L2_ETH_RPC_URL=$$L2_ETH_RPC_URL && \
	bats bats/pp/bridge-native-token-e2e.bats \
	     bats/pp/bridge-e2e-msg.bats \
	     bats/pp/e2e-pp.bats

.PHONY: test-e2e-fork12-multi-pessimistic
test-e2e-fork12-multi-pessimistic: stop
	./run-e2e-multi_pp.sh
	export BATS_TEST_TIMEOUT=1800
	bats bats/pp-multi

.PHONY: stop
stop:
	kurtosis clean --all


## Help display.
## Pulls comments from beside commands and prints a nicely formatted
## display with the commands and their usage information.
.DEFAULT_GOAL := help

.PHONY: help
help: ## Prints this help
		@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
