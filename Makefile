.PHONY: dev up down test live-asr-test perf-remote perf-data perf-media test-upgrade lint build migrate worker adminctl seed backup backup-verify restore contract compatibility clean

GO ?= go
COMPOSE ?= docker compose
COMPOSE_FILE ?= deploy/compose/compose.yaml
OPENAPI_CLI_VERSION ?= 2.39.0
ADMIN_EMAIL ?= owner@example.com
ADMIN_WORKSPACE ?= Primary Workspace
PERF_BASE_URL ?= https://api.getio.net:10443
PERF_CONTRACT_VERSION ?= 0.13.0
PERF_CA_FILE ?=
FFMPEG_PATH ?= ffmpeg

dev:
	$(COMPOSE) -f $(COMPOSE_FILE) up --build

up:
	$(COMPOSE) -f $(COMPOSE_FILE) up --build --detach

down:
	$(COMPOSE) -f $(COMPOSE_FILE) down --remove-orphans

test:
	$(GO) test ./...

live-asr-test:
	VOICEASSET_LIVE_ASR=1 $(GO) test ./internal/asr -run '^TestLiveProviders$$' -count=1 -v

perf-remote:
	VOICEASSET_REMOTE_PERF=1 \
		VOICEASSET_REMOTE_BASE_URL="$(PERF_BASE_URL)" \
		VOICEASSET_REMOTE_CONTRACT_VERSION="$(PERF_CONTRACT_VERSION)" \
		VOICEASSET_REMOTE_CA_FILE="$(PERF_CA_FILE)" \
		$(GO) test ./tests/performance -run '^TestRemoteReadControlPlane$$' -count=1 -v

perf-data:
	@test -n "$$TEST_DATABASE_URL" || { echo "TEST_DATABASE_URL is required" >&2; exit 2; }
	VOICEASSET_DATA_PERF=1 $(GO) test ./tests/performance \
		-run '^Test(DatabaseAsset|UploadWorkerAudio)Performance$$' -count=1 -v

perf-media:
	VOICEASSET_MEDIA_PERF=1 VOICEASSET_FFMPEG_PATH="$(FFMPEG_PATH)" \
		$(GO) test ./tests/performance -run '^TestFFmpeg(Clip|Waveform)Performance$$' -count=1 -v

test-upgrade:
	@test -n "$$TEST_DATABASE_URL" || { echo "TEST_DATABASE_URL is required" >&2; exit 2; }
	$(GO) test ./internal/platform/migration \
		-run '^Test(UpgradeFromEveryPriorVersion|SequentialUpgradePreservesLegacyData)$$' -count=1 -v

lint:
	$(GO) vet ./...

build:
	$(GO) build ./cmd/...

migrate:
	$(COMPOSE) -f $(COMPOSE_FILE) run --build --rm migrate

worker:
	$(COMPOSE) -f $(COMPOSE_FILE) run --build --rm worker /app/worker --once

adminctl:
	$(GO) run ./cmd/adminctl capabilities

seed:
	@test -n "$$VOICEASSET_ADMIN_PASSWORD" || { echo "VOICEASSET_ADMIN_PASSWORD is required" >&2; exit 2; }
	@printf '%s\n' "$$VOICEASSET_ADMIN_PASSWORD" | $(COMPOSE) -f $(COMPOSE_FILE) run --build --rm -T admin create-admin --email "$(ADMIN_EMAIL)" --workspace "$(ADMIN_WORKSPACE)" --password-stdin

backup:
	@test -n "$(BACKUP_DIR)" || { echo "BACKUP_DIR is required" >&2; exit 2; }
	@$(COMPOSE) -f $(COMPOSE_FILE) build admin
	@set -eu; \
		trap '$(COMPOSE) -f $(COMPOSE_FILE) start api worker >/dev/null' EXIT; \
		$(COMPOSE) -f $(COMPOSE_FILE) stop api worker; \
		$(COMPOSE) -f $(COMPOSE_FILE) run --rm --no-deps admin backup --output "$(BACKUP_DIR)" --confirm-offline; \
		$(COMPOSE) -f $(COMPOSE_FILE) start api worker >/dev/null; \
		trap - EXIT

backup-verify:
	@test -n "$(BACKUP_DIR)" || { echo "BACKUP_DIR is required" >&2; exit 2; }
	$(COMPOSE) -f $(COMPOSE_FILE) run --build --rm --no-deps admin backup-verify --backup "$(BACKUP_DIR)"

restore:
	@test -n "$(BACKUP_DIR)" || { echo "BACKUP_DIR is required" >&2; exit 2; }
	@test -n "$(RESTORE_STORAGE)" || { echo "RESTORE_STORAGE is required" >&2; exit 2; }
	$(COMPOSE) -f $(COMPOSE_FILE) run --build --rm --no-deps admin restore --backup "$(BACKUP_DIR)" --storage "$(RESTORE_STORAGE)" --confirm-empty-target

contract:
	npx --yes @redocly/cli@$(OPENAPI_CLI_VERSION) lint contracts/openapi.yaml

compatibility:
	node --test scripts/verify-workspace-compatibility.test.mjs
	node scripts/verify-workspace-compatibility.mjs

clean:
	$(GO) clean
