BINARY := gq

# Stamp the same build metadata goreleaser sets on releases, so `gq version`
# tells a local build apart from a release.
VERSION ?= $(patsubst v%,%,$(shell git describe --tags --always --dirty 2>/dev/null || echo dev))
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# The golangci-lint version .github/workflows/ci.yml pins; keep them in step.
# It runs on the Go version of go.mod, as in CI: an older golangci-lint cannot
# read packages built by a newer Go.
GOLANGCI_LINT_VERSION ?= v2.12.2
GO_VERSION := $(shell awk '/^go /{print $$2}' go.mod)

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_.-]+:.*?## ' $(MAKEFILE_LIST) \
	  | sort \
	  | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the binary to ./bin/gq
	go build -ldflags "$(LDFLAGS)" -o ./bin/$(BINARY) ./cmd/gq

.PHONY: install
install: ## Install gq into $GOBIN (default ~/go/bin)
	go install -ldflags "$(LDFLAGS)" ./cmd/gq

.PHONY: test
test: ## Run unit tests with the race detector and coverage, like CI
	go test -race -covermode=atomic -coverprofile=coverage.out ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint at the version CI uses
	GOTOOLCHAIN=go$(GO_VERSION) go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run

.PHONY: check
check: vet test lint ## Pre-PR check: vet, test, lint

.PHONY: tidy
tidy: ## Run go mod tidy
	go mod tidy

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf ./bin coverage.out
