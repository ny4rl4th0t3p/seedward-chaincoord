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
# mkdocs is a Python tool. To keep system Python clean, its deps install into a
# repo-local virtualenv (.venv) from docs/requirements.txt. `docs-serve`/`docs-build`
# depend on the venv stamp, so the first run bootstraps it automatically; the stamp
# re-triggers only when docs/requirements.txt changes.
# The API reference page renders docs/mkdocs/api/swagger.yaml (keep it fresh with `make swagger`).
# ──────────────────────────────────────────────────────────────────────────────
PYTHON     ?= python3
VENV       := .venv
VENV_PY    := $(VENV)/bin/python
VENV_STAMP := $(VENV)/.docs-deps.stamp

$(VENV_STAMP): docs/requirements.txt
	$(PYTHON) -m venv $(VENV)
	$(VENV_PY) -m pip install --quiet --upgrade pip
	$(VENV_PY) -m pip install --quiet -r docs/requirements.txt
	@touch $@

.PHONY: docs-deps
docs-deps: $(VENV_STAMP) ## Create .venv and install the docs deps (mkdocs + plugins)

.PHONY: docs-serve
docs-serve: $(VENV_STAMP) ## Serve the docs site locally with live reload (http://localhost:8000)
	$(VENV_PY) -m mkdocs serve -f docs/mkdocs.yml

.PHONY: docs-build
docs-build: $(VENV_STAMP) ## Build the static docs site into docs/site/ (strict mode set in mkdocs.yml)
	$(VENV_PY) -m mkdocs build -f docs/mkdocs.yml

# ──────────────────────────────────────────────────────────────────────────────
# Docker / smoke
# ──────────────────────────────────────────────────────────────────────────────
.PHONY: docker-build
docker-build: ## Build the coordd image locally (same Dockerfile the GHCR image publishes from)
	docker build --build-arg VERSION=$(VERSION) -f docker/Dockerfile -t seedward-chaincoord .

.PHONY: test-secrets-smoke
test-secrets-smoke: ## Generate audit/jwt keys for the smoke stack (if absent) — in a container, no local build
	@mkdir -p docker/secrets
	@[ -f docker/secrets/audit_key ] && [ -f docker/secrets/jwt_key ] || \
	  docker build -q --build-arg VERSION=$(VERSION) -f docker/Dockerfile.smoke -t seedward-chaincoord-smoke . >/dev/null
	@[ -f docker/secrets/audit_key ] || (docker run --rm seedward-chaincoord-smoke coordd keygen > docker/secrets/audit_key && chmod 600 docker/secrets/audit_key)
	@[ -f docker/secrets/jwt_key ]   || (docker run --rm seedward-chaincoord-smoke coordd keygen > docker/secrets/jwt_key   && chmod 600 docker/secrets/jwt_key)

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
# Release / clean
# ──────────────────────────────────────────────────────────────────────────────
.PHONY: release
release: ## Cross-compile coordd for linux/darwin × amd64/arm64 → bin/
	GOOS=linux  GOARCH=amd64  $(GO) build -ldflags "$(LDFLAGS)" -o bin/coordd-linux-amd64  ./cmd/coordd
	GOOS=linux  GOARCH=arm64  $(GO) build -ldflags "$(LDFLAGS)" -o bin/coordd-linux-arm64  ./cmd/coordd
	GOOS=darwin GOARCH=arm64  $(GO) build -ldflags "$(LDFLAGS)" -o bin/coordd-darwin-arm64 ./cmd/coordd
	GOOS=darwin GOARCH=amd64  $(GO) build -ldflags "$(LDFLAGS)" -o bin/coordd-darwin-amd64 ./cmd/coordd
