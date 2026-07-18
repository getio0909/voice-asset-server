# syntax=docker/dockerfile:1.9@sha256:fe40cf4e92cd0c467be2cfc30657a680ae2398318afd50b0c80585784c604f28
FROM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=0.1.0-dev
ARG COMMIT=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w -X github.com/getio0909/voice-asset-server/internal/platform/product.ServerVersion=${VERSION} -X github.com/getio0909/voice-asset-server/internal/platform/product.Commit=${COMMIT}" \
      -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -o /out/migrate ./cmd/migrate && \
    CGO_ENABLED=0 go build -trimpath -o /out/adminctl ./cmd/adminctl && \
    mkdir -p /out/var/objects /out/backups

FROM alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce AS runtime
ARG VERSION=0.1.0-dev
ARG COMMIT=unknown
LABEL org.opencontainers.image.licenses="AGPL-3.0-or-later" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.source="https://github.com/getio0909/voice-asset-server" \
      org.opencontainers.image.title="voiceasset-server" \
      org.opencontainers.image.version="${VERSION}"
RUN apk add --no-cache ca-certificates ffmpeg postgresql17-client
COPY --from=build --chown=65532:65532 /out/ /app/
COPY --chown=65532:65532 migrations/ /app/migrations/
WORKDIR /app
USER 65532:65532
EXPOSE 8080
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD ["/app/api", "-healthcheck=http://127.0.0.1:8080/readyz"]
CMD ["/app/api"]
