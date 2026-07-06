# ──────────────────────────────────────────────────────────────────────────────
# Variables
# ──────────────────────────────────────────────────────────────────────────────
BINARY_SERVER       := bin/coordd
BINARY_SMOKE_SIGNER := bin/smoke-signer
MODULE              := github.com/ny4rl4th0t3p/seedward-chaincoord
GO                  := go
VERSION             ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS             := -X $(MODULE)/cmd/coordd/cmd.Version=$(VERSION)

.DEFAULT_GOAL := help

# ──────────────────────────────────────────────────────────────────────────────
# Help
# ──────────────────────────────────────────────────────────────────────────────
.PHONY: help
help: ## Show this help message
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*##"}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# ──────────────────────────────────────────────────────────────────────────────
# Build
# ──────────────────────────────────────────────────────────────────────────────
.PHONY: build
build: build-server build-smoke-signer ## Build all binaries

.PHONY: build-server
build-server: ## Build coordd → bin/coordd
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY_SERVER) ./cmd/coordd

.PHONY: build-smoke-signer
build-smoke-signer: ## Build the smoke-signer test utility → bin/smoke-signer
	$(GO) build -o $(BINARY_SMOKE_SIGNER) ./cmd/smoke-signer

.PHONY: install
install: ## Install coordd to $(GOPATH)/bin (or ~/go/bin)
	$(GO) install -ldflags "$(LDFLAGS)" ./cmd/coordd

# ──────────────────────────────────────────────────────────────────────────────
# Test
# ──────────────────────────────────────────────────────────────────────────────
.PHONY: test
test: ## Run unit tests
	$(GO) test -count=1 ./...

.PHONY: test-race
test-race: ## Run unit tests with the race detector
	$(GO) test -race -count=1 ./...

.PHONY: test-integration
test-integration: ## Run integration tests (build tag: integration)
	$(GO) test -tags integration -count=1 ./internal/infrastructure/...

.PHONY: test-e2e
test-e2e: ## Run end-to-end tests (build tag: e2e)
	$(GO) test -tags e2e -count=1 ./internal/e2e/...

.PHONY: cover
cover: ## Run the full suite with cross-package coverage; write coverage.out + coverage-func.txt
	$(GO) test -count=1 -covermode=atomic -coverprofile=coverage.out -coverpkg=./... -tags 'integration e2e' ./...
	$(GO) tool cover -func=coverage.out | tee coverage-func.txt
	@echo "HTML report: $(GO) tool cover -html=coverage.out"

# ──────────────────────────────────────────────────────────────────────────────
# Code quality
# ──────────────────────────────────────────────────────────────────────────────
.PHONY: fmt
fmt: ## Format all Go source files
	$(GO) fmt ./...

.PHONY: fmt-check
fmt-check: ## Check formatting without modifying files (exits non-zero if changes needed)
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "The following files need formatting:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: tidy
tidy: ## Run go mod tidy
	$(GO) mod tidy

.PHONY: tidy-check
tidy-check: ## Check that go.mod and go.sum are tidy (exits non-zero if not)
	$(GO) mod tidy && git diff --exit-code go.mod go.sum

.PHONY: lint
lint: ## Run golangci-lint (report only — used by check/CI)
	golangci-lint cache clean
	golangci-lint run

.PHONY: lint-fix
lint-fix: ## Run golangci-lint and auto-fix what it can
	golangci-lint run --fix

.PHONY: check
check: fmt-check vet tidy-check lint test ## Run all checks (fmt + vet + tidy + lint + unit tests)

# ──────────────────────────────────────────────────────────────────────────────
# OpenAPI / swagger
# swag and vacuum are pinned as go.mod tool dependencies (the `tool` directives),
# so `go tool` runs the exact same version on every machine and in CI. Add/update:
#   go get -tool github.com/swaggo/swag/cmd/swag@<version>
#   go get -tool github.com/daveshanley/vacuum@<version>
# ──────────────────────────────────────────────────────────────────────────────
.PHONY: swagger
swagger: ## Regenerate docs/mkdocs/api/swagger.yaml from Go annotations
	$(GO) tool swag init --generalInfo cmd/coordd/main.go --dir . --output docs/mkdocs/api/ --outputTypes yaml --parseInternal

.PHONY: swagger-check
swagger-check: swagger ## Fail if the committed swagger.yaml is out of date (CI spec-drift guard)
	@git diff --exit-code HEAD -- docs/mkdocs/api/swagger.yaml \
		|| { echo "ERROR: docs/mkdocs/api/swagger.yaml is out of date — run 'make swagger' and commit the result." >&2; exit 1; }

.PHONY: lint-openapi
lint-openapi: swagger ## Lint the OpenAPI spec with vacuum
	$(GO) tool vacuum lint docs/mkdocs/api/swagger.yaml

# ──────────────────────────────────────────────────────────────────────────────
# Docs (mkdocs)
# mkdocs is a Python tool; its deps live in docs/requirements.txt — run `make docs-deps` once.
# The API reference page renders docs/mkdocs/api/swagger.yaml (keep it fresh with `make swagger`).
# ──────────────────────────────────────────────────────────────────────────────
.PHONY: docs-deps
docs-deps: ## Install the Python deps for the docs site (mkdocs + plugins)
	pip install -r docs/requirements.txt

.PHONY: docs-serve
docs-serve: ## Serve the docs site locally with live reload (http://localhost:8000)
	mkdocs serve -f docs/mkdocs.yml

.PHONY: docs-build
docs-build: ## Build the static docs site into site/ (--strict: fail on broken links/nav)
	mkdocs build -f docs/mkdocs.yml --strict

# ──────────────────────────────────────────────────────────────────────────────
# Docker / smoke
# ──────────────────────────────────────────────────────────────────────────────
.PHONY: docker-build
docker-build: ## Build the docker images
	docker compose -f docker/docker-compose.yml build

.PHONY: test-secrets-smoke
test-secrets-smoke: build-server ## Generate audit/jwt keys for the smoke stack (if absent)
	@mkdir -p docker/secrets
	@[ -f docker/secrets/audit_key ] || ($(BINARY_SERVER) keygen > docker/secrets/audit_key && chmod 600 docker/secrets/audit_key)
	@[ -f docker/secrets/jwt_key ]   || ($(BINARY_SERVER) keygen > docker/secrets/jwt_key   && chmod 600 docker/secrets/jwt_key)

.PHONY: test-smoke
test-smoke: test-down-smoke test-secrets-smoke ## Run the dockerized smoke test
	docker compose -f docker/docker-compose.smoke.yml up --build \
	    --abort-on-container-exit \
	    --exit-code-from smoke-test
	docker compose -f docker/docker-compose.smoke.yml down -v

.PHONY: test-down-smoke
test-down-smoke: ## Tear down the smoke stack and its volumes
	docker compose -f docker/docker-compose.smoke.yml down --volumes

# ──────────────────────────────────────────────────────────────────────────────
# Dev environment (coordd)
# ──────────────────────────────────────────────────────────────────────────────
.PHONY: dev-build
dev-build: ## Build the dev docker image
	docker compose -f docker/docker-compose.yml build

.PHONY: dev-up
dev-up: ## Start the dev environment (coordd)
	docker compose -f docker/docker-compose.yml up --build

.PHONY: dev-down
dev-down: ## Stop the dev environment and delete its volumes
	docker compose -f docker/docker-compose.yml down --volumes

# ──────────────────────────────────────────────────────────────────────────────
# Release / clean
# ──────────────────────────────────────────────────────────────────────────────
.PHONY: release
release: ## Cross-compile coordd for linux/darwin × amd64/arm64 → bin/
	GOOS=linux  GOARCH=amd64  $(GO) build -ldflags "$(LDFLAGS)" -o bin/coordd-linux-amd64  ./cmd/coordd
	GOOS=linux  GOARCH=arm64  $(GO) build -ldflags "$(LDFLAGS)" -o bin/coordd-linux-arm64  ./cmd/coordd
	GOOS=darwin GOARCH=arm64  $(GO) build -ldflags "$(LDFLAGS)" -o bin/coordd-darwin-arm64 ./cmd/coordd
	GOOS=darwin GOARCH=amd64  $(GO) build -ldflags "$(LDFLAGS)" -o bin/coordd-darwin-amd64 ./cmd/coordd
