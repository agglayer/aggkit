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
build: build-aggkit build-tools

.PHONY: build-aggkit
build-aggkit: ## Builds aggkit binary
	GIN_MODE=release $(GOENVVARS) go build -ldflags "all=$(LDFLAGS)" -o $(GOBIN)/$(GOBINARY) $(GOCMD)

.PHONY: build-tools
build-tools: $(GOBIN)/aggsender_find_imported_bridge $(GOBIN)/remove_ger ## Builds the tools

$(GOBIN)/aggsender_find_imported_bridge: ## Build aggsender_find_imported_bridge tool
	$(GOENVVARS) go build -o $(GOBIN)/aggsender_find_imported_bridge ./tools/aggsender_find_imported_bridge

$(GOBIN)/remove_ger: ## Build remove_ger tool
	$(GOENVVARS) go build -ldflags "all=$(LDFLAGS)" -o $(GOBIN)/remove_ger ./tools/remove_ger/cmd

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
test-unit: ## Runs the short unit tests
	trap '$(STOP)' EXIT; MallocNanoZone=0 go test -count=1 -short -race -p 1 -covermode=atomic -coverprofile=coverage.out -timeout 15m ./...

.PHONY: test-unit-all
test-unit-all: ## Runs all unit tests
	trap '$(STOP)' EXIT; MallocNanoZone=0 go test -count=1 -race -p 1 -covermode=atomic -coverprofile=coverage.out -timeout 15m ./...

.PHONY: test-e2e
test-e2e: ## Runs the e2e tests
	go test -v -timeout 30m ./test/e2e/...

.PHONY: lint
lint: ## Runs the linter
	export "GOROOT=$$(go env GOROOT)" && $$(go env GOPATH)/bin/golangci-lint run --timeout 5m

.PHONY: generate-swagger-docs
generate-swagger-docs: ## Generates the swagger docs
	@echo "Generating swagger docs"
	@$(SWAG) init -g bridgeservice/bridge.go -o bridgeservice/docs --exclude autoclaim/api
	@$(SWAG) init -g api.go -d autoclaim/api,autoclaim/apitypes -o autoclaim/api/docs --instanceName autoclaim
	@mkdir -p docs/assets/swagger/bridge_service
	@cp bridgeservice/docs/swagger.json docs/assets/swagger/bridge_service/swagger.json
	@echo "Copied swagger.json to docs/assets/swagger/bridge_service/"
	@mkdir -p docs/assets/swagger/autoclaim
	@cp autoclaim/api/docs/autoclaim_swagger.json docs/assets/swagger/autoclaim/swagger.json
	@echo "Copied autoclaim_swagger.json to docs/assets/swagger/autoclaim/"

.PHONY: vulncheck
vulncheck: ## Runs the vulnerability checker tool
	@command -v govulncheck >/dev/null 2>&1 || { \
		echo "govulncheck is not installed. Please run: go install golang.org/x/vuln/cmd/govulncheck@latest"; \
		exit 1; \
	}
	@echo "Running govulncheck on all packages..."
	@go list ./... | xargs -n1 govulncheck

.PHONY: generate-mocks
generate-mocks: ## Generates the mocks using mockery
	@cd test && $(MAKE) generate-mocks

## Help display.
## Pulls comments from beside commands and prints a nicely formatted
## display with the commands and their usage information.
.DEFAULT_GOAL := help

.PHONY: help
help: ## Prints this help
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
	| sort \
	| awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
