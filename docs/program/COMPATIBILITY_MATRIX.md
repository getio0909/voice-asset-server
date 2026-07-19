# Compatibility Matrix

The matrix is updated only after the listed combination passes its applicable
contract and integration tests.

| Server                        | API  | Contract | Console                                   | Android                        | MCP                           | Site docs                   | Status                       |
| ----------------------------- | ---- | -------- | ----------------------------------------- | ------------------------------ | ----------------------------- | --------------------------- | ---------------------------- |
| `0.1.0-dev`                   | `v1` | `0.1.0`  | `0.1.0` CI verified                       | `0.1.0` CI verified            | `0.1.0` CI verified           | `0.1.0` CI verified         | Phase 0 verified             |
| `0.1.0-dev+phase1.20260716`   | `v1` | `0.2.0`  | `0.1.0` remote browser verified           | `0.1.0` pin/format verified    | `0.1.0` local verify          | `0.1.0` static test         | Phase 1 deployed candidate   |
| `0.1.0-dev+phase2.20260716.1` | `v1` | `0.3.0`  | `0.1.0` local verify                      | `0.1.0` JVM/format verified    | `0.1.0` local verify          | `0.1.0` static test         | Phase 2 deployed candidate   |
| `0.1.0-dev+phase3.20260716.1` | `v1` | `0.4.0`  | `0.1.0` remote browser verified           | `0.1.0` JVM pin verified       | `0.1.0` local verify          | `0.1.0` static test         | Phase 3 deployed candidate   |
| `0.1.0-dev+phase4.20260716.1` | `v1` | `0.5.0`  | `0.1.0` local browser verified            | `0.1.0` JVM pin verified       | `0.1.0` live read E2E         | `0.1.0` static test         | Phase 4 read candidate       |
| `0.1.0-dev+phase4.20260716.2` | `v1` | `0.6.0`  | `0.1.0` local browser verified            | `0.1.0` JVM pin verified       | `0.1.0` remote HTTP E2E       | `0.1.0` static test         | Phase 4 Agent candidate      |
| `0.1.0-dev+phase4.20260716.3` | `v1` | `0.7.0`  | `0.1.0` tests/lint/build verified         | `0.1.0` JVM pin verified       | `0.1.0` remote read/write E2E | `0.1.0` static test         | Phase 4 deployed candidate   |
| `0.1.0-dev+phase5.20260716.4` | `v1` | `0.7.0`  | `0.1.0` live browser verified             | `0.1.0` JVM tests verified     | `0.1.0` remote read/write E2E | `0.1.0` 24-page test        | Phase 5 acceptance candidate |
| `0.1.0-dev+phase5.20260716.5` | `v1` | `0.7.0`  | `0.1.0` remote admin E2E                  | `0.1.0` JVM tests verified     | `0.1.0` remote read/write E2E | `0.1.0` 24-page test        | Phase 5 acceptance candidate |
| `0.1.0-dev+phase5.20260716.6` | `v1` | `0.7.0`  | `0.1.0` auto-approval E2E                 | `0.1.0` JVM tests verified     | `0.1.0` live read/audit E2E   | `0.1.0` 24-page test        | Phase 5 acceptance candidate |
| `0.1.0-dev+phase5.20260717.1` | `v1` | `0.8.0`  | `0.1.0` session/catalog/detail/export E2E | `0.1.0` release build verified | `0.1.0` strict read E2E       | `0.1.0` 62-op deterministic | Phase 5 security candidate   |
| `0.1.0-dev+phase5.20260717.2` | `v1` | `0.9.0`  | `0.1.0` filter/trash/restore E2E          | `0.1.0` build/pin verified     | `0.1.0` strict read E2E       | `0.1.0` 65-op deterministic | Phase 5 lifecycle candidate  |
| `0.1.0-dev+phase6.20260717.1` | `v1` | `0.10.0` | `0.1.0` search/bulk lifecycle E2E          | `0.1.0` release build verified | `0.1.0` exact-search E2E      | `0.1.0` 65-op deterministic | Phase 6 search candidate     |
| `0.1.0-dev+phase6.20260717.2` | `v1` | `0.11.0` | `0.1.0` waveform/upload/approval E2E       | `0.1.0` full build/pin verified | `0.1.0` strict read E2E       | `0.1.0` 67-op deterministic | Phase 6 waveform candidate   |
| `0.1.0-dev+phase6.20260717.3` | `v1` | `0.12.0` | `0.1.0` purge/state-clear E2E              | `0.1.0` 42 JVM/build verified  | `0.1.0` remote read E2E       | `0.1.0` 69-op deterministic | Phase 6 deployed candidate   |
| `0.1.0-dev+phase6.20260717.4` | `v1` | `0.12.0` | `0.1.0` purge/state-clear E2E              | `0.1.0` 42 JVM/build verified  | `0.1.0` `.3` read E2E         | `0.1.0` 69-op deterministic | Phase 6 metrics candidate    |
| `0.1.0-dev+phase6.20260717.5` | `v1` | `0.12.0` | `0.1.0` purge/state-clear E2E              | `0.1.0` 42 JVM/build verified  | `0.1.0` `.3` read E2E         | `0.1.0` 69-op deterministic | Phase 6 monitored candidate  |
| `0.1.0-dev+phase6.20260717.6` | `v1` | `0.13.0` | `0.1.0` operations/a11y E2E                | `0.1.0` full build/pin verified | `0.1.0` `.4` read E2E         | `0.1.0` 72-op deterministic | Phase 6 admin candidate      |
| `0.1.0-dev` (local)           | `v1` | `0.14.0` | `0.1.0` 89 tests/workspace a11y             | `0.1.0` 71 JVM/build verified | `0.1.0` local verify          | `0.1.0` 77-op deterministic | Phase 6 workspace candidate  |
| `0.1.0-dev` (local)           | `v1` | `0.15.0` | `0.1.0` 95 tests/account/version a11y       | `0.1.0` 73 JVM/build verified | `0.1.0` local verify          | `0.1.0` 78-op deterministic | Phase 6 account candidate    |
| `0.1.0-dev` (local)           | `v1` | `0.16.0` | `0.1.0` 95 tests/contract a11y              | `0.1.0` 109 JVM/32 compiled   | `0.1.0` local verify          | `0.1.0` 79-op deterministic | Phase 6 mobile compatibility candidate |
| `0.1.0-dev+phase6.20260718.1` | `v1` | `0.17.0` | `0.1.0` 95 tests/6 mocked Chromium          | `0.1.0` 112 JVM/32 compiled  | `0.1.0` local verify          | `0.1.0` 80-op deterministic | Phase 6 deployed mobile retry candidate |
| `0.1.0-dev` (local)           | `v1` | `0.18.0` | `0.1.0` 105 tests/pairing fallback          | `0.1.0` 117 JVM/35 compiled  | `0.1.0` verify/race           | `0.1.0` 82-op deterministic | Phase 6 local device pairing candidate |
| `0.1.0-dev+phase6.20260718.3` | `v1` | `0.18.0` | `0.1.0` 105 tests/6 mocked Chromium          | `0.1.0` 117 JVM/35 compiled  | `0.1.0` verify/race           | `0.1.0` 82-op deterministic | Phase 6 deployed device pairing candidate |
| `0.1.0-dev` (local follow-on) | `v1` | `0.18.0` | `0.1.0` 105 tests/6 mocked Chromium          | `0.1.0` 134 JVM/41 compiled  | `0.1.0` verify/race           | `0.1.0` 82-op deterministic | Phase 6 Android reconnect candidate |
| `0.1.0-dev+phase6.20260718.4` | `v1` | `0.19.0` | `0.1.0` 107 tests/7 mocked Chromium          | `0.1.0` 134 JVM/41 compiled  | `0.1.0` test/vet/build        | `0.1.0` 83-op deterministic | Phase 6 deployed safe-settings candidate |
| `0.1.0-dev+phase6.20260718.5` | `v1` | `0.20.0` | `0.1.0` 107 tests/build                      | `0.1.0` JVM/lint/APK/AAB/41 compiled | `0.1.0` test/vet/build        | `0.1.0` 84-op deterministic | Phase 6 deployed personal-notification candidate |
| `0.1.0-dev+workspace.20260718.11` | `v1` | `0.22.0` | `0.1.0` 110 tests/build/Webhook UI | `0.1.0` JVM/lint/APK/AAB/43 compiled | `0.1.0` test/vet/build | `0.1.0` 91-op deterministic | Phase 6 merged realtime/Webhook/S3/OTel release candidate |
| `0.1.0-rc.5` | `v1` | `0.22.0` | `0.1.0` hosted OCI/static archive | `0.1.0` signed APK/AAB + hosted tests | `0.1.0` hosted archive | `0.1.0` hosted archive | Hosted draft release candidate; v1.0 product gates remain open |

Contract `0.22.0` additionally passed migration 18, the Webhook repository,
WebSocket transport tests, and authenticated acceptance on the isolated `10443`
deployment. Contract `0.20.0` passed migration 17 against real PostgreSQL,
including deterministic backfill, retry history, transaction rollback, tenant
and recipient isolation, ordering, and row immutability. A verified 42-object/
42-file pre-cutover backup restored into a disposable database and upgraded to
schema 17 before deployment. Strict public-TLS acceptance on 10443 then proved
Session-only access, API-key denial, a 35-event safe-field history, stable empty
checkpoints, workspace/user cursor binding, tamper rejection, method handling,
safe read auditing, logout, and post-logout denial. Public Caddy and the
certificate-reusing gateway retained their PIDs, configuration hashes, and zero
restart counts.

The same `0.22.0` candidate also passed the isolated S3 lifecycle and
clean-instance backup/restore gates. The restore copied an original and an
unfinished upload part into a new destination prefix, matched both SHA-256
values and database inventories, and removed the temporary endpoint,
credentials, database, and object data afterward.

Current merged-branch evidence: Server CI `29655623363`, Workspace Compatibility
`29655623342`, Console CI `29655632775`, Android CI `29655641245`, MCP CI
`29655652283`, and Site CI `29655685955` all passed against their merged
default-branch commits. The signed Android candidate run `29655228357` passed
all six jobs and retains the unexpired `voice-asset-signed-release` artifact.

Evidence: Server run `29472180011`, Console `29472656283`, Android
`29473983934`, MCP `29472179992`, and Site `29472658617` all completed
successfully on their default branches. The Phase 1 candidate also passed an
external Chromium workflow through `https://api.getio.net:10443` on Debian 12
with PostgreSQL 15, separate systemd units, loopback-only API/database ports,
and Caddy internal-CA TLS. The contract `0.3.0` candidate additionally passed
local verification in Server, Console, Android JVM modules, MCP, and Site, plus
a real AAC/M4A upload, Mock transcription, and byte-range playback through the
isolated Debian deployment. Contracts `0.2.0`, `0.3.0`, and `0.4.0` remain candidates,
not supported CI rows: Android application compilation/runtime integration,
Docker Compose execution, and hosted default-branch CI for the uncommitted
slices have not run. Contract `0.4.0` passes the Server's real-PostgreSQL
correction/review E2E, every client repository's contract-pin gate, and a real
remote Console upload -> Mock ASR -> correction -> partial review -> approval
workflow through the isolated Debian gateway.
Contract `0.5.0` adds real-PostgreSQL cursor/audit coverage, all client pin
gates, an isolated Console-to-MCP search/revision/time-range E2E, and a strict
TLS remote deployment smoke test. It is a read-slice candidate, not evidence
that all Phase 4 write tools, Resources, or Prompts exist. Contract `0.6.0`
adds hashed scoped API keys, lifecycle/audit tests, all five repository pin
gates, and an official-SDK remote Streamable HTTP test through strict TLS.
Contract `0.7.0` adds collection, tag, annotation, processing-status, metadata,
clip, and transcript-export APIs. The official MCP SDK discovered all 21 tools
over strict TLS, then proved real clip and WebVTT creation, authenticated
HEAD/GET/Range, SHA-256 verification, Agent attribution, read-only scope denial,
and old-key revocation. Migrations 1–7 and the `.3` binaries run on the isolated
Debian host without restarting either Caddy process.
The `.4` candidate adds manifest-based backup/verification/clean restore,
Console gateway/Compose packaging, complete bilingual product and operator docs,
and draft release pipelines. A real recovery drill matched 30 table row counts
and 13 file hashes; a subsequent rollback/deploy exercise left both Caddy PIDs
unchanged. Hosted CI, Docker runtime, and Android application/device gates remain
open, so this row is not yet a supported release combination.
The `.5` candidate adds bounded expired-artifact reaping and deployed Console
API-key, ASR Provider/Hotword, and LLM Profile/Glossary administration. Real
remote E2E removed one
expired artifact while preserving its source Revision and audit, created and
revoked a least-privilege key with zero browser persistence, and loaded all
three credential-free ASR capability models, both LLM capability models, and
their admin inventories. Disposable real-PostgreSQL browser coverage also
publishes hotword and glossary versions and manages Mock ASR/LLM profiles. Both
Caddy PIDs again remained unchanged. The same hosted CI,
Docker, and Android gates remain open, so this row is not yet supported.
The `.6` candidate adds fail-closed `validated_glossary_only` auto-approval.
Real PostgreSQL proves the atomic corrected -> human-edited -> approved lineage,
automated review/audit attribution, and duplicate-approval rejection. The live
orchestrator then passed three Chromium workflows plus MCP read/audit checks;
the deployed build passed strict TLS and a read-only provider smoke after a
successful rollback exercise. MCP and both Caddy PIDs remained unchanged. The
hosted CI, Docker, and Android gates remain open, so this row is not yet
supported.
Contract `0.8.0` adds rotating browser refresh credentials and personal device
session inventory/revocation through migration 8, plus DNS-pinned provider
connections. All five repositories pin `0.8.0`; Server tests and a direct Linux
race run pass, Console passes a real deployed cookie-rotation/revocation flow,
MCP passes an official-SDK strict-CA read smoke, and Site verifies its generated
62-operation reference. The isolated Debian deployment runs the `.1` candidate
with zero service restarts and unchanged public/isolated Caddy PIDs. Android's
42 JVM tests, application/Room/lint builds, unsigned release APK/AAB verifier,
and 141-component SBOM gate pass, but emulator E2E and external signing remain
open, so this row is not yet a supported release combination. Its
strict-TLS control-plane performance smoke also passed 400 requests at
concurrency 8 with zero failures, 42.8 req/s, and 217.871 ms p95.
The matching Console detail/export candidate additionally passes 61 unit tests,
authenticated audio, bounded processing/annotation reads, note/bookmark creation,
an immutable Revision export with verified downloaded bytes/SHA-256, and a real
deployed title-search/detail/metadata round trip that restores its source asset.
Its deterministic archive SHA-256 is
`41fe4b600d8bbe9f00f7291915c609f92b22cb8014193a225898ce4b6f72613f`;
both Caddy PIDs remained unchanged and the post-deploy error journal was empty.
Contract `0.9.0` adds query-bound Collection, Tag, status, and UTC date filters,
assigned-tag reads, and optimistic audited trash/restore through migration 9.
All five pins and local gates pass; real PostgreSQL covers filters, workspace
isolation, lifecycle concurrency, audits, and upgrades from versions 1–8. The
isolated `.2` deployment passes a failure-safe Chromium filter/metadata/trash/
restore round trip and official-SDK strict-CA MCP reads. Its deterministic
Server and Console archive SHA-256 values are
`413f7c07ec63c75355dbc3975b2970937c24d6bd08c54d59a35a394217799b02` and
`a8c1dd4fb92e7d574495952eabe4457dd0d2585a6edfb6e75405fbcc09bba7cc`;
public Caddy PID `18314` and gateway PID `96419` remained unchanged. Hosted
default-branch CI, Docker, and Android device gates remain open.
Contract `0.10.0` adds PostgreSQL full-text title/latest-Revision Segment
search, literal fallback, ASR Provider and case-insensitive Speaker filters,
and at most five chronological timecoded hits through migration 10. Every
version 1–9 upgrades in a disposable real database, and live schema inspection
confirms both generated vectors plus all five supporting indexes. The isolated
`.1` deployment passes strict TLS, a full upload/Mock ASR/search/manual and
automatic approval Chromium workflow, lifecycle/export/provider regressions,
an official-SDK strict-CA MCP smoke, and an official-SDK exact Segment/timecode
search. Server and Console archive SHA-256 values are
`57fb8e86eac9c31d29145563b516565eaf0121814cef61cef624c7f841c74ef0` and
`20fa1e08bd6a162792626aa780f865b8c046560b1dd10d06e52f747b3788fc41`;
public Caddy PID `18314` and gateway PID `96419` remain unchanged with zero
post-cutover error journal entries. Hosted default-branch CI, Docker, Android
device execution, and waveform gates remain open. The follow-on Console bundle
also passes a real metadata round trip plus bulk trash/restore with exact
versions and source-state cleanup. Its deterministic archive SHA-256 is
`08a8c2ffd4f5078825b31187f1c54767b136db5a404a677cd609b4c87d3ba5a5`;
all service and Caddy PIDs remained unchanged with zero restarts.
Contract `0.11.0` adds one immutable 1600x256 waveform PNG per original through
migration 11, bounded deterministic FFmpeg rendering, authenticated integrity-
checked GET/HEAD delivery, and pointer/keyboard seek plus 0.75–2x playback in
Console. Versions 1–10 upgrade in isolated real PostgreSQL schemas; the deployed
`.2` candidate backfilled all nine existing originals, then two real Chromium
uploads brought the verified state to 11 originals, 11 waveforms, 11 succeeded
jobs, zero failures/pending jobs, and six read audits. The complete browser flow
verified the PNG signature, decoded waveform, playback speed, search, Mock ASR,
manual review, and automatic approval. Strict-CA control-plane and official-SDK
MCP reads pass; the deployment bundle SHA-256 is
`3084cf2769e9ffeff0b37b2f61fbbf83815cc4d8b421dba078143f37a11a0a6f`.
Public Caddy PID `18314` and gateway PID `96419` remained unchanged with zero
service restarts and no post-cutover error-priority journal entries. Hosted
default-branch CI, Docker, Android device execution, and S3 storage remain open.
An isolated fully migrated PostgreSQL schema with 5,000 seeded assets also
passed 100 concurrent production-service asset/audit creates at 900.6 req/s and
41.758 ms p95 plus 400 list/title-search reads at 194.2 req/s and 54.092 ms p95;
the schema was removed after the run.
Contract `0.12.0` is a deployed candidate that adds owner-only, exact-confirmation
permanent purge through migration 12. Its durable job deletes an integrity-
checked, fingerprinted object inventory before removing the relational graph,
retains audits, and resumes safely after terminal failures. Disposable real-
PostgreSQL schemas pass fresh/repeated/down migrations, upgrades from versions
1–11, legacy and waveform-trigger preservation, purge completion, integrity
rejection, and resume. All five repository pins and local gates pass, including
the Console's 69 tests and mocked accessible purge flow, Android's 42 JVM tests
plus lint/Debug APK, MCP verify, and Site's generated 69-operation reference.
The isolated Debian services now run `.3`/`0.12.0`/migration 12. A strict-TLS
Chromium flow uploaded and transcribed a dedicated asset, trashed it, submitted
its exact UUID, observed a succeeded durable purge, and left the deployment at
its pre-test 11 assets/42 objects with both object files absent and both purge
audits retained. Browser cookie/Web Storage were empty after sign-out. The
official MCP SDK passed unauthenticated 401, 21-tool discovery, capability, and
asset-list calls. The verified archive SHA-256 is
`34eb142be272356522a2fc80346944be39f4247835401b3dc7be0450b7fec1c2`;
both Caddy PIDs remained unchanged and all service restart counts are zero.
Hosted CI, Docker, Android device execution, and SDK-backed S3 remain open, so
this row is not a supported release combination.
The `.4` Server-only follow-up adds bounded Prometheus HTTP metrics and
Info-level status/latency logs with safe request IDs. Local tests/vet/build and
an exact-tree Linux race run pass. Direct-loopback scrape, raw-path/query
exclusion, method rejection, structured-log redaction, and public-gateway
non-exposure pass on the isolated host; MCP remains on the verified `.3` build.
The archive SHA-256 is
`21b59922f3bc8aa3679f7e98cd4fb37b5583ceebe57ca4c67ebed94faefe0abb`;
both Caddy processes and MCP remained unchanged with zero restart counts and no
error-priority journals. The `.5` follow-up replaces duration sums/counts with
fixed cumulative histogram buckets and deploys checksum-pinned Prometheus 3.13.1
on `127.0.0.1:19090` with 7-day/1-GiB retention plus four unit-tested alert
rules. Both scrape targets and all rules are healthy, TSDB history survives
restart, the listener and API metrics stay outside the gateway, and the archive
SHA-256 is
`8f2ff94d780bdefcbd95a98ec63db085a5fd94e5f1a4cdd1d1fec0cd10f04dc7`.
OpenTelemetry, alert notification delivery, hosted CI, Docker, Android device
execution, and SDK-backed S3 remain open.
Contract `0.13.0` adds workspace-scoped, `admin:read` Job, Audit Log, and System
Status read models with filter-bound cursors, reduced response fields, and
fail-closed read audits. Server unit and disposable real-PostgreSQL tests,
Console's 76 unit tests and mocked browser flow, Android's full build/pin gate,
MCP verification, and Site's generated 72-operation reference pass. The
isolated `.6` Server/Worker and `.4` MCP deployment passes strict TLS, official
SDK 21-tool discovery/read, and a real authenticated Chromium administration
flow with no unexpected writes, browser token storage, or axe violations. The
combined archive SHA-256 is
`447031b5a7e28e65b6dbb70178f0a4c9ac0dfc623d8d83ee50f9a577a89a446c`;
both Caddy PIDs remain unchanged and all candidate-service restart counts are
zero. This remains an unsupported candidate until the broader product and
publication gates close.
Contract `0.14.0` adds audited workspace member inventory, Owner-only local-user
creation and conditional role/status updates, last-active-Owner safety, and
credential revocation on disable, plus audited workspace profile reads and
Owner-only exact-ETag renames. Server ordinary and disposable real-
PostgreSQL migration 1–14/lifecycle tests, Console's 89 unit tests plus five
default mocked Chromium flows, Android's 71 JVM/lint/debug build, MCP verify,
and Site's generated 77-operation bilingual reference pass locally. This row is
uncommitted and undeployed: the isolated environment intentionally remains on
`0.13.0`/migration 12 until the preceding migration 13 real-time adapter is
complete. It is not a v1.0 release row.
Contract `0.15.0` adds session-only personal password rotation with current-
password verification, atomic PBKDF2 replacement, cross-workspace all-session
revocation, credential-free audit metadata, and an independent rate limit.
Server unit/HTTP/OpenAPI/full-Go and disposable real-PostgreSQL tests, Console's
95 unit tests plus six default mocked Chromium flows, Android's 73 JVM/Ktlint/
lint/debug build, MCP verify, and Site's generated 78-operation bilingual
reference pass locally. This row is also uncommitted and undeployed; the isolated
environment remains on `0.13.0`/migration 12. It is not a v1.0 release row.
Contract `0.16.0` adds transaction-written, workspace-isolated ordered asset
changes with a fixed high-watermark cursor, immutable mutation snapshots, and
permanent-deletion tombstones through migration 15. Real PostgreSQL proves
backfill, tenant isolation, transaction visibility/rollback, and upgrades from
versions 1–14. Console's 95 tests plus six mocked Chromium flows, Android's 109
JVM tests/full build plus compilation of 32 instrumentation methods, MCP's
coverage/vet/build, and Site's generated 79-operation bilingual reference pass
locally. Android Room migrations 2 through 4 atomically persist page/cursor
state, retry generations, and per-recording policy overrides; secure local
playback and export fail closed on identity, path, size, or integrity drift. The
same bounded offline projection now supports one case-insensitive query across
cached asset and local recording identity/metadata/status/error fields. Cached
asset metadata updates use the latest strong ETag, fail closed on conflicts, and
refresh Room without advancing the incremental cursor. On explicitly compatible
servers without `incremental_sync`, Android now follows the stable asset-list
cursor through a bounded bootstrap and merges only newer versions without
resurrecting tombstones or inferring deletion from absence. A redacted strict-TLS
smoke verified that path's real `0.13.0`/10443 page shape and continuation cursor.
At the 0.16 checkpoint, the same Android candidate could explicitly read the deployed administration
status, a bounded credential-free job page, and ASR/LLM Profile states. Exact-
version Profile enable/disable is locally typed and was remotely exercised with
the original LLM state restored, the temporary session revoked, and both Caddy
services still active with zero restarts. Family-specific Provider health is
also locally typed and strictly validated; a separate deployed Mock LLM check
returned `healthy`, revoked its temporary session, and left both Caddy services
active with zero restarts. QR pairing, job retry, and broader safe configuration
remain outside this supported row. A
dependency-free workspace gate executes the Server's real offline capability output and verifies all
five pins, Console/Android required features, MCP API identity, and the Site's
byte-identical OpenAPI copy. Its four negative fixtures cover repository-count,
missing-capability, schema-constant drift, and contract-copy drift failures. This row
was uncommitted and undeployed; the isolated application remained on `0.13.0`/
migration 12. Only the independent gateway was switched to the existing public
certificate, with public Caddy unchanged. It is not a v1.0 release row.
Contract `0.17.0` adds an `admin:write`, workspace-scoped retry boundary for
eligible failed jobs. It reuses the same Job UUID, preserves the attempt count,
raises the hard-bounded maximum by exactly one, clears stale failure/lease data,
and commits any required asset lifecycle transition plus credential-free audit
in one transaction. Server unit/HTTP/OpenAPI and disposable real-PostgreSQL
tests pass, as do Console's 95 tests and six mocked Chromium flows, Android's
112 JVM tests/full build plus 32 compiled instrumentation methods, MCP
coverage/vet/build, Site's deterministic 80-operation reference, and five
cross-repository compatibility fixtures. The isolated 10443 deployment now
runs this row on schema 15; a disposable real retry and cleanup passed while
the existing public certificate and public Caddy configuration remained
unchanged. Hosted CI and Android device execution remain open, so this is not a
v1.0 release row.
Contract `0.18.0` adds personal-session-only device pairing. Issuance revokes
older unclaimed payloads for the same user/workspace, stores only a SHA-256
secret digest, and returns one five-minute capability. Claim is exact-Origin
and per-IP rate limited; it atomically verifies the active user/membership,
consumes the capability, creates the named session, and writes credential-free
audits. Invalid, expired, revoked, and replayed claims share one error. Server
full real-PostgreSQL tests, race, vet, builds, OpenAPI lint, migration upgrades,
and workspace compatibility pass. Console's 105 tests/typecheck/lint/build,
Android's pairing checkpoint passed 117 JVM tests/full build plus 35 compiled instrumentation methods,
  MCP coverage/vet/build/race, and Site's 82-operation static gate pass. The
  current dependency-free UI uses a masked Console copy and Android paste; QR
  rendering/scanning still requires explicit production-dependency approval.
  The isolated 10443 deployment now runs `.20260718.3` on schema 16 after two
  independently verified backups and an isolated restore/migration drill. Its
  strict-TLS acceptance passed issue, exact-Origin rejection, claim, session
  inventory, replay rejection, logout, hash-at-rest, and log-redaction checks.
  Public Caddy and the certificate-reusing independent gateway were unchanged.
The local Android follow-on now encrypts and rotates the complete access/refresh
session, adds two-step exact-UUID device revocation, and replaces a revoked or
expired session on the exact existing Profile without changing its offline
identity. All 134 JVM tests and the full build pass, and 41 instrumentation
methods compile. The `.20260718.3`
Server/Console/MCP/Site row is deployed, but the Android follow-on remains
uncommitted. Hosted CI and physical-device pairing remain open, so neither row
is a v1.0 release row.
The same Console gate includes a credential-free Version Information view over
the fail-closed capability store. ADR 0013 explicitly keeps deployment-global
settings outside workspace Owner authority. Contract `0.19.0` adds only an
audited `admin:read` allowlist of eight runtime facts. All mutations return
`405`, queries return `400`, and neither API nor Console reads the global table
or exposes paths, endpoints, credentials, or tokens. Server ordinary tests/vet/
build/OpenAPI, Console's 107 tests and seven mocked Chromium flows, Android's
134 JVM tests/full build with 41 compiled instrumentation methods, MCP tests/
vet/build, Site's 83-operation/51-page gate, and seven compatibility fixtures
pass. The isolated host runs `.20260718.4` on schema 16 after a verified
42-object/42-file backup and strict-TLS 401/read/deny/audit/logout acceptance.
Both Caddy processes/configurations and the reused certificate remained
unchanged. Hosted CI, the current Windows race rerun, and Android physical-
device execution remain open, so this is not a v1.0 release row.
The supported local pipeline additionally passed eight concurrent two-part
5.24 MiB WAV upload/storage/probe operations at 499.027 ms p95, eight Mock
Worker/transcript publications at 107.040 ms p95, and 32 full-hash audio opens
at 32.332 ms p95, all with zero failures.
Two consecutive real FFmpeg runs each generated twelve 30-second mono 16 kHz
clips at concurrency 3 with zero failures and complete temporary-file cleanup.
The latest reached 12.9 operations/second and 454.337 ms p95. S3-compatible
storage and a future rendition path remain outside the supported combination.
Real PostgreSQL tests independently upgrade schema versions 1–8 to version 9.
A staged v1 → v2 → v9 run also preserves representative legacy records,
verifies the existing access-session backfill without creating a refresh token,
and assigns a recoverable prior state to legacy trashed assets.
Server and MCP release scripts each passed two complete Linux, Windows, and
macOS AMD64/ARM64 builds with identical per-archive hashes. Verification checks
safe archive layout, contract pins, Go target metadata, embedded versions, host
version commands, and complete SHA-256 coverage. Hosted Tag/SBOM and container
image gates remain open. Console and Site static bundles also pass independent
double-build comparison; the Site normalizes Pagefind's generated language-map
ordering before packaging so byte equality is intentional rather than
scheduler-dependent.

## Current Coordinated Candidate

The `v0.1.0-rc.6` tags point at Server `2a9dab4`, Console `ed5b7f8`, Android
`2e22dc5`, MCP `8d34906`, and Site `7c0a665`. Release workflows for all five
repositories passed (`29671512394`, `29671512945`, `29671513656`, `29671514283`,
and `29671514861`) and published draft artifacts with SBOMs and SHA256SUMS.
The Android Hosted Emulator run `29667693126` passed all 44 instrumentation
tests; physical-device recording, process-death, and network-recovery evidence
remain open, so this is not a v1.0 release row.

## Workspace Compatibility Gate

Run `make compatibility` from `voice-asset-server` with all five sibling
repositories present. The command first runs seven isolated fixture tests, then
executes `adminctl capabilities` and checks the actual Server API/contract and
sorted capability set against Console, Android, MCP, and Site declarations.
`.github/workflows/workspace-compatibility.yml` performs the same check by
checking out the four public client repositories. The current default-branch
merge run `29672081074` passed the live compatibility check; Server-only pull
requests safely fall back to each client repository's `main` when a shared ref
does not exist. Historical pre-0.22 rows remain unsupported unless their own
recorded evidence says otherwise.

## Policy

- A client records the exact contract version used for generation or testing.
- Additive OpenAPI changes preserve the API namespace and increment the contract
  minor version; corrections increment its patch version.
- Breaking wire changes require a new API namespace and an ADR.
- Clients must ignore unknown capability values and fail closed when a required
  capability or scope is absent.
- A row becomes supported only after cross-repository CI or a recorded release
  candidate run proves it.
