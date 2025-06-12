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
	@which protoc > /dev/null || (echo "Error: Protoc is not installed" && exit 1)

# Check for Curl
.PHONY: check-curl
check-curl: ## Check if curl is installed
	@which curl > /dev/null || (echo "Error: curl is not installed" && exit 1)

# Check for Golangci-lint
.PHONY: check-golangci-lint
check-golangci-lint:
	@which golangci-lint > /dev/null || (echo "Error: golangci-lint is not installed" && exit 1)

# Check for Swag
.PHONY: check-swag
check-swag:
	@command -v swag >/dev/null 2>&1 || { \
		echo >&2 "swag not installed. Please install it: https://github.com/swaggo/swag"; \
		exit 1; \
	}

# Targets that require the checks
build: check-go
lint: check-go check-golangci-lint
build-docker: check-docker
build-docker-nc: check-docker
generate-swagger-docs: check-swag

.PHONY: build ## Builds the binaries locally into ./target
build: build-aggkit build-tools

.PHONY: build-aggkit
build-aggkit: ## Builds aggkit binary
	GIN_MODE=release $(GOENVVARS) go build -ldflags "all=$(LDFLAGS)" -o $(GOBIN)/$(GOBINARY) $(GOCMD)

.PHONY: build-tools
build-tools: ## Builds the tools
	$(GOENVVARS) go build -o $(GOBIN)/aggsender_find_imported_bridge ./tools/aggsender_find_imported_bridge

.PHONY: build-docker
build-docker: ## Builds a docker image with the aggkit binary
	docker build -t aggkit:local -f ./Dockerfile .

.PHONY: build-docker-nc
build-docker-nc: ## Builds a docker image with the aggkit binary - but without build cache
	docker build --no-cache=true -t aggkit -f ./Dockerfile .

.PHONY: test-unit
test-unit: ## Runs the unit tests
	trap '$(STOP)' EXIT; MallocNanoZone=0 go test -count=1 -short -race -p 1 -covermode=atomic -coverprofile=coverage.out  -coverpkg ./... -timeout 15m ./...

.PHONY: lint
lint: ## Runs the linter
	export "GOROOT=$$(go env GOROOT)" && $$(go env GOPATH)/bin/golangci-lint run --timeout 5m

.PHONY: generate-swagger-docs
generate-swagger-docs: ## Generates the swagger docs
	@echo "Generating swagger docs"
	@swag init -g bridgeservice/bridge.go -o bridgeservice/docs
	@mkdir -p docs/assets/swagger/bridge_service
	@cp bridgeservice/docs/swagger.json docs/assets/swagger/bridge_service/swagger.json
	@echo "Copied swagger.json to docs/assets/swagger/bridge_service/"

.PHONY: vulncheck
vulncheck: ## Runs the vulnerability checker tool
	@command -v govulncheck >/dev/null 2>&1 || { \
		echo "govulncheck is not installed. Please run: go install golang.org/x/vuln/cmd/govulncheck@latest"; \
		exit 1; \
	}
	@echo "Running govulncheck on all packages..."
	@go list ./... | xargs -n1 govulncheck

## Help display.
## Pulls comments from beside commands and prints a nicely formatted
## display with the commands and their usage information.
.DEFAULT_GOAL := help

.PHONY: help
help: ## Prints this help
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
	| sort \
	| awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
