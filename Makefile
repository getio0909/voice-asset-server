.PHONY: dev test lint build migrate worker adminctl contract clean

GO ?= go
OPENAPI_CLI_VERSION ?= 2.39.0

dev:
	$(GO) run ./cmd/api

test:
	$(GO) test ./...

lint:
	$(GO) vet ./...

build:
	$(GO) build ./cmd/...

migrate:
	$(GO) run ./cmd/migrate

worker:
	$(GO) run ./cmd/worker

adminctl:
	$(GO) run ./cmd/adminctl -- capabilities

contract:
	npx --yes @redocly/cli@$(OPENAPI_CLI_VERSION) lint contracts/openapi.yaml

clean:
	$(GO) clean
