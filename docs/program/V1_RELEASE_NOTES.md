# VoiceAsset v1.0 Release Notes (Draft)

> **Status:** Draft only. `v1.0.0` has not been tagged, published, or declared
> stable. The coordinated Phase 1–6 slices are still uncommitted, so this
> document is release-candidate guidance rather than a release announcement.

## Release Identity

- Planned product version: `v1.0.0`.
- REST namespace: `v1`.
- Shared OpenAPI contract candidate: `0.22.0`.
- Latest deployed evidence: Server API/Worker
  `v0.1.0-dev+workspace.20260718.11` and MCP `workspace-20260718.11`, contract
  `0.22.0`, migration schema 18, and the matching Console bundle; authenticated
  Webhook and WebSocket acceptance
  passed on isolated `10443`, followed by readiness, capability, MCP boundary,
  `/version`, and static Console asset checks after the 14:21 UTC cutover.
- Coordinated test tag `v0.1.0-rc.6` now points at the current default-branch
  commits for all five repositories. Its five Release Candidate workflows
  passed and created draft prereleases containing platform artifacts, SBOMs,
  and SHA256SUMS; this candidate is not `v1.0.0` and is not Stable.

## Candidate Highlights

- Server supports resumable WAV/M4A ingestion, immutable local originals,
  playback, Mock ASR, vendor adapters, transcript lineage, correction, review,
  approval, scoped API keys, rotating browser sessions, PostgreSQL title and
  latest-Transcript search, Provider/Speaker filters, Segment timecodes,
  versioned trash/restore, immutable waveform generation, owner-confirmed
  storage-first permanent purge, audited exports, and an audited workspace
  profile/membership lifecycle with exact-version renames, last-Owner, and
  credential-revocation safeguards. A session-only password endpoint rechecks
  the current password, atomically replaces its hash, revokes all of the user's
  sessions across workspaces, and records only credential-free audit metadata.
  A workspace-isolated incremental feed records immutable asset snapshots and
  permanent-deletion tombstones in the original mutation transaction, then pages
  them behind a fixed high-watermark cursor. An `admin:write` retry boundary
  grants only eligible failed jobs one additional bounded attempt on the same
  UUID and atomically updates lifecycle, Job, and credential-free audit state.
  Personal device pairing issues a five-minute one-time capability, stores only
  its SHA-256 digest, revokes older unclaimed issues, and atomically creates the
  named session plus credential-free audits after active-account checks. The
  deployed strict-TLS acceptance passed exact-Origin denial, successful claim,
  replay rejection, session reads, explicit logout, and hash-only persistence.
  An authenticated `admin:read` System Settings endpoint additionally exposes
  exactly eight allowlisted runtime facts, rejects every mutation and query
  parameter, writes a credential-free audit, and never reads the deployment-
  global settings table.
  A Session-only personal event feed now records each terminal job transition
  in the same transaction, retains retry history, returns only safe task facts,
  binds checkpoints to workspace and user, rejects API keys, and audits reads.
  Owner Session-only outbound Webhook management now uses one-time encrypted
  secrets, optimistic versions, notification-triggered durable deliveries,
  leased retries, signed requests, and safe delivery inspection.
  Authenticated realtime transcription now upgrades through the
  `voiceasset.realtime.v1` WebSocket subprotocol, persists resumable sessions,
  and runs the Mock ASR stream through the same controller tested locally and
  over the isolated TLS gateway.
- API and Worker now select a real AWS SDK v2 S3-compatible driver when
  `VOICEASSET_STORAGE_BACKEND=s3`; the adapter enforces conditional creation,
  full-hash reads, ETag deletion, paginated cleanup, custom HTTPS CAs, and
  sanitized errors. Optional OTLP/HTTP tracing accepts only HTTPS (or loopback
  HTTP), propagates W3C context, and uses fixed span names without raw URLs.
  Local adapter/exporter tests, the isolated remote S3 lifecycle/performance
  probe, and the clean-instance S3 backup/restore gate pass. The latter
  restored an original plus unfinished upload part into a new database and
  destination prefix with matching inventories and SHA-256 values.
  Collector 0.155.0, Alertmanager 0.33.1, and the allowlisted loopback alert
  receiver are deployed and have retained trace and synthetic-alert evidence.
- The direct API listener exposes bounded process-local Prometheus HTTP metrics
  and emits Info-level structured status/latency request logs without raw paths
  or query strings. A loopback-only Prometheus 3.13.1 service retains samples for
  7 days or 1 GiB and evaluates four unit-tested rules. OpenTelemetry export is
  configured to the isolated loopback Collector, and an operator-selected local
  Alertmanager notification receiver has passed delivery and redaction checks.
- Console covers the Owner workflow from PostgreSQL title/latest-Transcript
  search with Provider/Speaker filters and Segment timecodes,
  authenticated audio, processing/annotation detail, and versioned metadata
  editing through upload, ASR/LLM administration, review, approval, immutable
  transcript export, waveform seek/playback controls, API-key management,
  device revocation, exact-ID permanent-purge confirmation, Job Center, Audit
  Log, live operational Dashboard, System Status, and Admin-readable/Owner-
  writable workspace profile and membership management, a credential-free
  Version Information view over the fail-closed capability store, plus an Account flow
  that clears all password fields before validation or network I/O. A succeeded
  purge also removes the matching upload/audio/transcript result from browser
  memory immediately. The Device Sessions view creates a masked one-time
  Android pairing URI, keeps it only in Pinia memory, and clears it on refresh,
  reset, expiry, or route unmount. System Settings renders the Server allowlist
  as operator-managed and read-only with no input or save control.
- MCP exposes 21 scoped tools, five resources, and six prompts over stdio and
  Streamable HTTP while attributing reads and writes to Agent identities.
- Android contains offline recording/sync source, per-profile Room asset/change
  checkpoints, a bounded recent-assets Compose view for the active profile, and
  guarded multi-server switching. A separate bounded local recording view reports
  upload/transcription progress and errors without requiring the 0.16 feed, and
  failed/blocked rows can resume from their last durable checkpoint with a fresh
  transcription retry generation. Tested
  core recovery/API logic plus application compile, lint,
  Room schema/migration, debug/test APK, signed release APK/AAB, SBOM, and
  checksum gates pass. Android strictly parses the current
  pairing URI, clears it before claim, and stores the complete access/refresh
  session only through Keystore. Expiring credentials rotate through one
  serialized boundary, and the personal device inventory requires two-step,
  exact-UUID revocation with local cleanup only after remote success;
  connected-device gates remain open.
- Site provides aligned Chinese/English product, API, security, deployment,
  recovery, and troubleshooting documentation.

## Compatibility and Upgrade

All five local candidates pin contract `0.22.0`, and the isolated test host now
runs the matching deployed row. This is still not a supported v1 release.
Consult the [compatibility matrix](COMPATIBILITY_MATRIX.md) before mixing
component versions. Clients must fail closed when required capabilities or
scopes are absent.

Before upgrading, verify an offline database/object backup, stop writers, apply
migrations 1–18 with the Server migration command, and run readiness plus a
representative upload/transcription check. Migrations are forward-only in
production. Roll back by restoring a verified pre-upgrade backup into a clean
target; do not down-migrate live data. See the
[backup and recovery runbook](../operations/backup-restore.md).

## Retained Verification Evidence

- Server tests, vet, builds, Linux race checks, real-PostgreSQL migration,
  storage-first purge, administration/member reads and writes, recovery tests,
  atomic password rotation/all-session revocation, transactional incremental
  sync and tombstone rollback/backfill,
  pairing creation/claim/replay tests, System Settings allowlist/denial/audit
  tests, transactional personal-notification backfill/retry/rollback/isolation,
  and strict-TLS `0.22.0` Session/API-key/cursor/audit/logout/Webhook/WebSocket
  deployment checks pass. A disposable real
  retry also proves same-UUID state restoration, one-attempt expansion,
  duplicate rejection, safe audit retention, and mutable-fixture cleanup. The
  `.5` histogram, structured-log redaction, durable
  Prometheus restart, target/rule health, and non-exposure checks pass without
  exposing `/metrics` or the Prometheus listener through the gateway.
- Console passes formatting, lint, type checks, 107 unit tests, production build,
  and mocked plus deployed purge/full-text/filter/trash/restore/export browser
  workflows. The deployed purge case is self-cleaning and verifies both Server
  `404` and immediate Console state removal. A deployed authenticated
  administration smoke validates reduced Job/Audit/System fields, filters,
  empty Web Storage, zero unexpected writes, and zero axe violations. Its local
  Account flow also proves immediate three-field clearing, cookie-only transport,
  local identity removal, and no redundant logout. MCP
  passes coverage tests, vet, builds, SDK E2E, and remote strict-TLS reads. Site
  passes contract, i18n, accessibility, build, and link gates; its generated
  bilingual reference contains 91 operations. Android passes 134 JVM tests,
  Ktlint, Debug/Release Lint and APK assembly, and compilation of 43
  instrumentation methods. Its exact-Profile reconnect clears the password
  before network I/O, never persists it, and preserves any stored session when
  authentication is rejected. The local 0.22 Android candidate remains
  uncommitted; its 14,870,347-byte debug APK SHA-256 is
  `FEAC45E88C4BE2AD2208B4E5DF03E176B1DF2DB59E24DCEF07788514E849DA86`.
- An earlier Android checkpoint passed 112 JVM tests, Ktlint, first-party Release lint, a valid
  v2 development-signed debug APK, unsigned release APK/AAB verification,
  compilation of 32
  instrumentation tests, and a 141-component CycloneDX/checksum gate. Its Room
  asset cache is wired to the active-profile UI with a 50-item rendering bound.
  Room migration 3 preserves retry state across upgrades and avoids replaying a
  terminal transcription idempotency key. The tests
  are not claimed as run on a device, and final signing is not claimed.
- Local upload/storage/Mock Worker/audio performance budgets pass. Twelve real
  FFmpeg clips reached 12.9 operations/second and 454.337 ms p95 with zero
  failures and complete cleanup.
- Server and MCP scripts produced reproducible six-target archives, while Console
  and Site produced deterministic static archives, all with complete SHA-256 and
  content verification. These synthetic local builds are not publishable Tag
  artifacts. Server and Console now also have digest-pinned dual-architecture
  OCI Tag workflows and strict local verifier tests, but no actual image was
  built on this Docker-less workstation; see
  [artifact validation](../operations/release-artifacts.md).
- The independent Debian test deployment exposes only `10443` for VoiceAsset.
  Its gateway now reads the existing public certificate through restricted
  root-managed symlinks and reloads only itself; external system-trust checks
  pass, while the host's public Caddy configuration, process, and restart count
  remain unchanged. The retained 0.17 deployment archive SHA-256 is
  `3950784247ce2870f684d4d42b8a209cc025bc144f5260004cdb20aa72b98f61`;
  the current 14,348,236-byte development APK SHA-256 is
  `5a5afed75d841ddef861e58fdf30b1f6a60b8323790414a3792288d6d10965c2`.

## Acceptance Status

The current `v0.1.0-rc.6` candidate is the first retained five-repository
artifact set. Release workflows `29671512394`, `29671512945`, `29671513656`,
`29671514283`, and `29671514861` all passed. The Android APK checksum was
independently matched against its published `SHA256SUMS` before installation.

- **A — self-hosted installation:** native isolated deployment and restore are
  verified; the Docker upgrade gate remains open.
- **B — Android recording and sync:** core JVM logic, application compile, lint,
  Room schema, debug/test APK, formally signed release APK/AAB builds, and all
  44 API 35 Hosted Emulator instrumentation tests in Android CI
  `29667693126` are verified. Physical-device microphone, process-death, and
  network-recovery acceptance remains open.
- **C — ASR and LLM workflow:** Mock and fixture workflows are verified;
  successful live-vendor execution is not claimed.
- **D — MCP integration:** local and isolated remote read/write, scope, and audit
  evidence is retained.
- **E — backup and recovery:** local-storage and clean-instance S3 restore are
  verified, including object inventories and SHA-256 checks.

## Gates Before Publication

- Submit the coordinated slices and pass all five default-branch CI workflows.
- Run immutable Tag workflows and retain exact commits, image digests, SBOMs,
  checksums, signatures, and provenance. The draft `v0.1.0-rc.6` artifacts are
  retained evidence, not the final `v1.0.0` publication.
- Validate Linux amd64/arm64 container builds and Docker upgrade/restore.
- Retain the hosted emulator run, then pass microphone, process-death,
  network-recovery, and externally signed APK/AAB gates on an accelerated
  emulator or physical device.
- Retain the clean-instance S3 backup/restore evidence before advertising that
  backend.
- Retain the deployed Collector, Alertmanager, and receiver evidence while
  closing the remaining release gates.
- Close every required item in the [v1.0 checklist](RELEASE_CHECKLIST.md).

Do not publish `v1.0.0` or promote any component to Stable until these gates
pass and this draft is replaced with immutable artifact references.
