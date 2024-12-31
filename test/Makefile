.PHONY: generate-mocks
generate-mocks:	generate-mocks-bridgesync generate-mocks-reorgdetector \
	generate-mocks-da \
	generate-mocks-l1infotreesync generate-mocks-helpers \
	generate-mocks-sync \
	generate-mocks-aggsender generate-mocks-agglayer

COMMON_MOCKERY_PARAMS=--disable-version-string --with-expecter --exported

.PHONY: generate-mocks-bridgesync
generate-mocks-bridgesync: ## Generates mocks for bridgesync, using mockery tool
	export "GOROOT=$$(go env GOROOT)" && $$(go env GOPATH)/bin/mockery --all --case snake --dir ../bridgesync --output ../bridgesync/mocks --outpkg mocks_bridgesync ${COMMON_MOCKERY_PARAMS}
	
.PHONY: generate-mocks-reorgdetector
generate-mocks-reorgdetector: ## Generates mocks for reorgdetector, using mockery tool
	export "GOROOT=$$(go env GOROOT)" && $$(go env GOPATH)/bin/mockery --name=EthClient --dir=../reorgdetector --output=../reorgdetector --outpkg=reorgdetector --inpackage --structname=EthClientMock --filename=mock_eth_client.go ${COMMON_MOCKERY_PARAMS}

.PHONY: generate-mocks-rpc
generate-mocks-rpc: ## Generates mocks for rpc, using mockery tool
	export "GOROOT=$$(go env GOROOT)" && $$(go env GOPATH)/bin/mockery --all --case snake --dir ../rpc --output ../rpc/mocks --outpkg mocks ${COMMON_MOCKERY_PARAMS}

.PHONY: generate-mocks-l1infotreesync
generate-mocks-l1infotreesync: ## Generates mocks for l1infotreesync, using mockery tool
	export "GOROOT=$$(go env GOROOT)" && $$(go env GOPATH)/bin/mockery --all --case snake --dir ../l1infotreesync --output ../l1infotreesync/mocks --outpkg mocks_l1infotreesync ${COMMON_MOCKERY_PARAMS}
	export "GOROOT=$$(go env GOROOT)" && $$(go env GOPATH)/bin/mockery --name=ReorgDetector --dir=../sync --output=../l1infotreesync/mocks --outpkg=mocks_l1infotreesync --structname=ReorgDetectorMock --filename=mock_reorgdetector.go ${COMMON_MOCKERY_PARAMS}
	
.PHONY: generate-mocks-helpers
generate-mocks-helpers: ## Generates mocks for helpers, using mockery tool
	export "GOROOT=$$(go env GOROOT)" && $$(go env GOPATH)/bin/mockery --name=EthTxManager --dir=../aggoracle/chaingersender --output=./helpers --outpkg=helpers --structname=EthTxManagerMock --filename=mock_ethtxmanager.go ${COMMON_MOCKERY_PARAMS}

.PHONY: generate-mocks-aggoracle
generate-mocks-aggoracle: ## Generates mocks for aggoracle, using mockery tool
	export "GOROOT=$$(go env GOROOT)" && $$(go env GOPATH)/bin/mockery --name=EthTxManager --dir ../aggoracle/chaingersender --output ../aggoracle/mocks --outpkg mocks --structname=EthTxManagerMock --filename=mock_ethtxmanager.go ${COMMON_MOCKERY_PARAMS}
	export "GOROOT=$$(go env GOROOT)" && $$(go env GOPATH)/bin/mockery --name=L2GERManager --dir ../aggoracle/chaingersender --output ../aggoracle/mocks --outpkg mocks --structname=L2GERManagerMock --filename=mock_l2germanager.go ${COMMON_MOCKERY_PARAMS}

.PHONY: generate-mocks-sync
generate-mocks-sync: ## Generates mocks for sync, using mockery tool
	export "GOROOT=$$(go env GOROOT)" && $$(go env GOPATH)/bin/mockery --name=EthClienter --dir=../sync --output=../sync --outpkg=sync --inpackage --structname=L2Mock --filename=mock_l2_test.go ${COMMON_MOCKERY_PARAMS}
	export "GOROOT=$$(go env GOROOT)" && $$(go env GOPATH)/bin/mockery --name=evmDownloaderFull --dir=../sync --output=../sync --outpkg=sync --inpackage --structname=EVMDownloaderMock --filename=mock_downloader_test.go ${COMMON_MOCKERY_PARAMS}
	export "GOROOT=$$(go env GOROOT)" && $$(go env GOPATH)/bin/mockery --name=processorInterface --dir=../sync --output=../sync --outpkg=sync --inpackage --structname=ProcessorMock --filename=mock_processor_test.go ${COMMON_MOCKERY_PARAMS}
	export "GOROOT=$$(go env GOROOT)" && $$(go env GOPATH)/bin/mockery --name=ReorgDetector --dir=../sync --output=../sync --outpkg=sync --inpackage --structname=ReorgDetectorMock --filename=mock_reorgdetector_test.go ${COMMON_MOCKERY_PARAMS}
	
.PHONY: generate-mocks-aggsender
generate-mocks-aggsender: ## Generates mocks for aggsender, using mockery tool
	export "GOROOT=$$(go env GOROOT)" && $$(go env GOPATH)/bin/mockery --all --case snake --dir ../aggsender --output ../aggsender/mocks --outpkg mocks ${COMMON_MOCKERY_PARAMS}

.PHONY: generate-mocks-agglayer
generate-mocks-agglayer: ## Generates mocks for agglayer, using mockery tool
	export "GOROOT=$$(go env GOROOT)" && $$(go env GOPATH)/bin/mockery --name=AgglayerClientInterface --dir=../agglayer --output=../agglayer --outpkg=agglayer --inpackage --structname=AgglayerClientMock --filename=mock_agglayer_client.go ${COMMON_MOCKERY_PARAMS}	

.PHONY: test-e2e-fork12-pessimistic
test-e2e-fork12-pessimistic: stop
	./run-e2e.sh fork12 pessimistic
	export BATS_TEST_TIMEOUT=1800
	bats bats/pp/bridge-e2e.bats bats/pp/e2e-pp.bats

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
