# Project configuration
PROJECT_NAME ?= llm-d-inference-payload-processor
REGISTRY ?= ghcr.io/llm-d
IMAGE ?= $(REGISTRY)/$(PROJECT_NAME)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
PLATFORMS ?= linux/amd64,linux/arm64

# Go configuration
GOFLAGS ?=
LDFLAGS ?= -s -w -X main.version=$(VERSION)

# E2E configuration
E2E_IMAGE ?= $(IMAGE):e2e
E2E_MANIFEST_PATH ?= $(CURDIR)/test/testdata/deepseek-model-server.yaml
E2E_USE_KIND ?= true
KIND_CLUSTER_NAME ?= pp-e2e

# Tools
GOLANGCI_LINT_VERSION ?= v2.8.0

.DEFAULT_GOAL := help

##@ General

.PHONY: help
help: ## Show this help message
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: build
build: ## Build the Go binary
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(PROJECT_NAME) ./cmd

.PHONY: test
test: ## Run tests with race detection
	go test -race -count=1 ./...

.PHONY: test-coverage
test-coverage: ## Run tests with coverage report
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -html=coverage.out -o coverage.html

.PHONY: lint
lint: lint-go ## Run all linters

.PHONY: lint-go
lint-go: ## Run Go linter (golangci-lint v2)
	golangci-lint run

.PHONY: fmt
fmt: ## Format Go code
	gofmt -w .

.PHONY: generate
generate: ## Run go generate
	go generate ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: tidy
tidy: ## Run go mod tidy
	go mod tidy

##@ Container

.PHONY: image-build
image-build: ## Build multi-arch container image (local only)
	docker buildx build \
		--platform $(PLATFORMS) \
		--tag $(IMAGE):$(VERSION) \
		--tag $(IMAGE):latest \
		.

.PHONY: image-build-local
image-build-local: ## Build container image for local architecture (used by e2e)
	docker build \
		--tag $(E2E_IMAGE) \
		.

.PHONY: image-kind
image-kind: image-build-local ## Build image and load into Kind cluster
	kind load docker-image $(E2E_IMAGE) --name $(KIND_CLUSTER_NAME)

.PHONY: image-push
image-push: ## Build and push multi-arch container image
	docker buildx build \
		--platform $(PLATFORMS) \
		--push \
		--annotation "index:org.opencontainers.image.source=https://github.com/llm-d/$(PROJECT_NAME)" \
		--annotation "index:org.opencontainers.image.licenses=Apache-2.0" \
		--tag $(IMAGE):$(VERSION) \
		--tag $(IMAGE):latest \
		.

##@ CI Helpers

.PHONY: ci-lint
ci-lint: ## CI: install and run golangci-lint
	@which golangci-lint > /dev/null 2>&1 || go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	golangci-lint run

##@ E2E Testing

.PHONY: test-e2e
test-e2e: ## Run e2e tests (requires Kind or an existing cluster)
	E2E_IMAGE=$(E2E_IMAGE) \
	MANIFEST_PATH=$(E2E_MANIFEST_PATH) \
	USE_KIND=$(E2E_USE_KIND) \
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) \
	./hack/test-e2e.sh

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin/ coverage.out coverage.html
