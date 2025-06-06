COMMON_MOCKERY_PARAMS=--disable-version-string --with-expecter --exported

.PHONY: generate-mocks
generate-mocks:	
	mockery ${COMMON_MOCKERY_PARAMS}

.PHONY: test-e2e-fork12-pessimistic
test-e2e-fork12-pessimistic: stop
	./run-e2e.sh fork12 pessimistic
	export BATS_TEST_TIMEOUT=1800
	bats bats/pp/bridge-native-token-e2e.bats \
	     bats/pp/bridge-e2e-msg.bats \
	     bats/pp/e2e-pp.bats

.PHONY: test-e2e-fork12-op-succinct
test-e2e-fork12-op-succinct: stop
	./run-e2e.sh fork12 op-succinct
	# Adding a delay to ensure that bridge service is not started before L2 op node
	# This is a temporary fix because there is a race condition in kurtosis somewhere
	sleep 30
	export BATS_TEST_TIMEOUT=1800
	# This for fail fast is some service dead at start
	./scripts/check_kurtosis_services_are_alive 60 aggkit aggkit-001 agglayer aggkit-prover-001
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
