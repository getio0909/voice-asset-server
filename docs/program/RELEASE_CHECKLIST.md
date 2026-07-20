# v1.0 Release Checklist

Unchecked items are not complete and must not be inferred from project files.

## Governance and Contract

- [x] Server license, contribution, security, changelog, CODEOWNERS, and CI
      baseline
- [x] Server architecture, domain model, ADRs, OpenAPI 3.1 draft, and migration
      framework
- [x] Cross-repository v1.0 release-notes draft distinguishes retained candidate
      evidence from every open publication gate
- [x] All five current release-candidate slices independently build and pass
      default-branch CI (merged runs `29655623363`, `29655632775`,
      `29655641245`, `29655652283`, and `29655685955`)
- [x] Cross-repository compatibility matrix is proven by integration tests
      (the eight-fixture local gate and hosted five-checkout run
      `29655623342` pass)
- [x] Hosted `v0.1.0-rc.7` release-candidate workflows pass for all five
      repositories (`29681441081`, `29681442803`, `29681444319`, `29681445958`,
      and `29681447473`); each remains a draft prerelease with checksums and
      SBOMs, and the Server/Console OCI bundles plus signed Android APK/AAB
      are retained for testing
- [x] Hosted `v1.0.0-rc.2` release-candidate workflows pass for all five
      repositories (`29684731273`, `29684732759`, `29684734369`, `29684735798`,
      and `29684737106`); the draft prereleases retain the recorder-first
      Android APK/AAB, platform archives, OCI bundles, SBOMs/licenses, and
      `SHA256SUMS` for testing

## Product

- [x] Complete asset upload, storage, playback, and lifecycle (hosted Compose
      smoke `29670080607` covers multipart upload, local object storage,
      authenticated range/HEAD playback, metadata, export, trash, listing,
      and restore)
- [x] Hosted Docker Compose startup smoke covers Postgres, migrations, API,
      Worker, Console gateway, readiness, and capabilities; the smoke uses
      isolated project volumes and cleans them after the run
- [x] Hosted Compose Phase 1 HTTP smoke covers owner bootstrap/login, multipart
      WAV upload and completion, Worker Mock ASR, normalized transcript retrieval,
      and range/HEAD playback through the Console gateway (`29664615096`)
- [x] Mock ASR and Mock LLM end-to-end path
- [x] Alibaba Cloud and Tencent Cloud fixture and opt-in live-test paths
- [x] Immutable transcript correction, review, and approval flow
- [x] Console lifecycle management and accessibility checks (title search,
      cursor paging, authenticated audio, processing status, annotations,
      Collection/Tag/status/date filters, assigned-tag controls, versioned
      metadata, trash/restore, PostgreSQL title/latest-Transcript search,
      Provider/Speaker filters, Segment timecodes, bounded bulk lifecycle,
      authenticated immutable waveform, pointer/keyboard seeking, playback
      speed, Owner-only exact-ID permanent deletion with durable status/retry,
      immediate post-purge in-memory result removal, mocked Chromium, deployed
      permanent-purge Chromium, and axe pass)
- [x] One-time pairing QR slice: Console in-memory QR rendering and Android
      Google Code Scanner QR flow with paste fallback pass hosted PR CI;
      physical-device scanning remains acceptance evidence
- [ ] Android offline recording, recovery, upload, and sync (Hosted Emulator
      now covers production MediaRecorder capture, real-M4A recovery, host-driven
      process death, the durable network checkpoint, and 51 instrumentation
      tests. The authorized M2012K10C (Android 13) also passed a signed-build
      27-second local recording, playback pause/stop, and system-share export
      on Android CI APK `e8ec632ba431bbc65edf85022ef9dbaaafcff8775b1bc14e403e8cedbb285605`.
      Physical recovery/reboot,
      upload/sync, and complete scenario-B evidence remain open)
- [x] MCP stdio and Streamable HTTP tools, resources, scopes, and audit
- [x] MCP asset search, specified Revision, exact time citation, scope denial,
      and read-audit vertical slice
- [x] Scoped Agent API-key lifecycle and isolated remote MCP authentication
- [x] Console API-key list/create/revoke workflow with one-time in-memory token
      handling and accessibility coverage
- [x] Workspace member inventory and Owner-only creation/role/status lifecycle
      with optimistic concurrency, last-active-Owner safety, disable-time
      credential revocation, audits, and accessible Console coverage
- [x] Audited workspace profile read and Owner-only exact-ETag rename with
      workspace isolation, monotonic versions, and accessible Console coverage
- [x] Bounded audio clips and transcript exports with authenticated download,
      expiry, hashes, byte ranges, Agent audit attribution, and deployed Console
      export/download integrity
- [x] Bounded expired clip/export cleanup with integrity checks, retries,
      immutable system audits, and real-PostgreSQL coverage
- [x] Chinese and English Site with generated API reference and no broken links

## Security and Operations

- [x] Authentication, RBAC, revocation, rate limits, SSRF and upload defenses
- [x] Provider secret envelope encryption and redaction tests
- [x] Backup, verification, clean-instance restore, rollback, every-version schema
      upgrade, and legacy-data preservation tests
- [x] Dependency audit, secret scan, license check, and SBOM are green in every
      repository
- [x] Isolated strict-TLS control-plane concurrency, error-rate, p95, and
      throughput smoke
- [x] Isolated PostgreSQL asset-create/audit and list/title-search performance
      budgets
- [x] Multipart upload, local storage, Mock Worker, full-hash audio, and WAV-probe
      performance budgets
- [x] Real FFmpeg 30-second clip and 1600x256 waveform p95, throughput, output,
      and cleanup budgets
- [x] Structured status/latency request logs and bounded Prometheus HTTP metrics
      without raw path, query, credential, or resource-ID labels
- [x] Operator-owned loopback Prometheus retention and unit-tested alert-rule
      evaluation without public metric or administration exposure
- [x] OpenTelemetry trace export and an operator-selected alert notification
      (Collector 0.155.0 receives loopback OTLP/HTTP on `14318`, Alertmanager
      0.33.1 delivers to the loopback receiver on `19193`, the receiver stores
      only allowlisted fields, and an API trace plus synthetic alert were
      verified on the isolated host without changing public Caddy)
- [x] S3-compatible storage adapter and API/Worker wiring (SDK-backed adapter
      lifecycle tests, isolated remote compatibility/performance, and the
      clean-instance backup/restore gate all pass; the test bucket and
      credentials were removed after verification)
- [x] Reproducible, checksum-verified Server and MCP archives for Linux,
      Windows, and macOS AMD64/ARM64
- [x] Deterministic, checksum-verified Console and Site static archives with
      safe extraction and exact-content comparison
- [x] Linux amd64 and arm64 Server/Console container images (hosted `rc.5`
      builds and strict OCI validators pass; retained OCI tar digests are
      `sha256:3a299378bdf5e43d603bd8e3b09b9ef18a67fb6723f41893b6aa7c0a19fd6dc9`
      and
      `sha256:e20fd671d30e02eecc5bfeca4d10bd6774f96c9a94deaa066f0264190fecf051`)
- [x] Android unsigned APK/AAB candidates, checksums, SBOM verification, and
      external signing instructions

## Acceptance

- [ ] Scenarios A-E in `GOAL.md` pass with retained evidence
- [ ] No real secrets, critical placeholders, skipped gates, or fabricated results
- [ ] Release notes and `v1.0.0` artifacts are reproducible
