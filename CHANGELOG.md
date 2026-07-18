# Changelog

All notable changes are documented here. This project follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and Semantic
Versioning.

## [Unreleased]

### Added

- Updated the OCI release verifier to safely dereference the standard nested
  image-index layout emitted by hosted BuildKit while retaining exact
  `linux/amd64` and `linux/arm64` platform checks.
- Hardened the hosted multi-platform OCI release step by disabling default
  BuildKit attestations and explicitly selecting OCI media types before the
  strict two-platform archive verifier runs.
- Fixed waveform Worker source resolution by selecting and preserving the
  original object's storage backend. Added a PostgreSQL regression assertion
  and reran the isolated live Console and MCP workflows: upload, Mock ASR,
  waveform playback, correction/review/approval, scoped MCP reads, audit
  attribution, and credential revocation all pass.
- Added a loopback-only OpenTelemetry Collector 0.155.0 and Alertmanager
  0.33.1 test-host deployment. OTLP traces are written to a protected local
  journal, Prometheus alerts are delivered through a bounded receiver that
  stores only allowlisted fields, and both paths have isolated health and
  delivery evidence without changing public Caddy.
- Added an explicit opt-in remote S3 lifecycle/performance probe covering
  concurrent part upload, exact assembly, snapshot verification, immutable
  publication, and integrity-checked deletion. The isolated test-host baseline
  passed without retaining credentials. The clean-instance S3 backup/restore
  gate now copies and restores database-referenced objects with size and
  SHA-256 verification.
- Added optional OTLP/HTTP tracing for API request boundaries and Worker
  scheduler cycles. Endpoint validation requires HTTPS except for loopback
  development, propagates W3C trace context, uses fixed span names, excludes
  metrics/WebSocket upgrade traffic, and avoids recording raw URLs or queries;
  exporter flush and validation are covered without a live collector.
- Added the production AWS SDK v2 S3-compatible storage adapter and driver
  factory. API and Worker now honor `VOICEASSET_STORAGE_BACKEND=s3` with
  optional static credentials, default credential-chain support, custom HTTPS
  CA roots, path-style endpoints, conditional create/delete, paginated part
  cleanup, and sanitized SDK errors. A local HTTP-compatible adapter test
  covers immutable publication, snapshot reads, ETag deletion, and cleanup;
  representative remote lifecycle/performance plus clean-instance
  backup/restore evidence pass on the isolated test host.
- Added contract `0.20.0` and migration 17 with a Session-only personal event
  feed for terminal jobs. Immutable notifications are inserted in the same
  transaction as each terminal state transition, expose only allowlisted task
  facts, retain retry history, and use workspace-and-user-bound cursors. API
  keys and outbound webhook delivery remain outside this boundary.
  Real PostgreSQL restore/upgrade and strict-TLS deployment acceptance pass on
  isolated 10443 as `.20260718.5`/schema 17 without changing public Caddy or
  the reused certificate.
- Added checksum-covered Linux AMD64/ARM64 OCI archives to the Server Tag
  workflow. Digest-pinned build/runtime and QEMU/BuildKit images feed a strict
  streaming verifier for blob integrity, exact platforms, immutable version and
  revision labels, port `8080`, and the unprivileged `65532:65532` runtime user.
- Added contract `0.19.0` with an authenticated, audited `admin:read` projection
  of allowlisted deployment runtime facts. The read-only endpoint exposes no
  database URL, filesystem path, object-store endpoint, credential, token, or
  row from the deployment-global `system_settings` table; all mutation methods
  fail with `405 Method Not Allowed`.
- Added contract `0.18.0` and migration 16 for personal-session-only device
  pairing. Five-minute capabilities are random, stored only as SHA-256, revoked
  when superseded, rate-limited on claim, and atomically exchanged for a named
  device session after active user/membership checks. Invalid, expired,
  revoked, and replayed claims share one credential-free error boundary.
- Added contract `0.17.0` with an Owner/Admin `admin:write` retry boundary for
  eligible failed jobs. The Server requeues the same durable job, grants exactly
  one bounded attempt, preserves attempt history, restores the asset lifecycle
  when required, and writes the audit record in the same transaction.
- Added a dependency-free five-repository compatibility gate. It executes the
  real `adminctl capabilities` output, verifies every contract pin and client
  feature requirement, requires the Site OpenAPI copy to be byte-identical,
  rejects extra VoiceAsset repositories, and has a dedicated public-repository
  GitHub workflow plus four negative fixture tests.
- Added contract `0.16.0` and migration 15 with a transactional, workspace-
  scoped incremental asset feed. Ordered opaque cursors, bounded snapshot
  pages, historical backfill, and durable permanent-deletion tombstones let
  offline clients apply changes without follow-up reads. Real PostgreSQL tests
  cover tenant isolation, rollback atomicity, updates, and deletion.
- Added contract `0.15.0` session-only password changes with bounded
  reauthentication attempts, atomic PBKDF2 replacement, cross-workspace
  all-session revocation, cookie expiry, and credential-free audit metadata.

- Added the authenticated workspace profile to contract `0.14.0`. Principals
  with `admin:read` can perform an audited read, while Owners with
  `admin:write` can rename the workspace using an exact version ETag. Changed
  names, versions, and audits commit atomically; normalized no-op updates retain
  the current version.
- Added contract `0.14.0` workspace member administration: audited bounded
  inventory for `admin:read`, Owner-only local-user creation and role/status
  changes, optimistic concurrency, last-active-Owner protection, and atomic
  session/API-key revocation when a membership is disabled. Passwords are
  write-only and never returned or included in audit metadata.
- Added an unadvertised real-time transcription core: strict protocol-v1 JSON
  Schema and duplicate-key rejection, durable migration 13 recording sessions,
  idempotent frame ACK/reconnect/final-result replay, an exclusive provider
  stream hub, deterministic Mock ASR streaming, and an authenticated
  pre-upgrade HTTP boundary. Initial and established client-event reads are now
  bounded to three advertised heartbeat intervals; an idle established stream
  becomes interrupted and retains its Provider only for the bounded reconnect
  window. The concrete WebSocket adapter and API startup wiring remain
  intentionally unavailable pending production-dependency approval; deploying
  schema 13–15 does not expose a WebSocket path.

### Changed

- Deployed contract `0.20.0` and schema 17 as
  `v0.1.0-dev+phase6.20260718.5`. A verified 42-object/42-file backup restored
  and upgraded in disposable targets before cutover. Strict-TLS personal-event
  acceptance passed Session/API-key boundaries, 35 safe ordered events, empty
  checkpoints, authenticated cursor binding, tamper/method rejection, safe
  audits, logout, and post-logout denial. The deployment archive SHA-256 is
  `8f86c245063e244b57b0c2a779f8f099c78404342e3e7128ba3905eef5de23f5`;
  public Caddy, the gateway, their configuration hashes/restart counts, and the
  reused certificate remained unchanged.
- Hardened Tencent Flash normalization for live responses whose word timestamps
  extend outside their enclosing sentence timestamps. The normalized segment
  now expands to contain every word while preserving the vendor word timeline
  and immutable raw JSON. Safe live-test diagnostics expose only the sanitized
  provider code; a credential-backed 16 kHz WAV test passes against the current
  Tencent service when the account APPID is used instead of its UIN.
- Deployed contract `0.19.0` without a schema change as
  `v0.1.0-dev+phase6.20260718.4`. The verified offline backup contains 42
  database objects and 42 storage files. Strict-TLS acceptance proved
  unauthenticated denial, the exact eight-field allowlist, query rejection,
  `405` mutation denial with `Allow: GET`, immutable read auditing, and logout.
  The retained 19,444,593-byte deployment archive has SHA-256
  `9cfbcc553b8e0907751f40fb926ba5757cf27fe13baf558c42a17ec9349c03aa`.
  Schema 16, both Caddy processes/configurations, the reused certificate, and
  Prometheus remained unchanged; all six services report zero restarts and the
  post-deploy VoiceAsset error journal is empty.
- Deployed contract `0.18.0` and schema 16 as
  `v0.1.0-dev+phase6.20260718.3` after verified offline backups and an isolated
  restore/migration/API drill. A real strict-TLS pairing flow found a subsecond
  mismatch between the response and custom-URI expiry; the HTTP boundary now
  serializes one conservative whole-second value in both locations. The
  regression, full ordinary/race suites, exact-Origin denial, claim, replay
  rejection, logout, SHA-256-at-rest, and journal-redaction checks pass. The
  retained 19,441,092-byte archive SHA-256 is
  `54f37ce08a561d28e44ddeefffa880f8a506ebc21d2339f0c89a6b4f828502d6`;
  public Caddy and the certificate-reusing independent gateway were unchanged.
- Deployed Server API/Worker, MCP, and the matching Console as
  `0.1.0-dev+phase6.20260718.1`, contract `0.17.0`, and schema 15 to the
  isolated `10443` topology after a verified offline backup. A real failed-job
  retry passed same-UUID lifecycle restoration, bounded-attempt expansion,
  duplicate rejection, safe immutable auditing, and mutable-fixture cleanup.
  The retained deployment archive SHA-256 is
  `3950784247ce2870f684d4d42b8a209cc025bc144f5260004cdb20aa72b98f61`.
  All five services have zero restarts and empty error-priority journals; 10443
  still reuses the existing certificate and public Caddy remains unchanged.
- Switched only the independent `10443` gateway from its internal CA to the
  existing `api.getio.net` public certificate via restricted root-managed
  symlinks. The gateway runs with least-privilege key access and reloads itself
  daily for renewals; external system-trust checks and the read-only performance
  gate pass while the public Caddy process and configuration remain unchanged.
- Advanced the additive contract candidate to `0.13.0` with workspace-scoped,
  `admin:read` job, audit-log, and system-status read models. Filter-bound
  cursors, controlled response fields, and fail-closed read audits prevent
  cross-tenant replay and invisible administrative access.
- Deployed Server API/Worker `0.1.0-dev+phase6.20260717.6`, MCP
  `0.1.0-dev+phase6.20260717.4`, contract `0.13.0`, and the matching Console to
  the isolated `10443` topology. Full local gates, exact-source Linux race,
  strict-TLS official-SDK MCP reads, and a real authenticated Chromium
  administration/accessibility smoke pass. API, Worker, and MCP have zero
  restarts; gateway PID `96419`, public Caddy PID `18314`, and the public
  Caddyfile SHA-256 remain unchanged. The combined deployment archive SHA-256
  is `447031b5a7e28e65b6dbb70178f0a4c9ac0dfc623d8d83ee50f9a577a89a446c`.

- Replaced the process-local request-duration summary with fixed cumulative
  Prometheus histogram buckets and added boundary/concurrency coverage. Server
  API/Worker `0.1.0-dev+phase6.20260717.5` passed local tests/vet/build, an
  exact-source Linux race run, staged-binary verification, rollback, and live
  histogram checks. The retained archive SHA-256 is
  `8f2ff94d780bdefcbd95a98ec63db085a5fd94e5f1a4cdd1d1fec0cd10f04dc7`.
- Added a checksum-pinned Prometheus 3.13.1 configuration, four alert rules,
  rule-unit tests, and a hardened loopback-only systemd service with 7-day/1-GiB
  retention. The isolated deployment retains samples across restart, reports
  two healthy scrape targets and four healthy inactive rules, exposes no public
  metrics endpoint, and leaves API, MCP, gateway, and public Caddy unaffected.
  CI now validates the configuration and rules with the same pinned `promtool`.
- Added a standard-library Prometheus text endpoint at direct-API `GET /metrics`,
  with process build identity, in-flight requests, bounded
  method/route/status counters, and duration sums/counts. Metrics and new
  Info-level structured request logs omit raw paths and query strings; unsafe
  caller-supplied request IDs are replaced before response or logging.
- Deployed Server API/Worker `0.1.0-dev+phase6.20260717.4` to the isolated host
  after local tests/vet/build, a clean Linux race run, exact staged-binary hash
  verification, and an independently checked `.3` rollback snapshot. Direct
  loopback metrics, structured-log redaction, method rejection, and strict-TLS
  gateway non-exposure pass. The retained archive SHA-256 is
  `21b59922f3bc8aa3679f7e98cd4fb37b5583ceebe57ca4c67ebed94faefe0abb`;
  MCP PID `124649`, gateway PID `96419`, public Caddy PID `18314`, public
  Caddyfile SHA-256, zero restart counts, and empty error journals are unchanged.
- Advanced the additive contract candidate to `0.12.0` with migration 12.
  Workspace Owners can permanently delete an already trashed asset only after
  exact UUID confirmation, exact-version preconditions, and idempotency. A
  storage-first Worker verifies and removes parts/objects before transactionally
  deleting the relational graph, retains audits, exposes durable status, and
  permits explicit recovery from a terminal failed purge.
- Deployed `0.1.0-dev+phase6.20260717.3`, contract `0.12.0`, migration 12,
  matching Console, and MCP to the isolated `10443` topology after verified
  code and database/object backups. A real strict-TLS browser upload, Mock ASR,
  trash, exact-ID permanent purge, graph/file removal, retained-audit, and empty
  browser-storage flow passed; the official MCP SDK passed its remote read smoke.
  The bundle SHA-256 is
  `34eb142be272356522a2fc80346944be39f4247835401b3dc7be0450b7fec1c2`;
  public Caddy PID `18314` and gateway PID `96419` remain unchanged.
- Added a context-aware local/S3 storage-driver boundary with typed backend
  persistence and fail-closed read matching. The SDK-independent S3 core now
  enforces conditional create, full part/object SHA-256 verification, ETag-
  guarded deletion, bounded exact-prefix cleanup, and disposable seekable read
  snapshots. Production S3 startup remains disabled until its reviewed SDK
  adapter, compatibility test, backup policy, and performance gate are present.
- Advanced the additive contract candidate to `0.11.0` with migration 11.
  Upload completion atomically queues a bounded waveform job; the Worker uses
  deterministic FFmpeg settings to publish one immutable 1600x256 PNG per
  original, and authenticated GET/HEAD delivery verifies size and SHA-256.
- Deployed `0.1.0-dev+phase6.20260717.2`, contract `0.11.0`, migration 11,
  matching Console, and MCP to the isolated `10443` topology after verified
  code and database/object backups. Backfill plus real Chromium produced 11
  waveforms for 11 originals with zero failed/pending jobs; strict-CA
  performance, authenticated PNG UI, full upload-to-approval, and official-SDK
  MCP reads pass. The bundle SHA-256 is
  `3084cf2769e9ffeff0b37b2f61fbbf83815cc4d8b421dba078143f37a11a0a6f`;
  public Caddy PID `18314` and gateway PID `96419` remain unchanged.
- Added a production-renderer waveform performance gate. Two consecutive
  12-output runs at concurrency 3 validate complete 1600x256 PNGs, the 4 MiB
  ceiling, throughput/p95 floors, and empty temporary storage; the latest
  reached 11.5 ops/s and 317.440 ms p95.
- Deployed `0.1.0-dev+phase6.20260717.1`, contract `0.10.0`, migration 10,
  the matching Console, and MCP to the isolated `10443` topology. Strict TLS,
  full upload/ASR/search/correction/approval Chromium, lifecycle/export/provider
  browser regressions, and both official-SDK MCP smokes pass. Server and Console
  archive SHA-256 values are
  `57fb8e86eac9c31d29145563b516565eaf0121814cef61cef624c7f841c74ef0` and
  `20fa1e08bd6a162792626aa780f865b8c046560b1dd10d06e52f747b3788fc41`;
  public Caddy PID `18314` and gateway PID `96419` remain unchanged.
- Deployed `0.1.0-dev+phase5.20260717.2`, contract `0.9.0`, migrations 1–9,
  the matching Console, and MCP to the isolated `10443` topology. Strict
  CA/hostname checks, a real filter/metadata/trash/restore Chromium flow, and an
  official-SDK read-only MCP smoke passed. Server and Console archive SHA-256
  values are `413f7c07ec63c75355dbc3975b2970937c24d6bd08c54d59a35a394217799b02`
  and `a8c1dd4fb92e7d574495952eabe4457dd0d2585a6edfb6e75405fbcc09bba7cc`;
  public Caddy PID `18314` and gateway PID `96419` were unchanged.
- Deployed `0.1.0-dev+phase5.20260717.1`, contract `0.8.0`, migrations 1–8,
  the matching Console, and MCP to the isolated `10443` topology. A post-start
  `pipefail`/SIGPIPE assertion exercised automatic rollback before the corrected
  retry. Strict CA/hostname checks, a real refresh/device-revocation Chromium
  flow, and an official-SDK read-only MCP smoke passed with public Caddy PID
  `18314` and gateway PID `96419` unchanged. Retained Server and Console archive
  SHA-256 values are `3ea26300e43bfc8bc97758819b39d504c753d067ab47e03d96002e82aac91177`
  and `01cf3c35bcb7e51083da69acf8b3cf4b202b174b524be7418673a3b262fad8e2`.
- Hardened OpenAI-compatible endpoints against DNS rebinding and proxy bypass:
  resolution is validated once, mixed public/special-use answers fail closed,
  the approved IP is pinned for dialing, redirects are disabled, and ambient
  proxy environment variables are ignored.
- Deployed `0.1.0-dev+phase5.20260716.6` and the matching Console bundle to the
  isolated `10443` topology. A bad loopback version-path assertion exercised
  automatic binary rollback before the corrected retry; strict TLS and a
  credential-free deployed browser smoke then passed with unchanged MCP,
  gateway, and public Caddy PIDs. Retained Server and Console archive SHA-256
  values are `550bb0019a8c1ab19bc7001b35862378f5c2125b4c0a89495f8937b7837d1df0`
  and `1bf50f47b874b8b10950166004d3ff0ba540728cf34d058f559133d5ce127e2c`.
- Extended the deployed `.5` Console with LLM Profile/Glossary administration.
  Strict CA/hostname checks and a real credential-free ASR-plus-LLM browser
  session passed without restarting either Caddy process; the retained Console
  archive SHA-256 is
  `034f7c2e233e032e5857f9e10388b225c3b597bd4a110e81a97d4d9be2ea13c6`.
- Extended the deployed `.5` Console with ASR Provider/Hotword administration.
  The isolated gateway passed strict CA/hostname checks and a real credential-free
  browser session without restarting the public or isolated Caddy process.
- Deployed `0.1.0-dev+phase5.20260716.5`, contract `0.7.0`, migrations 1–7,
  and the bounded artifact reaper to the isolated Debian topology. An incorrect
  deployment-time database-name check triggered automatic rollback; the
  corrected run and deterministic remote reaper E2E completed without changing
  either Caddy PID. The matching Console API-key bundle passed strict-TLS and
  real create/display-once/revoke browser verification.
- Deployed `0.1.0-dev+phase5.20260716.4`, contract `0.7.0`, migrations 1–7,
  and the permanent backup administration binary to the isolated Debian test
  topology. A failed early health probe proved automatic rollback before the
  corrected deployment completed without restarting either Caddy process.
- Deployed `0.1.0-dev+phase4.20260716.3`, contract `0.7.0`, and migrations
  1–7 to the isolated Debian `10443` topology without restarting its gateway
  or the host's public Caddy process.
- Rotated the remote MCP service from a read-only key to a six-scope Agent key,
  enabled explicit writes, verified read-only denial and Agent audit
  attribution, then revoked the previous key.

### Added

- OpenAPI contract `0.10.0` and migration `000010` for PostgreSQL full-text
  title/latest-Revision Segment search, literal fallback for non-tokenized
  languages, ASR Provider and case-insensitive Speaker filters, and bounded
  chronological Segment hits with immutable identifiers and timecodes. Unit,
  HTTP, real-PostgreSQL, every-version upgrade, Console, and MCP tests cover the
  local candidate.
- OpenAPI contract `0.9.0` and migration `000009` for query-bound collection,
  tag, status, and UTC date filters; assigned-tag reads; and optimistic,
  audited trash/restore that preserves immutable audio and transcript lineage.
  Unit, HTTP, real-PostgreSQL, every-version upgrade, mocked browser, and
  deployed browser tests cover the slice.
- A cross-repository v1.0 release-notes draft that records candidate scope,
  compatibility, upgrade guidance, retained validation, scenario status, and
  every gate that still prevents a stable release claim.
- Reproducible release scripts for six Linux/Windows/macOS AMD64/ARM64 Server
  archives, deterministic SHA-256 manifests, safe package extraction checks,
  contract/target/version/revision verification, and a required-SPDX mode for
  the Tag workflow. Two complete local builds produced identical archive hashes.
- Side-effect-free JSON version output for the API, Worker, Migrate, and
  Adminctl binaries so every packaged command exposes its embedded release and
  source revision before configuration or database access.
- A side-effect-free strict-TLS release-candidate performance smoke for the
  isolated readiness/capabilities control plane. It uses fixed warm-up, 400
  measured requests at concurrency 8, zero-error enforcement, and p95/throughput
  budgets; the first retained baseline reached 42.8 req/s and 217.871 ms p95.
- An opt-in PostgreSQL performance gate that applies all migrations in a
  disposable schema, seeds 5,000 assets, then measures concurrent asset/audit
  creation and list/title-search reads through the production services. The
  first retained baseline reached 900.6 creates/s at 41.758 ms p95 and 194.2
  reads/s at 54.092 ms p95, then removed its schema.
- An isolated local data-pipeline performance gate covering two-part 5.24 MiB
  WAV upload, checksum-protected storage assembly, media probing, Mock ASR
  Worker publication, and full-file hash verification during audio access. The
  first retained baseline passed every operation at 499.027 ms, 107.040 ms, and
  32.332 ms p95 respectively.
- A real FFmpeg performance gate that concurrently generates twelve 30-second,
  mono 16 kHz PCM clips from a 5.24 MiB source, fully reads and validates every
  output, and proves temporary-file cleanup. Two retained runs passed the
  3-second p95 and 1 operation/second floors; the latest reached 12.9 operations/
  second and 454.337 ms p95.
- Real PostgreSQL upgrade-matrix tests from each schema version 1–7 to version 8,
  plus a staged v1-to-v2-to-v8 test preserving legacy workspace, user, asset,
  transcript, Provider, setting, access session, and queued-job state while
  verifying the version 8 device-session backfill.
- OpenAPI contract `0.8.0` and migration `000008` for atomic access/refresh
  rotation, SHA-256-only refresh storage, recognizable device names, personal
  session inventory/revocation, secure path-scoped cookies, and immutable
  create/refresh/revoke audits.
- Strict `validated_glossary_only` correction auto-approval. It requires a
  non-empty effective glossary, a non-empty deterministic patch, and every
  existing text, number, semantic, ratio, and timeline validation to pass. One
  transaction records the approved correction, automated review, system audit,
  and immutable `human_edited` -> `approved` descendants; `never` remains the
  default and already approved proposals reject duplicate manual approval.
- Real-PostgreSQL and live Chromium coverage for manual approval followed by
  glossary-only auto-approval, including exact lineage, system attribution,
  duplicate-approval rejection, MCP read/audit verification, and complete
  ephemeral-schema cleanup.
- Real disposable-schema Console coverage for immutable glossary v1/v2 and
  state changes plus Mock LLM profile create, health, and disable. The same run
  passes the Phase 3, ASR administration, and MCP audit workflows before
  deleting all transient state.
- Real disposable-schema Console coverage for immutable hotword v1/v2 and state
  changes plus Mock ASR profile create, health, and disable, followed by the
  existing MCP read/audit proof and complete transient-state cleanup.
- A bounded worker reaper for expired audio clips and transcript exports. It
  verifies exact file integrity, uses retry-safe file-first and conditional
  metadata deletion, preserves permanent source records, writes system audits,
  and has unit plus real-PostgreSQL isolation coverage.
- An expired-artifact retention runbook with backlog, audit, alerting, failure,
  and offline-backup guidance.
- Offline `adminctl backup`, `backup-verify`, and clean-target `restore`
  commands with PostgreSQL custom archives, complete local-storage copies,
  versioned SHA-256 manifests, credential-safe client invocation, and strict
  database/object inventory checks.
- A recovery runbook and isolated test-host drill that restored 30 user tables
  and 13 stored objects with exact row-count and file-hash parity while leaving
  the public Caddy and independent `10443` gateway processes unchanged.
- CI runtime-image, Compose-model, and real PostgreSQL backup/clean-restore
  gates, plus a Tag-triggered AMD64/ARM64 draft release pipeline with checksums
  and an SPDX SBOM.
- Audited, workspace-scoped asset metadata, tag, and annotation mutations with
  optimistic concurrency where applicable and Agent/API-key attribution.
- Immutable audio clips generated through argument-safe FFmpeg execution, with
  a five-minute limit, verified storage, one-hour authenticated download URLs,
  byte ranges, expiry, and fail-closed read auditing.
- Immutable JSON, Markdown, SRT, and WebVTT transcript exports with bounded
  storage, one-hour authenticated downloads, byte ranges, and read/create
  audits.
- Migration `000007` for short-lived clip and transcript-export lineage.
- OpenAPI contract `0.7.0` collection, tag, annotation, and bounded
  processing-status reads with stable pagination, workspace isolation,
  soft-delete filtering, and fail-closed Agent audit attribution.
- OpenAPI contract `0.6.0` workspace-scoped API keys with one-time plaintext
  delivery, SHA-256-only storage, least-privilege scopes, expiry, idempotent
  revocation, Agent attribution, and immutable lifecycle/read audits.
- An isolated loopback MCP systemd unit and `/mcp` reverse-proxy route with a
  separate inbound bearer, retained SDK DNS-rebinding protection, and explicit
  internal-CA trust handling for the `10443` test deployment.
- OpenAPI contract `0.5.0` asset title search with stable opaque-cursor
  pagination for public API clients and MCP.
- Fail-closed immutable read auditing for asset and transcript access, including
  `actor_type=agent` classification for agent-role sessions.

- OpenAPI contract `0.4.0` with encrypted ASR/LLM profile administration,
  provider capabilities and health checks, versioned hotword/glossary APIs,
  correction jobs, append-only review decisions, and approval endpoints.
- Alibaba Flash and Tencent Flash ASR adapters implemented from vendor
  protocols, sanitized fixture contract tests, explicit live-test switches,
  provider-neutral errors, concurrency limits, retry, and eligible failover.
- Separate versioned ASR hotword and LLM glossary systems with workspace,
  collection, and asset resolution plus immutable per-job snapshots.
- Mock and OpenAI-compatible LLM providers with HTTPS/SSRF policy, encrypted
  credentials and custom headers, prompt isolation, structured patch output,
  and server-side semantic validation.
- Durable correction jobs and the immutable `raw_asr -> normalized ->
llm_corrected -> human_edited -> approved` lineage, including per-change and
  bulk review decisions and transactional duplicate-approval protection.
- Real PostgreSQL Phase 3 E2E coverage for glossary/profile creation, Mock LLM
  correction, partial review, approval, raw response preservation, and source
  revision immutability.
- OpenAPI contract `0.3.0`, the `m4a_uploads` capability, and bounded ISO BMFF
  probing for Android MediaRecorder AAC/M4A originals.

- Phase 1 local owner bootstrap, PBKDF2 password hashing, opaque hashed
  sessions, HttpOnly cookies, Origin checks, RBAC scopes, and login throttling.
- Workspace-scoped assets, idempotent resumable WAV upload, bounded probing,
  immutable local originals, and authenticated GET/HEAD/Range playback.
- Durable PostgreSQL transcription jobs with leases and attempts, deterministic
  Mock ASR, immutable raw provider responses, transcript revisions, and
  timestamped segments.
- OpenAPI contract `0.2.0`, a two-part-WAV Phase 1 E2E, and a shared persistent
  object volume for API and worker in Docker Compose.
- Cross-process no-replace object publication, symlink-safe rooted filesystem
  access, durable directory syncing on Linux, upload-state compensation, and
  checksum verification before audio playback.
- A secret-safe cross-repository live browser harness that creates and removes
  an isolated PostgreSQL schema while proving Console, API, worker, storage,
  authenticated audio, and the raw transcript together.
- Hardened systemd units and an independent Caddy `10443` configuration for a
  Docker-free Debian test host, with loopback-only API/PostgreSQL and internal
  CA operational guidance.
- Phase 0 Go module with API, worker, migration, and administration commands.
- Health, liveness, readiness, and capability-negotiation endpoints.
- OpenAPI 3.1 contract and contract validation command.
- Transactional PostgreSQL migration runner with advisory locking and checksum
  verification.
- Initial schema for identities, workspaces, assets, immutable objects,
  immutable transcript revisions, providers, jobs, settings, and audit records.
- Container, Docker Compose, CI, architecture decisions, and program governance.
- PostgreSQL-backed migration integration coverage, immutable container pins, and
  Go 1.26.5 security baseline.

[Unreleased]: https://github.com/getio0909/voice-asset-server/commits/main
