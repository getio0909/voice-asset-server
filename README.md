# VoiceAsset Server

VoiceAsset Server is the API, worker, migration, and administration foundation
for the self-hosted VoiceAsset platform. Phase 1 upload/playback, the Phase 2
M4A boundary, and the Phase 3 provider/correction slice are implemented. The
current local `0.22.0` candidate adds authenticated realtime WebSocket
transcription and signed outbound Webhooks on top of the transactionally
generated, Session-only
personal terminal-job events on top of an audited, read-only allowlist of safe
deployment runtime facts and five-minute, single-use personal device
pairing and the bounded, audited failed-job retry boundary,
transactional incremental asset sync, and permanent-deletion
tombstones alongside session-only password changes with
all-session revocation alongside Owner-controlled workspace profile and
membership lifecycle on top of bounded Job Center, audit-log, and system-status reads,
Owner-confirmed retryable permanent deletion,
asynchronous immutable waveform PNGs, and
PostgreSQL full-text search across asset titles and each transcript's latest
immutable revision, Provider/Speaker filters, and bounded Segment hits with
timecodes. Collection, tag, status, UTC date,
assigned-tag, and versioned trash/restore behavior remains available while
preserving immutable audio and revisions. It builds on rotating refresh credentials, personal
device-session revocation, scoped Agent workflows, and hardened provider DNS
pinning. Mock ASR and Mock LLM require no cloud credentials; vendor live tests
remain explicit opt-in checks.

## Implemented Surface

- `GET /healthz`, `GET /livez`, `GET /readyz`, `GET /version`, and loopback-only
  Prometheus `GET /metrics`
- `GET /api/v1/system/capabilities`
- Session-only `GET /api/v1/events` with immutable terminal-job history and
  workspace-and-user-bound opaque checkpoints
- local owner bootstrap, rotating access/refresh web sessions, and personal
  device inventory/revocation
- audited workspace profile reads plus Owner-only, version-protected renames
- workspace member inventory plus Owner-only creation, role/status changes,
  optimistic concurrency, last-active-Owner protection, and credential
  revocation on disable
- workspace-scoped asset creation and resumable, checksum-verified WAV/M4A upload
- workspace-bound incremental asset changes with ordered opaque checkpoints,
  mutation-time snapshots, and durable permanent-deletion tombstones
- opaque-cursor PostgreSQL title/transcript search filtered by collection, tag,
  status, UTC date, ASR Provider, and Speaker, with bounded latest-Revision
  Segment timecodes and audited versioned trash/restore
- Owner-only permanent deletion after trash with exact UUID confirmation,
  optimistic concurrency, idempotency, storage-first integrity checks, durable
  job status, terminal-failure resume, and retained governance audits
- durable Mock ASR jobs and immutable raw transcript timelines
- Alibaba Cloud and Tencent Cloud batch ASR adapters, capability reporting,
  bounded retry/failover, encrypted workspace profiles, and opt-in live tests
- independent versioned ASR hotwords and LLM correction glossaries
- durable Mock/OpenAI-compatible LLM correction jobs with validated structured
  patches, immutable normalized/corrected revisions, append-only review, and
  approval revisions
- authenticated `GET`/`HEAD` audio with byte-range playback
- one-time, hashed, workspace-scoped API keys with least-privilege scopes,
  expiry, revocation, and immutable audit attribution
- workspace-scoped collection, tag, annotation, and bounded processing-status
  reads with stable cursors and fail-closed audit records
- `admin:read` job summaries, controlled audit entries, and generated system
  status with filter-bound opaque cursors and fail-closed read audits
- `admin:write` retry of eligible failed jobs with one additional bounded
  attempt, preserved history, workspace isolation, and atomic lifecycle/audit updates
- audited metadata/tag/annotation mutations plus immutable, one-hour audio-clip
  and JSON/Markdown/SRT/WebVTT transcript-export artifacts
- bounded worker cleanup of expired clip/export bytes and metadata with exact
  integrity checks, retry-safe ordering, and immutable system audits
- structured errors, safe request IDs, Info-level status/latency request logs,
  bounded process-local HTTP histograms/counters, migrations, API/worker/admin binaries,
  and a hardened local Compose stack

The REST source of truth is [`contracts/openapi.yaml`](contracts/openapi.yaml).

## Requirements

- Go 1.26.5 or newer in the 1.26 release line
- GNU Make (optional convenience commands)
- Node.js 24+ only for OpenAPI linting
- FFmpeg for audio-clip generation (included in the runtime container)
- PostgreSQL 17 client tools for backup and restore (included in the runtime
  container)
- Docker Compose only for the containerized stack

## Local Development

```bash
go mod download
make test
```

Start PostgreSQL, apply migrations, and run the API and worker in the foreground
with one command:

```bash
make dev
```

Use `make up` for a detached stack and `make down` to stop it. PostgreSQL data,
uploaded objects, and backups persist in separate named volumes. The Console
and same-origin API gateway listen on `http://localhost:8080` and are bound to
loopback only; the direct API diagnostic port is
`http://127.0.0.1:18080`. Check the public path with:

```bash
curl http://localhost:8080/api/v1/system/capabilities
```

Scrape process-local API metrics only from the direct loopback listener; the
same-origin gateway deliberately does not proxy this operator endpoint:

```bash
curl http://127.0.0.1:18080/metrics
```

Metric labels use bounded route families rather than raw paths, query strings,
or resource identifiers. Counters reset when the API process restarts. The
operator configuration under `deploy/prometheus/` persists these samples and
evaluates the checked-in availability, 5xx-rate, and p95-latency rules.

Validate migration ordering and checksums without PostgreSQL:

```bash
go run ./cmd/migrate -dry-run
```

The Compose password is a documented local-only value. If you replace it, also
set `VOICEASSET_COMPOSE_DATABASE_URL` with the same password percent-encoded as
required by a PostgreSQL URL. Use a separate hardened deployment configuration
instead of exposing this development stack to a network.
Create the initial owner with the dedicated administration container. The
password is piped on standard input and never placed in command arguments:

```bash
make seed ADMIN_EMAIL=owner@example.com
```

PowerShell equivalent:

```powershell
make seed ADMIN_EMAIL=owner@example.com
```

Then log in through `POST /api/v1/auth/sessions`. Set a unique base64-encoded
32-byte `VOICEASSET_PROFILE_MASTER_KEY` before storing real ASR or LLM profile
credentials. See the OpenAPI contract for the implemented `0.18.0` flows and
response schemas.

## Commands

- `make test` runs all Go tests.
- `make lint` runs `go vet`.
- `make build` compiles every command.
- `make contract` lints the OpenAPI 3.1 contract.
- `make compatibility` tests the workspace verifier and checks the five sibling
  repositories against the Server's real offline capability output, contract
  pins, required client features, and the Site's exact OpenAPI copy.
- `make live-asr-test` runs credential-backed Alibaba/Tencent checks only when
  `VOICEASSET_LIVE_ASR=1`; missing provider credentials skip that provider.
- `make perf-remote` runs the strict-TLS, read-only concurrency and p95
  release-candidate smoke test with the operating system trust store. Override
  `PERF_CONTRACT_VERSION` when intentionally testing another deployed contract;
  `PERF_CA_FILE` is optional for a private test CA.
- `make perf-data` runs the isolated PostgreSQL asset/search plus multipart
  upload, local-storage, Mock Worker, and full-hash audio performance gates when
  `TEST_DATABASE_URL` is supplied through the environment.
- `make perf-media` runs the real FFmpeg 30-second clip concurrency, p95,
  throughput, output-validation, and temporary-file cleanup gate.
- `make test-upgrade` proves every prior schema version upgrades to the current
  version and verifies representative legacy data remains intact in a disposable
  schema; see [the upgrade runbook](docs/operations/upgrade.md).
- `make migrate` builds and runs the one-shot migration service against the
  Compose PostgreSQL instance.
- `make worker` builds and runs one worker scheduling cycle.
- `go run ./cmd/worker --heartbeat 5s` runs the scheduler continuously; expired
  Agent artifacts are reaped in fair batches of at most 25.
- `go test ./tests/e2e` runs isolated-schema Phase 1 and Phase 3 workflows when
  `TEST_DATABASE_URL` is set; otherwise database tests skip without modifying a
  database.
- `pwsh ./scripts/run-live-console-e2e.ps1` runs the real Console browser flow
  against an ephemeral PostgreSQL schema, owner, API, worker, and object path,
  then removes all test state.
- `go run ./cmd/adminctl capabilities` prints the offline capability model.
- `go run ./cmd/api --version`, `go run ./cmd/worker --version`,
  `go run ./cmd/migrate --version`, and `go run ./cmd/adminctl version` print the
  binary version and source revision without loading configuration or connecting
  to PostgreSQL.
- `make seed ADMIN_EMAIL=owner@example.com` creates the initial owner from the
  `VOICEASSET_ADMIN_PASSWORD` environment variable through standard input.
- `make backup BACKUP_DIR=/app/backups/new-backup` briefly stops Compose API and
  worker writes, creates the backup through the admin container, and always
  restarts both services. Stop an external MCP service separately if present.
- `make backup-verify BACKUP_DIR=/app/backups/backup` validates the manifest, every
  file checksum, database archive, and object references.
- `make restore BACKUP_DIR=/app/backups/backup RESTORE_STORAGE=/app/var/restored`
  restores only into the clean database named by
  `VOICEASSET_COMPOSE_DATABASE_URL` and a new or empty storage path.

See the [performance validation runbook](docs/operations/performance.md) for the
fixed budgets, strict TLS procedure, current baseline, and remaining coverage.

## Release Archives

The Tag workflow uses the same scripts that can be exercised locally. Start
with a new, empty output directory; the builder refuses to mix a release with
existing files.

```bash
version=v1.0.0-rc.1
commit=$(git rev-parse HEAD)
mkdir dist
bash scripts/build-release.sh "$version" "$commit" dist
bash scripts/write-checksums.sh dist
bash scripts/verify-release.sh "$version" "$commit" dist
```

This builds deterministic Linux, Windows, and macOS AMD64/ARM64 archives. The
verifier checks archive paths and contents, contract pins, Go target metadata,
embedded version/revision values, the host binary commands, and every SHA-256
entry. Tag CI also builds a checksum-covered Linux AMD64/ARM64 OCI archive and
validates its digests, platforms, non-root identity, port, and immutable OCI
labels. It adds the SPDX JSON SBOM, rewrites `SHA256SUMS`, and repeats verification
with `--require-sbom --require-container` before uploading a draft prerelease.
The local commands above do not build the OCI archive. See the
[local release validation record](docs/operations/release-artifacts.md) for the
latest per-target evidence and its limits.

## Backup and Clean Restore

Backups combine a custom-format PostgreSQL dump, the complete local storage
tree, and a versioned SHA-256 manifest. Stop all writers before creating one;
the confirmation flag does not stop services for you. Verification is safe to
run while the service is online. Restore deliberately refuses a database with
user objects or a non-empty storage target and never overwrites a live install.

Treat backup directories as sensitive because they contain account data,
credential ciphertext, and API-key hashes. Encrypt them at rest and copy them
off-host. Follow the tested [backup and restore runbook](docs/operations/backup-restore.md)
for prerequisites, service ordering, failure recovery, and integrity checks.
For short-lived clip/export cleanup and backlog monitoring, follow the
[artifact retention runbook](docs/operations/artifact-retention.md).

## Independent Test Host

The Docker-free Debian test deployment currently runs the matching Console,
Server API/Worker `v0.1.0-dev+workspace.20260718.11` and MCP
`workspace-20260718.11` on contract `0.22.0` and schema 18 at
`https://api.getio.net:10443`. Its separate Caddy process
reuses the host's publicly trusted certificate through restricted symlinks.
An isolated loopback Prometheus 3.13.1 service retains API metrics for 7 days or
1 GiB and evaluates four locally tested alert rules. OTLP/HTTP tracing is
collected by the loopback Collector on `14318`, with the allowlisted alert
receiver on `19193` and Alertmanager on `19093`; no public observability port is
opened.
The deployed slices add Session-only personal terminal-job events, realtime
WebSocket transcription, signed outbound Webhooks, one-time
personal device pairing, workspace-scoped Job Center, audit-log,
Dashboard, system-status reads, and bounded failed-job retry without expanding
MCP authority. Migrations 1–18, immutable authenticated waveforms, Owner-confirmed storage-first
permanent deletion, PostgreSQL
title/latest-Transcript search, Provider/Speaker filters, Segment timecodes,
strict refresh rotation/device revocation,
trash/restore, bounded FFmpeg clips, transcript exports, scoped remote MCP
workflows, a 30-table/13-file clean-restore drill, deterministic expired-artifact
reaping, the Console's real API-key lifecycle, and conservative glossary-only
correction auto-approval have passed there or in an equivalent isolated
real-PostgreSQL workflow.
The 0.17 cutover used a verified offline backup and passed a disposable real
same-UUID retry, duplicate rejection, safe-audit retention, and fixture cleanup.
The 0.18 cutover additionally passed an isolated restore/migration drill and a
strict-TLS one-time pairing issue/claim/replay/logout acceptance flow; only the
secret SHA-256 remains at rest.
The 0.20 cutover restored and upgraded the verified r2 backup before live
migration, then passed strict-TLS Session/API-key, safe-field, cursor-binding,
tamper, audit, logout, and post-logout notification acceptance.
It does not read or reload the host's existing Caddy configuration. See
[`docs/operations/test-host-10443.md`](docs/operations/test-host-10443.md) for
the isolated systemd topology, certificate reload procedure, firewall boundary, and
operational checks.

## Architecture and Governance

Start with [`docs/architecture/README.md`](docs/architecture/README.md), then
read the [domain model](docs/architecture/domain-model.md),
[version strategy](docs/architecture/versioning.md), and accepted
[architecture decisions](docs/adr/). Cross-repository delivery state lives in
[`docs/program/PROGRAM_STATUS.md`](docs/program/PROGRAM_STATUS.md).

## License

Copyright 2026 VoiceAsset contributors. Licensed under
`AGPL-3.0-or-later`; see [`LICENSE`](LICENSE).
