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