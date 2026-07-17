# --- Stage 1: Build coordd ---
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Stamp the version to match the Makefile's LDFLAGS — a bare `go build` would leave it at the "dev"
# default. Pass with: docker build --build-arg VERSION=$(git describe --tags --always --dirty)
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-X github.com/ny4rl4th0t3p/seedward-chaincoord/cmd/coordd/cmd.Version=${VERSION}" -o /bin/coordd ./cmd/coordd

# --- Stage 2: Runtime image ---
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /bin/coordd /usr/local/bin/coordd

# Run as a non-root user. coordd writes its sqlite DB / files / audit log at the configured
# COORD_DB_PATH / COORD_FILES_PATH / COORD_AUDIT_LOG_PATH — mount those to a path this uid owns.
RUN adduser -D -u 1000 coordd
USER coordd

# coordd serves on COORD_LISTEN_ADDR (default :8080).
EXPOSE 8080

# Liveness: probe the deep /healthz (DB SELECT 1 + audit-writability → 503 on failure), not just process
# up. busybox wget is in the alpine base, so no extra package. Hits the in-container port (8080).
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --spider -q http://127.0.0.1:8080/healthz || exit 1
# Default to `serve`; override with e.g. `docker run <image> migrate` (the e2e chains migrate+serve).
ENTRYPOINT ["coordd"]
CMD ["serve"]