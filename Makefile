LOCALBIN ?= $(shell pwd)/bin

# Code generation targets are defined in Makefile.gen.mk
include Makefile.gen.mk

# Project configuration
PROJECT_NAME ?= llm-d-inference-payload-processor
REGISTRY ?= ghcr.io/llm-d
IMAGE ?= $(REGISTRY)/$(PROJECT_NAME)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
PLATFORMS ?= linux/amd64,linux/arm64

# Container configuration
CONTAINER_RUNTIME ?= docker
TARGETARCH ?= amd64
GIT_COMMIT_SHA ?= $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
BUILD_REF ?= $(VERSION)

# Helm configuration
CHART ?= payload-processor
IMAGE_REPOSITORY ?= $(PROJECT_NAME)

# Go configuration
GOFLAGS ?=
LDFLAGS ?= -s -w -X main.version=$(VERSION)

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
	
.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: tidy
tidy: ## Run go mod tidy
	go mod tidy

##@ Container

.PHONY: image-build
image-build: ## Build container image for local development
	$(CONTAINER_RUNTIME) build \
		--platform linux/$(TARGETARCH) \
		--build-arg COMMIT_SHA=$(GIT_COMMIT_SHA) \
		--build-arg BUILD_REF=$(BUILD_REF) \
		--tag $(IMAGE):$(VERSION) \
		--tag $(IMAGE):latest \
		.

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

##@ Helm

HELM_VERSION ?= v3.17.1
YQ_VERSION ?= v4.45.1
HELM ?= $(LOCALBIN)/helm
YQ ?= $(LOCALBIN)/yq

.PHONY: helm-install
helm-install: $(HELM) ## Download helm locally if necessary.
$(HELM): | $(LOCALBIN)
	$(call go-install-tool,$(HELM),helm.sh/helm/v3/cmd/helm,$(HELM_VERSION))

.PHONY: yq
yq: $(YQ) ## Download yq locally if necessary.
$(YQ): | $(LOCALBIN)
	$(call go-install-tool,$(YQ),github.com/mikefarah/yq/v4,$(YQ_VERSION))

.PHONY: helm-push
helm-push: yq helm-install ## Package and push the payload-processor Helm chart.
	CHART=$(CHART) EXTRA_TAG="$(EXTRA_TAG)" IMAGE_REPOSITORY="$(IMAGE_REPOSITORY)" YQ="$(YQ)" HELM="$(HELM)" ./hack/push-chart.sh

##@ CI Helpers

.PHONY: ci-lint
ci-lint: ## CI: install and run golangci-lint
	@which golangci-lint > /dev/null 2>&1 || go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	golangci-lint run

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin/ coverage.out coverage.html
