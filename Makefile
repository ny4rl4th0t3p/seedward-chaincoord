BINARY_SERVER       := bin/coordd
BINARY_SMOKE_SIGNER := bin/smoke-signer
MODULE              := github.com/ny4rl4th0t3p/chaincoord
VERSION             ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS             := -X $(MODULE)/cmd/coordd/cmd.Version=$(VERSION)


.PHONY: build build-server build-smoke-signer build-web test test-integration test-e2e test-jest test-playwright lint lint-web swagger lint-openapi release clean docker-build test-smoke test-down-smoke test-secrets-smoke dev-build dev-up dev-down install-web

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

install-web:
	cd web/app && yarn install --frozen-lockfile

build-web:
	cd web/app && yarn build

lint-web:
	cd web/app && yarn lint

test-jest:
	cd web/app && yarn test

test-playwright: build-server
	cd web/app && yarn playwright test

lint:
	go vet ./...
	golangci-lint run --fix

swagger:
	swag init --generalInfo cmd/coordd/main.go --dir . --output docs/mkdocs/api/ --outputTypes yaml --parseInternal --parseDependency

lint-openapi: swagger
	vacuum lint docs/swagger.yaml

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

# ── Dev environment (coordd + web frontend) ───────────────────────────────────

dev-build:
	docker compose -f docker/docker-compose.yml build

dev-up:
	docker compose -f docker/docker-compose.yml up --build

dev-down:
	docker compose -f docker/docker-compose.yml down --volumes
