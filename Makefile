BINARY_SERVER       := bin/coordd
BINARY_SMOKE_SIGNER := bin/smoke-signer
MODULE              := github.com/ny4rl4th0t3p/seedward-chaincoord
VERSION             ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS             := -X $(MODULE)/cmd/coordd/cmd.Version=$(VERSION)


.PHONY: build build-server build-smoke-signer test test-integration test-e2e lint swagger swagger-check lint-openapi release clean docker-build test-smoke test-down-smoke test-secrets-smoke dev-build dev-up dev-down

build: build-server build-smoke-signer

build-server:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY_SERVER) ./cmd/coordd

build-smoke-signer:
	go build -o $(BINARY_SMOKE_SIGNER) ./cmd/smoke-signer

test:
	go test ./... --count=1

test-integration:
	go test -tags integration -count=1 ./internal/infrastructure/...

test-e2e:
	go test -tags e2e -count=1 ./internal/e2e/...

lint:
	go vet ./...
	golangci-lint run --fix

# swag and vacuum are pinned as go.mod tool dependencies (the `tool` directives),
# so `go tool` runs the exact same version on every machine and in CI — keeping
# generated output deterministic. Add/update with:
#   go get -tool github.com/swaggo/swag/cmd/swag@<version>
#   go get -tool github.com/daveshanley/vacuum@<version>
swagger:
	go tool swag init --generalInfo cmd/coordd/main.go --dir . --output docs/mkdocs/api/ --outputTypes yaml --parseInternal

# CI guard against spec drift: regenerate and fail if the result differs from
# what's committed, so the Go annotations and docs/mkdocs/api/swagger.yaml can't
# fall out of sync. Fix a failure by running `make swagger` and committing.
# Deterministic because swag is pinned via the go.mod tool directive (see above).
swagger-check: swagger
	@git diff --exit-code HEAD -- docs/mkdocs/api/swagger.yaml \
		|| { echo "ERROR: docs/mkdocs/api/swagger.yaml is out of date — run 'make swagger' and commit the result." >&2; exit 1; }

lint-openapi: swagger
	go tool vacuum lint docs/mkdocs/api/swagger.yaml

release:
	GOOS=linux  GOARCH=amd64  go build -ldflags "$(LDFLAGS)" -o bin/coordd-linux-amd64  ./cmd/coordd
	GOOS=linux  GOARCH=arm64  go build -ldflags "$(LDFLAGS)" -o bin/coordd-linux-arm64  ./cmd/coordd
	GOOS=darwin GOARCH=arm64  go build -ldflags "$(LDFLAGS)" -o bin/coordd-darwin-arm64 ./cmd/coordd
	GOOS=darwin GOARCH=amd64  go build -ldflags "$(LDFLAGS)" -o bin/coordd-darwin-amd64 ./cmd/coordd

clean:
	rm -rf bin/

docker-build:
	docker compose -f docker/docker-compose.yml build

test-secrets-smoke: build-server
	@mkdir -p docker/secrets
	@[ -f docker/secrets/audit_key ] || ($(BINARY_SERVER) keygen > docker/secrets/audit_key && chmod 600 docker/secrets/audit_key)
	@[ -f docker/secrets/jwt_key ]   || ($(BINARY_SERVER) keygen > docker/secrets/jwt_key   && chmod 600 docker/secrets/jwt_key)

test-smoke: test-down-smoke test-secrets-smoke
	docker compose -f docker/docker-compose.smoke.yml up --build \
	    --abort-on-container-exit \
	    --exit-code-from smoke-test
	docker compose -f docker/docker-compose.smoke.yml down -v

test-down-smoke:
	docker compose -f docker/docker-compose.smoke.yml down --volumes

# ── Dev environment (coordd) ──────────────────────────────────────────────────

dev-build:
	docker compose -f docker/docker-compose.yml build

dev-up:
	docker compose -f docker/docker-compose.yml up --build

dev-down:
	docker compose -f docker/docker-compose.yml down --volumes
