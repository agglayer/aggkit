COMMON_MOCKERY_PARAMS=--disable-version-string --with-expecter --exported

.PHONY: generate-mocks
generate-mocks:	
	mockery ${COMMON_MOCKERY_PARAMS}

.PHONY: test-e2e-fork12-pessimistic
test-e2e-fork12-pessimistic: stop
	@bash -c '\
		set -euo pipefail; \
		./run-e2e.sh fork12 pessimistic; \
		export L2_RPC_NODE=cdk-erigon-rpc-001; \
		export BATS_TEST_TIMEOUT=1800; \
		bats bats/pp/bridge-native-token-e2e.bats \
			 bats/pp/bridge-e2e-msg.bats \
			 bats/pp/e2e-pp.bats; \
	'

.PHONY: test-e2e-fork12-op-succint
test-e2e-fork12-op-succint: stop
	@bash -c '\
		set -euo pipefail; \
		source ./setup-helpers.sh; \
		./run-e2e.sh fork12 op-succint; \
		export L2_RPC_NODE=op-el-1-op-geth-op-node-001; \
		SENDER_PRIVATE_KEY=ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80; \
		fund "$$SENDER_PRIVATE_KEY" 0xE34aaF64b29273B7D567FCFc40544c014EEe9970 1000ether "$$L2_ETH_RPC_URL"; \
		export BATS_TEST_TIMEOUT=1800; \
		bats bats/pp/bridge-native-token-e2e.bats \
		     bats/pp/bridge-e2e-msg.bats \
		     bats/pp/e2e-pp.bats; \
	'

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
