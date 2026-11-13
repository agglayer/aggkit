include version.mk

ARCH := $(shell arch)

ifeq ($(ARCH),x86_64)
	ARCH = amd64
else
	ifeq ($(ARCH),aarch64)
		ARCH = arm64
	endif
endif
GOBASE := $(shell pwd)
GOBIN := $(GOBASE)/target
GOENVVARS := GOBIN=$(GOBIN) CGO_ENABLED=1 GOARCH=$(ARCH)
GOBINARY := aggkit
GOCMD := $(GOBASE)/cmd
SWAG_VERSION ?= v1.16.6
SWAG := $(shell command -v swag 2>/dev/null || echo "$$(go env GOPATH)/bin/swag")

LDFLAGS += -X 'github.com/agglayer/aggkit.Version=$(VERSION)'
LDFLAGS += -X 'github.com/agglayer/aggkit.GitRev=$(GITREV)'
LDFLAGS += -X 'github.com/agglayer/aggkit.GitBranch=$(GITBRANCH)'
LDFLAGS += -X 'github.com/agglayer/aggkit.BuildDate=$(DATE)'

# ger_validator_signer is its own Go module (and its own docker image): it doesn't share the dependencies of aggkit,
# and `go ./...` from here doesn't see it, so it has its own targets
GER_VALIDATOR_SIGNER_DIR := tools/ger_validator_signer
GER_VALIDATOR_SIGNER_IMAGE ?= ger_validator_signer:local
GER_VALIDATOR_SIGNER_DEBUG_IMAGE ?= ger_validator_signer:local-debug
GER_VALIDATOR_SIGNER_PKG := github.com/agglayer/aggkit/tools/ger_validator_signer
GER_VALIDATOR_SIGNER_LDFLAGS += -X '$(GER_VALIDATOR_SIGNER_PKG)/version.Version=$(VERSION)'
GER_VALIDATOR_SIGNER_LDFLAGS += -X '$(GER_VALIDATOR_SIGNER_PKG)/version.GitRev=$(GITREV)'
GER_VALIDATOR_SIGNER_LDFLAGS += -X '$(GER_VALIDATOR_SIGNER_PKG)/version.GitBranch=$(GITBRANCH)'
GER_VALIDATOR_SIGNER_LDFLAGS += -X '$(GER_VALIDATOR_SIGNER_PKG)/version.BuildDate=$(DATE)'

# Check dependencies
.PHONY: check-go
check-go: ## Check if golang is installed
	@which go > /dev/null || (echo "Error: Go is not installed" && exit 1)

# Check for Docker
.PHONY: check-docker
check-docker: ## Check if docker is installed
	@which docker > /dev/null || (echo "Error: docker is not installed" && exit 1)

# Check for Protoc
.PHONY: check-protoc
check-protoc: ## Check if protoc is installed
	@which protoc > /dev/null || (echo "Error: protoc is not installed" && exit 1) || (echo "Please install protoc from https://grpc.io/docs/protoc-installation/" && exit 1)
	@which protoc-gen-go > /dev/null || (echo "Error: protoc-gen-go is not installed. Run: go install google.golang.org/protobuf/cmd/protoc-gen-go@latest" && exit 1)
	@which protoc-gen-go-grpc > /dev/null || (echo "Error: protoc-gen-go-grpc is not installed. Run: go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest" && exit 1)
	@which buf > /dev/null || (echo "Error: buf is not installed. Please install it from https://docs.buf.build/installation" && exit 1)

# Check for Curl
.PHONY: check-curl
check-curl: ## Check if curl is installed
	@which curl > /dev/null || (echo "Error: curl is not installed" && exit 1)

# Check for Golangci-lint
.PHONY: check-golangci-lint
check-golangci-lint:
	@which golangci-lint > /dev/null || (echo "Error: golangci-lint is not installed. Please install it: https://github.com/golangci/golangci-lint" && exit 1)

# Check for Swag
.PHONY: check-swag
check-swag:
	@test -x "$(SWAG)" || { \
		echo >&2 "Error: swag not installed. Run: make install-swag"; \
		exit 1; \
	}

.PHONY: install-swag
install-swag: check-go ## Install swag for swagger generation
	GOBIN=$$(go env GOPATH)/bin go install github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION)

# Targets that require the checks
build: check-go
lint: check-go check-golangci-lint
build-docker: check-docker
build-docker-nc: check-docker
generate-swagger-docs: check-swag
generate-code-from-proto: check-protoc

.PHONY: generate-code-from-proto
generate-code-from-proto: check-protoc ## Generate Go code from proto files in-place
	buf dep update
	buf generate

.PHONY: build ## Builds the binaries locally into ./target
build: build-aggkit build-aggkit-proxy build-tools build-ger_validator_signer

.PHONY: build-aggkit
build-aggkit: ## Builds aggkit binary
	GIN_MODE=release $(GOENVVARS) go build -ldflags "all=$(LDFLAGS)" -o $(GOBIN)/$(GOBINARY) $(GOCMD)

.PHONY: build-aggkit-proxy
build-aggkit-proxy: ## Builds aggkit-proxy binary
	$(GOENVVARS) go build -ldflags "all=$(LDFLAGS)" -o $(GOBIN)/aggkit-proxy ./proxy/cmd

ARGS ?= run
.PHONY: run-proxy
run-proxy: build-aggkit-proxy ## Runs aggkit-proxy (pass args with ARGS, e.g. make run-proxy ARGS="run --cfg proxy.toml")
	$(GOBIN)/aggkit-proxy $(ARGS)

.PHONY: build-tools
build-tools: $(GOBIN)/aggsender_find_imported_bridge $(GOBIN)/remove_ger $(GOBIN)/exit_certificate $(GOBIN)/exit_certificate_claimer $(GOBIN)/force_ger_update $(GOBIN)/config_render ## Builds the tools


.PHONY: build-aggsender_find_imported_bridge
build-aggsender_find_imported_bridge: $(GOBIN)/aggsender_find_imported_bridge ## Build aggsender_find_imported_bridge tool

.PHONY: build-remove_ger
build-remove_ger: $(GOBIN)/remove_ger ## Build remove_ger tool

.PHONY: build-exit_certificate
build-exit_certificate: $(GOBIN)/exit_certificate ## Build exit_certificate tool

.PHONY: build-exit_certificate_claimer
build-exit_certificate_claimer: $(GOBIN)/exit_certificate_claimer ## Build exit_certificate_claimer backend tool

.PHONY: build-force_ger_update
build-force_ger_update: $(GOBIN)/force_ger_update ## Build force_ger_update tool

.PHONY: build-config_render
build-config_render: $(GOBIN)/config_render ## Build config_render tool

.PHONY: $(GOBIN)/aggsender_find_imported_bridge
$(GOBIN)/aggsender_find_imported_bridge:
	$(GOENVVARS) go build -o $(GOBIN)/aggsender_find_imported_bridge ./tools/aggsender_find_imported_bridge


.PHONY: $(GOBIN)/remove_ger
$(GOBIN)/remove_ger:
	$(GOENVVARS) go build -ldflags "all=$(LDFLAGS)" -o $(GOBIN)/remove_ger ./tools/remove_ger/cmd

.PHONY: $(GOBIN)/exit_certificate
$(GOBIN)/exit_certificate:
	$(GOENVVARS) go build -ldflags "all=$(LDFLAGS)" -o $(GOBIN)/exit_certificate ./tools/exit_certificate/cmd

.PHONY: $(GOBIN)/exit_certificate_claimer
$(GOBIN)/exit_certificate_claimer:
	$(GOENVVARS) go build -ldflags "all=$(LDFLAGS)" -o $(GOBIN)/exit_certificate_claimer ./tools/exit_certificate_claimer/service/cmd

.PHONY: $(GOBIN)/force_ger_update
$(GOBIN)/force_ger_update:
	$(GOENVVARS) go build -ldflags "all=$(LDFLAGS)" -o $(GOBIN)/force_ger_update ./tools/force_ger_update/cmd

.PHONY: $(GOBIN)/config_render
$(GOBIN)/config_render:
	$(GOENVVARS) go build -ldflags "all=$(LDFLAGS)" -o $(GOBIN)/config_render ./tools/config_render/cmd

.PHONY: build-ger_validator_signer
build-ger_validator_signer: check-go ## Builds the ger_validator_signer binary (its own Go module)
	cd $(GER_VALIDATOR_SIGNER_DIR) && $(GOENVVARS) go build -trimpath \
		-ldflags "all=$(GER_VALIDATOR_SIGNER_LDFLAGS)" -o $(GOBIN)/ger_validator_signer ./cmd

GER_VALIDATOR_SIGNER_DOCKER_ARGS = --build-arg VERSION=$(VERSION) --build-arg GIT_REV=$(GITREV) \
	--build-arg GIT_BRANCH=$(GITBRANCH) --build-arg BUILD_DATE="$(DATE)"

.PHONY: build-docker-ger_validator_signer
build-docker-ger_validator_signer: check-docker ## Builds the docker image of ger_validator_signer (no shell)
	docker build $(GER_VALIDATOR_SIGNER_DOCKER_ARGS) -t $(GER_VALIDATOR_SIGNER_IMAGE) \
		-f $(GER_VALIDATOR_SIGNER_DIR)/Dockerfile $(GER_VALIDATOR_SIGNER_DIR)

.PHONY: build-docker-ci-ger_validator_signer
build-docker-ci-ger_validator_signer: check-docker ## Builds the docker image of ger_validator_signer with a shell (CI, dev)
	docker build $(GER_VALIDATOR_SIGNER_DOCKER_ARGS) --build-arg INCLUDE_SHELL=true -t $(GER_VALIDATOR_SIGNER_IMAGE) \
		-f $(GER_VALIDATOR_SIGNER_DIR)/Dockerfile $(GER_VALIDATOR_SIGNER_DIR)

.PHONY: build-docker-debug-ger_validator_signer
build-docker-debug-ger_validator_signer: check-docker ## Builds a debug image of ger_validator_signer (dlv headless on :40000, no optimizations)
	docker build $(GER_VALIDATOR_SIGNER_DOCKER_ARGS) -t $(GER_VALIDATOR_SIGNER_DEBUG_IMAGE) \
		-f $(GER_VALIDATOR_SIGNER_DIR)/Dockerfile.debug $(GER_VALIDATOR_SIGNER_DIR)

.PHONY: build-docker
build-docker: ## Builds a docker image with the aggkit binary
	docker build -t aggkit:local -t aggkit:local-no_shell -f ./Dockerfile .

.PHONY: build-docker-ci
build-docker-ci: ## Builds a docker image with the aggkit binary for CI (includes shell)
	docker build --build-arg INCLUDE_SHELL=true -t aggkit:local -t aggkit:local-dev -f ./Dockerfile .

.PHONY: build-docker-nc
build-docker-nc: ## Builds a docker image with the aggkit binary - but without build cache
	docker build --no-cache=true -t aggkit:local -t aggkit:local-no_shell -f ./Dockerfile .

.PHONY: build-docker-debug
build-docker-debug: ## Builds a debug docker image (dlv headless on :40000, no optimizations)
	docker build -t aggkit:local-debug -f ./Dockerfile.debug .

.PHONY: test-unit
test-unit: ## Runs the unit tests
	trap '$(STOP)' EXIT; MallocNanoZone=0 go test -count=1 -short -race -p 1 -covermode=atomic -coverprofile=coverage.out -timeout 15m ./...

.PHONY: test-unit-ger_validator_signer
test-unit-ger_validator_signer: check-go ## Runs the unit tests of ger_validator_signer
	cd $(GER_VALIDATOR_SIGNER_DIR) && MallocNanoZone=0 go test -count=1 -short -race -p 1 -covermode=atomic \
		-coverprofile=coverage.out -timeout 15m ./...

TEST_RUN ?=
.PHONY: test-e2e
test-e2e: ## Runs the e2e tests
	# Keep this aligned with test-go-e2e.yml's job timeout; remove-GER scenarios have long retry budgets.
	go test -v -timeout 60m $(if $(TEST_RUN),-run "$(TEST_RUN)") ./test/e2e/...

.PHONY: test-e2e-force_ger_update
test-e2e-force_ger_update: ## Runs the isolated force_ger_update e2e test (dedicated CI job/runner only)
	AGGKIT_E2E_ENV=anvil-2chains RUN_FORCE_GER_UPDATE_E2E=true E2E_SKIP_POSTTEST_BRIDGE_CHECK=true \
		go test -v -timeout 30m -run TestForceGERUpdateE2E ./test/e2e/...

.PHONY: lint
lint: ## Runs the linter
	export "GOROOT=$$(go env GOROOT)" && $$(go env GOPATH)/bin/golangci-lint run --timeout 5m

.PHONY: lint-ger_validator_signer
lint-ger_validator_signer: check-go check-golangci-lint ## Runs the linter on ger_validator_signer
	cd $(GER_VALIDATOR_SIGNER_DIR) && export "GOROOT=$$(go env GOROOT)" && \
		$$(go env GOPATH)/bin/golangci-lint run --config ../../.golangci.yml --timeout 5m

.PHONY: generate-swagger-docs-ger_validator_signer
generate-swagger-docs-ger_validator_signer: check-swag ## Generates the swagger docs of ger_validator_signer
	@echo "Generating swagger docs of ger_validator_signer"
	@cd $(GER_VALIDATOR_SIGNER_DIR) && $(SWAG) init -g server.go -d api -o api/docs --outputTypes json,yaml --parseDependency=false
	@mkdir -p docs/assets/swagger/ger_validator_signer
	@cp $(GER_VALIDATOR_SIGNER_DIR)/api/docs/swagger.json docs/assets/swagger/ger_validator_signer/swagger.json
	@echo "Copied swagger.json to docs/assets/swagger/ger_validator_signer/"

.PHONY: generate-swagger-docs
generate-swagger-docs: ## Generates the swagger docs
	@echo "Generating swagger docs"
	@$(SWAG) init -g bridgeservice/bridge.go -o bridgeservice/docs --exclude bridgetracker
	@$(SWAG) init -g api.go -d bridgetracker/api,bridgetracker/types -o bridgetracker/api/docs --parseDependency --instanceName bridgetracker
	@mkdir -p docs/assets/swagger/bridge_service
	@cp bridgeservice/docs/swagger.json docs/assets/swagger/bridge_service/swagger.json
	@echo "Copied swagger.json to docs/assets/swagger/bridge_service/"
	@mkdir -p docs/assets/swagger/bridge_tracker
	@cp bridgetracker/api/docs/bridgetracker_swagger.json docs/assets/swagger/bridge_tracker/swagger.json
	@echo "Copied bridgetracker_swagger.json to docs/assets/swagger/bridge_tracker/"

.PHONY: vulncheck
vulncheck: ## Runs the vulnerability checker tool
	@command -v govulncheck >/dev/null 2>&1 || { \
		echo "govulncheck is not installed. Please run: go install golang.org/x/vuln/cmd/govulncheck@latest"; \
		exit 1; \
	}
	@echo "Running govulncheck on all packages..."
	@go list ./... | xargs -n1 govulncheck
	@echo "Running govulncheck on ger_validator_signer (its own Go module)..."
	@cd $(GER_VALIDATOR_SIGNER_DIR) && govulncheck ./...

.PHONY: generate-mocks
generate-mocks: ## Generates the mocks using mockery
	@cd test && $(MAKE) generate-mocks

.PHONY: generate-mocks-ger_validator_signer
generate-mocks-ger_validator_signer: ## Generates the mocks of ger_validator_signer using mockery
	@cd $(GER_VALIDATOR_SIGNER_DIR) && mockery --disable-version-string --with-expecter --exported

## Help display.
## Pulls comments from beside commands and prints a nicely formatted
## display with the commands and their usage information.
.DEFAULT_GOAL := help

.PHONY: help
help: ## Prints this help
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
	| sort \
	| awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
