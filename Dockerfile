# syntax=docker/dockerfile:1
# Multi-arch capable multi-stage build for XrayR (XBoard-ready).
# Example:
#   docker buildx build --platform linux/amd64,linux/arm64 -t xrayr-xboard:latest .

ARG GO_VERSION=1.24

# ---------- Build stage ----------
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

WORKDIR /src

RUN apk add --no-cache ca-certificates git tzdata

# Cache module downloads first for faster rebuilds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0
RUN set -eux; \
    export GOOS="${TARGETOS:-linux}"; \
    export GOARCH="${TARGETARCH:-amd64}"; \
    if [ "${GOARCH}" = "arm" ] && [ -n "${TARGETVARIANT}" ]; then \
      export GOARM="${TARGETVARIANT#v}"; \
    fi; \
    go build -trimpath -ldflags="-s -w -buildid=" -o /out/XrayR .

# ---------- Runtime stage ----------
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo "Asia/Shanghai" > /etc/timezone \
    && mkdir -p /etc/XrayR

COPY --from=builder /out/XrayR /usr/local/bin/XrayR
COPY release/config/config.yml.example /etc/XrayR/config.yml.example

# Mount your production config at /etc/XrayR/config.yml
VOLUME ["/etc/XrayR"]

ENTRYPOINT ["XrayR", "--config", "/etc/XrayR/config.yml"]
