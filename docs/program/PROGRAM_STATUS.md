# VoiceAsset Program Status

- Last updated: 2026-07-19 16:44 UTC

- Android recorder-first CI acceptance (2026-07-19 16:03 UTC): PR #13 branch
  `agent/recorder-first-ux` at commit `d060892` passed GitHub Actions run
  `29693853885` end-to-end. The run passed all 51 Hosted Emulator
  instrumentation tests, validation, signed release-candidate build,
  dependency review, supply-chain/SBOM/license checks, and secret scan. The
  installable signed APK/AAB plus `SHA256SUMS` are in the
  [signed-release artifact](https://github.com/getio0909/voice-asset-android/actions/runs/29693853885/artifacts/8444422590);
  the debug APK is in the [debug artifact](https://github.com/getio0909/voice-asset-android/actions/runs/29693853885/artifacts/8444432250),
  and the Android SBOM is in the [SBOM artifact](https://github.com/getio0909/voice-asset-android/actions/runs/29693853885/artifacts/8444393667).
  The signed-release artifact ZIP digest is
  `14a1c91a7a1cd32e396fb8763b40954a9fc0771dc7c526a61e00454abd3f93b6`.
  Physical-device microphone, reboot/process-death, and complete scenario-B
  evidence remain open for the user's next test session; this is not a final
  `v1.0.0` publication.

- Android post-merge CI acceptance (2026-07-19 16:19 UTC): PR #13 is merged
  into Android `main` as `d6c8df69553ed3338a98520bedc9909bcb64e3b5`.
  Main-branch run `29694391878` passed validation, all 51 Hosted Emulator
  instrumentation tests, supply-chain checks, and secret scanning. The main
  workflow intentionally skips the duplicate signed-candidate and dependency
  review jobs; the signed APK/AAB artifact remains retained from the fully
  green PR run above. Physical-device and final scenario-B evidence remain
  open.

- Current workspace gate (2026-07-19 16:22 UTC): Server `go test ./...` passed
  all command, internal, and E2E/performance packages. `make compatibility`
  passed all eight negative/positive fixtures and the live five-repository
  contract check: five repositories, API v1, contract `0.22.0`, 45 Server
  features, 32 Console features, 6 Android features, and an exact Site
  OpenAPI copy.

- Android local post-merge gate (2026-07-19 16:23 UTC): Android `main` at
  `d6c8df6` passed `ktlintCheck`, `:app:testDebugUnitTest`, `:app:lintDebug`,
  and `git diff --check` on the workstation. No device was attached, so this
  local check does not replace the explicit physical-device acceptance gate.

- Android acceptance evidence documentation (2026-07-19 16:44 UTC): PR #14
  merged as `5bad9912a1c81245fd7a0cc910a7e7b41c5992d`. The README now provides
  a credential-free UTC evidence template for device/build details, offline
  recording, playback/export, pairing, recovery, and reconnect results. It
  explicitly excludes passwords, pairing URIs, tokens, session IDs, and raw
  server responses; it is documentation only and does not count as test
  evidence itself.

- Independent 10443 live recheck (2026-07-19 16:08 UTC): the read-only
  `https://api.getio.net:10443/readyz` endpoint returned HTTP 200; `/version`
  still reports Server `0.1.0-dev+workspace.20260718.12` at commit
  `294334993a64d6ccacb675d814e23441ee438830`; and capabilities report API v1,
  contract `0.22.0`, and the expected feature set. Strict TLS handshakes on
  ports 443 and 10443 present the same `CN=api.getio.net` certificate,
  expiring `2026-10-15T19:52:38Z`, SHA-256
  `8CAF123ADD29ECA48BB2A9D2D40185A74589BBD291F8FD732990479CC71DE0FF`.
  This was read-only and did not reload or modify either Caddy configuration.

- Cross-repository local verification (2026-07-19): Server `go test ./...` and
  MCP `go test ./...` passed. Console `pnpm verify` passed formatting,
  contract pin, lint, 22 Vitest files/110 tests, typecheck, production build,
  and license checks. Site `pnpm test` passed Astro diagnostics, contract and
  generated API reference checks, English/Chinese parity, static build,
  accessibility, links, and license checks. No generated artifacts or secrets
  were left tracked. These checks validate the current clean Console/MCP/Site
  branches; the Android recorder changes are committed on PR #13's feature
  branch and still await physical-device acceptance.

- Additional CI-equivalent checks (2026-07-19): Server `go vet ./...`,
  `go build ./cmd/...`, MCP `go vet ./...`, `go build ./cmd/voice-asset-mcp`,
  Android `git diff --check`, and the eight-case workspace compatibility suite
  all passed. The compatibility verifier confirmed five repositories, API v1,
  contract `0.22.0`, 45 Server features, 32 Console features, 6 Android
  features, and an exact Site OpenAPI copy.

- Android CI parity follow-up (2026-07-19): the newly added decoder-choice
  Compose regression test compiles with `:app:compileDebugAndroidTestKotlin`.
  `ktlintCheck` initially found formatting violations in the recorder changes;
  those were corrected, and the final `ktlintCheck`, AndroidTest compilation,
  `:app:testDebugUnitTest`, `:app:lintDebug`, `:app:assembleDebug`, and
  `git diff --check` all pass. The refreshed debug APK is
  `app/build/outputs/apk/debug/app-debug.apk` with SHA-256
  `AA5C47EBCB69BEC76C9A6E754349E77FCBD18CF1A1BA87BB99AD16CE5B973C09`.
  Instrumentation was compiled but not executed because the phone is
  disconnected.

- External acceptance preflight (2026-07-19 13:44 UTC): the independent test
  endpoint `https://api.getio.net:10443` returned `/readyz` HTTP 200,
  `/version` Server `0.1.0-dev+workspace.20260718.12` at commit
  `294334993a64d6ccacb675d814e23441ee438830`, and capabilities API v1/contract
  `0.22.0` with 45 features. The checks were read-only and did not reload or
  modify Caddy. Docker is not installed on this workstation and `adb devices`
  currently lists no attached device; GitHub CLI authentication is available
  for a later explicitly requested push/CI operation.

- Android recorder-first UX follow-up (PR #13 feature branch, 2026-07-19): navigation is
  now a single top-bar flow (recordings, record, settings) with search,
  filters, sorting, a single prominent record action, and language kept inside
  Settings. Playback rows expose one stateful play/pause/stop control, and
  Settings exposes system-default, hardware-preferred, and compatibility
  decoder choices. Hardware preference is best-effort and safely falls back to
  Android's system player; it does not claim direct MediaCodec selection.
  `:app:compileDebugKotlin`, `:app:compileDebugAndroidTestKotlin`,
  `:app:testDebugUnitTest`, `:app:lintDebug`, `:app:assembleDebug`, and
  `git diff --check` passed. The current debug APK is
  `app/build/outputs/apk/debug/app-debug.apk` with SHA-256
  `F5218C26F8A139F3A21709EC6963BEA940DDF654FA635B1ADC942061A83FF3D0`.
  Physical-device instrumentation, microphone capture, and playback were not
  rerun after the user unplugged the phone and remain explicit acceptance
  items for the next test session.

- Independent 10443 live recheck: at `2026-07-19T11:39:52Z`, an external
  Windows client using the operating-system trust store received HTTP 200 from
  `/readyz` and `/api/v1/system/capabilities`; `/version` reported Server
  `0.1.0-dev+workspace.20260718.12` at commit
  `294334993a64d6ccacb675d814e23441ee438830`, with contract `0.22.0`. This was
  read-only and sent no credentials; no service restart, gateway reload, or
  change to the public Caddy configuration was performed.

- Independent TLS boundary recheck: at `2026-07-19T11:54:51Z`, strict
  system-trust handshakes to `api.getio.net:443` and `:10443` both presented
  `CN=api.getio.net`, expiry `2026-10-15T19:52:38Z`, and SHA-256
  `8CAF123ADD29ECA48BB2A9D2D40185A74589BBD291F8FD732990479CC71DE0FF`.
  This was read-only; no certificate, gateway, or public Caddy configuration
  was changed.

- Android recorder-first UX update: Android PR #12 was squash-merged as
  `049d9cc` after PR CI `29684050069` passed all six checks. The home screen
  now opens on a local-first recorder surface with a large record action,
  elapsed timer, animated waveform, pause/resume/stop-save controls, and
  secondary recording-policy options. English and Simplified Chinese can be
  selected from a persisted language menu. Post-merge Android main CI
  `29684280677` passed validation, secret scan, supply-chain checks, and all
  49 Hosted Emulator instrumentation tests, including the host-driven process
  death seed/verify pair (`OK (1 test)` each). Physical-device microphone,
  reboot, and complete scenario-B evidence remain open.

- v1.0.0 release-candidate audit: annotated `v1.0.0-rc.2` tags target Server
  `0f5cf21`, Console `dee765a`, Android `049d9cc`, MCP `8d34906`, and Site
  `1ff291c`. Release workflows `29684731273`, `29684732759`, `29684734369`,
  `29684735798`, and `29684737106` all passed and created draft prereleases
  with archives, OCI bundles, checksums, SBOMs/licenses, and signed Android
  APK/AAB. The Android APK checksum is
  `102ebeb4abd4cf6602404b650d7ca31008c35f4eb7123e09882bd7d8f2542680`.
  These are testable release candidates, not a final `v1.0.0` publication;
  physical-device evidence and the final A–E gates remain open.

- Android Hosted Emulator recovery gate: Android PR #10 was squash-merged as
  `09cad43` after PR CI `29680433670` passed all six checks. Its API 35 run
  completed 48/48 instrumentation tests, then installed both APKs and ran a
  host-driven seed (`OK (1 test)`), `adb shell am force-stop
  com.voiceasset.android`, and a fresh-process verify (`OK (1 test)`). The
  production `MediaRecorder` M4A, persisted `Starting` → `Recording` state,
  startup `RecordingRecovery`, and duration/size/SHA-256 checks are covered;
  post-merge Android main CI `29680652575` also passed with the same 48-test
  plus seed/verify evidence. Physical-device microphone/reboot and complete
  scenario-B evidence remain open and are not inferred from Hosted Emulator CI.

- Candidate artifact audit: the annotated `v0.1.0-rc.7` tags target the current
  default-branch commits Server `7271011`, Console `ed5b7f8`, Android `09cad43`,
  MCP `8d34906`, and Site `7c0a665`. Release workflows
  `29681441081`, `29681442803`, `29681444319`, `29681445958`, and `29681447473`
  all passed. The draft prereleases contain the expected archives, OCI bundles,
  APK/AAB, SBOMs, licenses, and `SHA256SUMS`; the signed Android APK digest is
  `143dc8f70f73a4568ba5bb4cd8021aa50cae81571ea3ce887888452861f5ea13`, and
  the Server/Console OCI bundle digests are
  `8e71b9c9bd04b5f08a1f9fb3f77e3ac9856d075f9098d744af6b586feb2985dd` and
  `335dfe80978cfe0175bf74589babeace65b29ea602606c00dd328dadb93cbcf1`.
  These are reproducible test candidates, not a `v1.0.0` publication: physical
  device evidence and final release gates remain open.

- Independent 10443 read-only recheck: at `2026-07-19T06:16:45Z`,
  `https://api.getio.net:10443/readyz` returned HTTP 200,
  `/version` returned Server `0.1.0-dev+workspace.20260718.12` at deployed
  commit `294334993a64d6ccacb675d814e23441ee438830`, and the public
  capabilities endpoint returned HTTP 200 with contract `0.22.0`. That
  deployed commit is an ancestor of current Server `main` `0262775`; later
  differences are CI and documentation only. A strict system-trust TLS
  probe observed the same certificate SHA-256 on `api.getio.net:443` and
  `:10443` (`8caf123add29eca48bb2a9d2d40185a74589bbd291f8fd732990479cc71de0ff`).
  This was read-only and did not reload or modify either Caddy configuration.

- Android real-recording gate: Android PR #7 was squash-merged as
  `3b5137ff` after Hosted PR CI `29675019396` passed all six checks,
  including a production `MediaRecorderEngine` test that records, pauses,
  resumes, and verifies a readable M4A with positive duration. The first
  attempt (`29674799547`) exposed an API-order bug on API 35; the recorder
  now configures privacy sensitivity before output format. PR #8 recorded
  this acceptance change in `CHANGELOG.md` and merged as `6d7ea02`. Android
  main CI `29675191841` (instrumentation `88161270018`, validation
  `88161270022`, supply-chain `88161270004`, secret-scan `88161270015`) and
  follow-up main CI `29675556207` (instrumentation `88162237156`, validation
  `88162237165`, supply-chain `88162237162`, secret-scan `88162237175`) also
  passed. Android instrumentation now contains 46 tests. Physical-device
  microphone, process-kill/reboot, and complete scenario-B evidence remain
  open.

- Android network-recovery slice: Android PR #6 was squash-merged as
  `a7348ed5` after all six PR checks passed in Hosted CI run `29673994655`,
  including the signed release candidate and 45 Hosted Emulator
  instrumentation tests. The new test forces a connection loss before an
  upload-part commit, then verifies the durable checkpoint resumes only the
  missing part without duplication. Post-merge Android main CI `29674136550`
  passed instrumentation (`88158194638`), validation (`88158194644`),
  supply-chain (`88158194640`), and secret-scan (`88158194648`). Local `main`
  is fast-forwarded to `a7348ed5`; physical-device recording/recovery remains
  an explicit acceptance gate.
- Release-draft accuracy update: Server PR #28 was squash-merged as
  `1327e29`, correcting the stale claim that the coordinated Phase 1–6 slices
  were uncommitted and recording the current merged-branch gate references.
  PR checks `29673200001` and `29673200020`, followed by main CI
  `29673359492` and Workspace Compatibility `29673359514`, all passed. The
  release checklist remains intentionally open for Docker, Android physical
  device, and final A–E evidence.
- Isolated live acceptance rerun: the disposable PostgreSQL schema applied all
  18 migrations, real API/Worker processes completed the three live Console
  workflows (provider administration, LLM/glossary administration, and the
  upload → Mock ASR → correction → review → approval path), and the MCP
  `TestLiveMCPReadWorkflow` passed search, revision, exact-range citation, and
  read-audit checks. The schema, object directory, processes, and logs were
  removed by the cleanup path. This strengthens A/C/D evidence; the complete
  A–E gate remains open because Docker installation and Android physical-device
  recording/recovery are still unexecuted.
- Site documentation update: Site PR #7 was squash-merged as `7c0a665` after
  its build, SBOM, and secret-scan checks passed. Post-merge Site CI
  `29670866380` is green. The bilingual status, roadmap, and draft release
  notes now record Android Hosted Emulator run `29667693126` with all 44 tests;
  physical-device acceptance, external signing, and `v1.0.0` publication
  remain explicitly open.
- Release candidate update: `v0.1.0-rc.6` tags now point at the current
  default branches: Server `2a9dab4`, Console `ed5b7f8`, Android `2e22dc5`,
  MCP `8d34906`, and Site `7c0a665`. Release workflows
  `29671512394`, `29671512945`, `29671513656`, `29671514283`, and
  `29671514861` all passed and created draft prereleases with platform
  artifacts, SBOMs, and SHA256SUMS. The Android APK is available as
  `voiceasset-android-v0.1.0-rc.6.apk` and its verified SHA-256 is
  `11c67f237514807e5297856b63aa288675a1eacdbdb3c9470e5761c2d78c9837`.
  This is a testable prerelease, not a `v1.0.0` claim.
- QR pairing update: Android PR #3 was squash-merged as `73a7254`, and its
  hosted CI rerun `29665995831` passed dependency review, SBOM/license checks,
  JVM/Ktlint/lint/build validation, signed release packaging, secret scanning,
  and all 44 Hosted Emulator tests. Console PR #7 was squash-merged as
  `ed5b7f8`; its hosted PR CI `29665815142` and post-merge main CI
  `29666366670` passed. Android PR #4 (`04f4923`) stabilized the ViewModel
  fixture and Compose callback timing; its six hosted checks and 44 tests
  passed in `29667085136`. Android PR #5 (`2e22dc5`) then kept the controlled
  search fixture synchronized; all six hosted checks and 44 tests passed in
  `29667497601`, followed by a successful post-merge main run `29667693126`.
  The Console renders the one-time pairing payload as an in-memory QR code,
  while Android uses the Google Code Scanner QR-only flow and keeps paste as a
  fallback. A physical-device scan remains user acceptance.
- Compose gate update: Console PR #6 (`dbfb991`) clears the official Caddy
  image's inherited `cap_net_bind_service` file capability before the image
  runs as non-root with `no-new-privileges`. Server PR #12 (`8044833`) adds a
  dedicated hosted Compose smoke. Its rerun in Server CI `29663709132` (job
  `88130534343`) built the Console image, started Postgres, migrations, API,
  Worker, and the Console gateway, verified readiness and capabilities through
  the same-origin gateway, and removed its dedicated volumes. The first run's
  `exec /usr/bin/caddy: operation not permitted` failure is retained as the
  regression evidence that this fix closes.
- Compose Phase 1 HTTP update: Server PR #14 was squash-merged as `7389d73`.
  Hosted Server CI `29664615096` (job `88132843423`) now creates an isolated
  CI-only owner, logs in through the Console gateway, uploads and completes a
  two-part WAV, lets the real Worker run Mock ASR, verifies the normalized
  transcript, and checks HTTP range/HEAD playback. The run also cleans its
  Compose volumes. Workspace Compatibility `29664615120` (job `88133319517`)
  passed after the five repositories exposed the same coordination ref.
- Compose lifecycle/export update: Server PR #19 was squash-merged as
  `ef112272`. Its hosted checks `29668941012` passed after the smoke path added
  conditional metadata updates, Markdown transcript export/download, trash,
  filtered listing, and restore assertions. The test refreshes the asset ETag
  after asynchronous upload/transcription processing, preserving the intended
  optimistic-concurrency contract. Post-merge Server CI `29669093306` and
  Workspace Compatibility `29669093313` both passed.
- Compose Mock LLM update: Server PR #21 was squash-merged as `95c1e5e`.
  Hosted checks `29669921684` passed after the smoke created a credential-free
  Mock LLM profile and glossary, ran structured correction, accepted the
  immutable review, created the approved child revision, rejected duplicate
  approval, and exported the approved text. The first fixture attempt
  (`29669704812`) correctly hit the per-segment change-ratio safety limit;
  the corrected punctuation-only fixture passed in the rerun. Post-merge
  Server CI `29670080607` and Workspace Compatibility `29670080597` passed.
- Release-candidate update: Server PRs #7, #8, #9, and #10, Console PRs #2-
  #5, Android PR #2, and Site PR #6 are merged into their default branches.
  The immutable `v0.1.0-rc.5` tags now point at Server `efc1db1`, Console
  `b3d602a`, Android `d03b61c`, MCP `8d34906a`, and Site `372c43a`; Server
  (`29661350978`), Console `29661351093` (rerun job `88128128013`), Android
  (`29661351299`), MCP (`29661351168`), and Site (`29661351335`) hosted release
  runs pass. The draft prereleases retain the Server OCI tar
  `sha256:3a299378bdf5e43d603bd8e3b09b9ef18a67fb6723f41893b6aa7c0a19fd6dc9`,
  Console OCI tar
  `sha256:e20fd671d30e02eecc5bfeca4d10bd6774f96c9a94deaa066f0264190fecf051`,
  Android APK/AAB/SHA256SUMS
  `sha256:aa0903f0dcf78d9b6b6ea8052ae6617d5da7694308c9762ef343f5c7b41cbf89`,
  `sha256:5b1c159dc3bd7943a2cb527522c518ec91338aa30d793534ff63fb9fc0876909`,
  `sha256:de8219918e1adfeebfe9c16745d33bd619b1a09cdc06d364ade972c049d0a182`,
  MCP Linux archive `sha256:f2434b015e8d4149665a4e17841ff24663bb1d1e9cf150dcb9cdc9c50a4e020b`,
  and Site archive `sha256:59cb9a15e47750eab6c20c70ecad233c064e3b5d1ed4546f293c194e1afdaa95`.
- Current phase: Phase 6 gap closure (pre-v1; coordinated `0.22.0` candidate
  merged). Contract `0.22.0` and migration
  18 add the authenticated WebSocket realtime transcription upgrade using the
  `voiceasset.realtime.v1` subprotocol, bounded text event frames, resumable
  sessions, and the existing Mock ASR stream, alongside the Owner Session-only
  outbound Webhook slice. All five repositories pin `0.22.0`. Server ordinary
  tests, vet, command builds, OpenAPI lint, real PostgreSQL migration/
  notification/Webhook tests, WebSocket transport tests, and the eight-fixture
  workspace gate pass; Console passes formatting, lint, typecheck, 110 unit
  tests, seven local Chromium flows, Webhook UI build, and licenses; Android passes JVM/Ktlint,
  Debug+Release lint, Debug+Release APK/AAB, and compilation of 43
  instrumentation methods; MCP test/vet/build passes; Site's 91-operation/
  51-page static gate passes. The isolated deployment now runs
  Server API/Worker `.20260718.12`, contract `0.22.0`, schema 18, and the
  existing Console/MCP `.20260718.11` artifacts.
  Authenticated Webhook acceptance and a real TLS WebSocket start/audio/finish
  flow passed; unauthenticated access returns safe 401.
  A 19:28 UTC isolated live E2E run exposed and fixed a waveform Worker source
  metadata bug: `waveform.PostgresOriginalRepository` omitted
  `storage_backend`, so valid originals were rejected as `invalid_audio` and
  the Console waveform endpoint returned 404. The focused PostgreSQL
  regression test now passes, followed by three live Chromium workflows and
  the live MCP search/Revision/exact-range/audit/revocation workflow. The
  temporary schema and storage were removed. PR #5 was squash-merged as
  Server commit `29433499`, and the fix was deployed to the isolated 10443
  gateway as `0.1.0-dev+workspace.20260718.12`; `/readyz` returns HTTP 200 and
  `/version` reports the merged commit.
  All five coordinated candidate PRs are now merged into their default
  branches with no open PRs: Server `29433499`, Console `84440f75`, Android
  `40700d24`, MCP `8d34906a`, and Site `a3fef686`. Post-merge default-branch
  CI passed for all five repositories; the Server compatibility rerun
  `29655623342` passed after every merge was present, and the Android main
  rerun `29655641245` passed all Hosted Emulator tests. The final signed
  Android candidate run `29655228357` still retains an unexpired
  `voice-asset-signed-release` artifact.
  A 19:45 UTC post-deploy audit reports all six systemd units active,
  `/version` `0.1.0-dev+workspace.20260718.12` with commit `29433499`,
  `/readyz` HTTP 200, API/Worker/MCP/gateway/Caddy PIDs
  `165453`/`165455`/`163048`/`146764`/`18314`, and zero restarts. Port `10443`
  remains an independent listener; the public Caddyfile hash is unchanged, and
  the reused `api.getio.net` certificate fingerprint is
  `8C:AF:12:3A:DD:29:EC:A4:8B:B2:A9:D2:D4:01:85:A7:45:89:BB:D2:91:F8:FD:73:29:90:47:9C:C7:1D:E0:FF`.
  A 15:31 UTC rerun of `make compatibility` passed all eight workspace
  fixtures and the live five-repository capability/OpenAPI drift check; the
  Android change also passed JVM/Ktlint, Debug Lint, instrumentation Kotlin
  compilation, Release Lint, Release APK, and Release AAB at 15:32 UTC.
  The M4A recovery slice passed its focused JVM test and the full Android
  `test ktlintCheck lintDebug lintRelease assembleDebug assembleRelease
  bundleRelease :app:compileDebugAndroidTestKotlin` gate at 15:38 UTC.
  A read-only 15:40 UTC deployment recheck reports all five systemd units
  active, `/version` `workspace-20260718.11`, `/readyz` HTTP 200, unchanged
  PIDs (`163047`, `163043`, `163048`, `146764`, `18314`), and the same
  `api.getio.net` 10443 certificate fingerprint; no Caddy reload occurred.
  The same 15:40 UTC `make compatibility` rerun passed all eight fixtures and
  the exact five-repository capability/OpenAPI check.
  Profile selection and manual remote refresh now also skip Profiles without a
  readable local session; the Android regression test covers both entry
  points, and the full Debug/Release APK/AAB gate passed again at 15:46 UTC.
  The final Android JVM `test` rerun passed at 15:48 UTC.
  A formal RSA-4096 PKCS#12 upload keystore was generated outside the
  repositories at `C:\tools\voiceasset-signing\voiceasset-upload-20260718.p12`;
  the four required GitHub Actions Secrets are configured for the Android
  repository. A local signed release APK/AAB, SBOM, and checksum package passed
  `package-release.sh` and `verify-release.sh` at 16:01 UTC. The APK is
  10,294,140 bytes with SHA-256
  `744F29EB0FF26C6DBE16331934D41C12A803460F56083F61A160E3BDF52E8ED6`, and
  the signing certificate SHA-256 is
  `3D2076862E3F0BD6E6031B0210D5B190CA15AF16FC1CE1F0B08A0A2F3A722BE6`.
  The Android CI now builds and uploads a signed release candidate for
  same-repository pull requests, while the tag workflow publishes a signed
  draft prerelease. Final hosted Android CI run `29655228357` passed all six jobs,
  including all 44 Hosted Emulator instrumentation tests and the signed release
  candidate upload (`voice-asset-signed-release`). Server CI run `29652672572`
  and Workspace Compatibility run `29652672615` also passed, including the real
  runtime OCI image build and backup/restore smoke gate. The remote test host has
  no container runtime installed, so the hosted OCI gate is the container evidence.
  Physical-device execution remains user acceptance; no v1.0 tag or publication
  claim is made yet.
  A read-only 15:13 UTC recheck still reports all five systemd units active,
  `/version` `workspace-20260718.11`, `/readyz` HTTP 200, and unchanged API,
  Worker, MCP, gateway, and public Caddy PIDs; no public Caddy reload occurred.
  The verified 42-object/42-file backup is retained at
  `/srv/voiceasset-backups/2026-07-18T1116Z-before-contract-0.22.0-r1`, plus the
  binary rollback snapshot `backups/server-before-workspace-20260718.11`, and a
  file rollback copy is retained under the VoiceAsset data directory. Public
  Caddy PID `18314`, gateway PID `146764`, and the existing certificate on
  443/10443 remain unchanged. Android startup now prioritizes local recording
  without requiring login or a server connection. A development-signed APK is
  retained in Downloads; the server-profile form now explicitly labels sync as
  optional and the refreshed debug APK is
  `voice-asset-android/app/build/outputs/apk/debug/app-debug.apk` with SHA-256
  `FEAC45E88C4BE2AD2208B4E5DF03E176B1DF2DB59E24DCEF07788514E849DA86`; a
  copy is also retained as
  `C:\Users\minit\Downloads\VoiceAsset-local-first-auth-gated-sync-debug-20260718T154653Z.apk`.
  Physical-device execution remains user acceptance. The installed API 35
  x86_64 AVD was also tried with software rendering; `emulator -accel-check`
  reports that AEHD is not installed, and the no-acceleration launch exited
  without an online `adb` device, so no emulator pass is claimed.
  The 14:21 UTC cutover replaced the isolated API/Worker/MCP with workspace
  candidate `workspace-20260718.11`; readiness, `/version`, capability,
  authenticated MCP denial, and static Console asset checks passed after a
  bounded startup wait. Current API/Worker/MCP PIDs are `163047`, `163043`, and
  `163048`; public Caddy and the independent gateway remained `18314` and
  `146764`.
  The AWS SDK v2 S3-compatible adapter is now wired into API/Worker startup;
  its local HTTP-compatible lifecycle tests pass, and the isolated remote S3
  lifecycle probe passed four 256 KiB parts, 1 MiB assembly, snapshot
  verification, immutable publication, and deletion in 5.382 seconds at
  0.2 MiB/s. The isolated host now runs Collector 0.155.0 on loopback
  OTLP/HTTP `14318` with protected trace output, Alertmanager 0.33.1 on
  `19093`, and the allowlisted local notification receiver on `19193`; a
  synthetic firing alert was delivered and an API trace was observed. Public
  Caddy PID `18314` and gateway PID `146764` remained unchanged. The isolated
  S3 backup/restore gate also passed with two objects, two database rows, 18
  migrations, and matching SHA-256 values after restoring into a new database
  and prefix; all temporary state was removed. Physical-device execution,
  Docker image execution, hosted CI, and publication remain open; this is not
  v1.0. Android's
  no-server startup instrumentation assertion now checks the local-first
  description and local recordings section. Android README and architecture
  baseline now pin the current `0.22.0` contract instead of stale `0.20.0`
  wording. MCP, Console, Server versioning, and the v1.0 draft now carry the
  same current contract and schema wording. Android's new initial-state test
  proves no authentication or remote-sync scheduling occurs before user setup.
  The new `StartupSyncPolicy` JVM test additionally covers authenticated,
  missing, and unreadable Profile credentials; the refreshed debug APK was
  rebuilt after this gate.
  R-002/R-009 now retain the process-recovery and missing-AEHD device evidence;
  both Android runtime risks remain open. Android
  application startup now also skips remote sync recovery for server Profiles
  without a locally protected session, preserving the local-first path when a
  server is configured but not authenticated. RecordingService now applies the
  same session gate after capture, so a signed-out or unreadable Profile cannot
  enqueue a remote task after a local recording is saved. Recording recovery
  now also validates and promotes readable MediaRecorder `.m4a` files after a
  process restart, while preserving explicit failure for invalid media. The Console
  mock Chromium fixtures now derive their capability contract from the pinned
  `0.22.0` constant rather than stale literals; the full local e2e gate passes
  seven flows with ten explicitly skipped live/deployed flows. Hosted CI still
  needs a new pushed candidate before its current branch status can be treated
  as evidence. A read-only GitHub audit confirms the latest default-branch
  baseline CI records remain green (Server `29472180011`, Console `29472656283`,
  Android `29473983934`, MCP `29472179992`, Site `29472660722`). Site Dependabot
  PRs #1 and #2 are green; PR #4's failure is the historical pnpm minimum
  release-age policy for `astro@7.1.0`, not a build failure, and no rerun or
  merge was triggered. The five-repository compatibility verifier still passes for
  API `v1`, contract `0.22.0`, 45 sorted Server features, Console 32, Android
  6, and the exact Site OpenAPI. A documentation audit also corrected the
  bilingual Site status pages to advertise the current Android `0.22.0`
  compatibility range; Site's full static gate remains green. The Android
  emulator workflow now invokes
  the Gradle wrapper through Bash explicitly, addressing the old hosted
  exit-126 path without
  changing the release gate. After adding the opt-in probe, Server
  `go test -count=1 ./...`, `go vet ./...`, and `git diff --check` pass.
  The current uncommitted workspace also regenerated and verified temporary
  six-platform Server and MCP archives, deterministic Console and Site
  archives with license inventories, and unsigned Android APK/AAB release
  metadata plus SBOM/checksums; all temporary release directories were removed.
  `make compatibility` passed all eight workspace fixtures and the live
  five-repository capability check, and the current Console/Site/Android
  default verification commands pass again. GitHub Dependabot inspection found
  Site PRs 1 and 2 fully green; PR 4's only failure was the historical pnpm
  minimum-release-age policy for newly published `astro@7.1.0`, not a build
  failure. No Dependabot PR was merged or rerun. After the operational
  `/version` endpoint fix, the same eight-fixture compatibility suite was rerun
  successfully without changing the shared contract or client pins.
- Previous 0.19 checkpoint: Contract `0.19.0` added an
  authenticated, audited `admin:read` projection of exactly eight allowlisted
  deployment runtime facts. Every mutation method returns `405`, query
  parameters fail closed, the global `system_settings` table is never read, and
  no path, endpoint, credential, or token is serialized. Console renders the
  projection as operator-managed and read-only without a form or save action.
  All five repositories pin `0.19.0`. Server ordinary tests, vet, command builds,
  OpenAPI lint, and the seven-fixture workspace gate pass; Console passes 107
  unit tests and seven local Chromium flows; Android passes 134 JVM tests,
  Debug/Release Lint and APK/AAB builds, plus compilation of 41 instrumentation
  methods; MCP ordinary tests, vet, and builds pass; Site's 83-operation/51-page
  static gate passes. The local Windows race rerun is blocked before compilation
  by the installed cgo toolchain, so no new 0.19 race claim is made. The isolated
  `10443` deployment runs `.20260718.4`, contract `0.19.0`, and schema 16 after
  a verified 42-object/42-file offline backup. Strict-TLS 401, allowlist read,
  query/mutation denial, audit, and logout acceptance pass. Public Caddy PID
  `18314`, gateway PID `146764`, both configuration hashes, zero-restart state,
  and the certificate reused on 443/10443 remained unchanged. The full GOAL
  still lacks the concrete real-time adapter, physical-device execution,
  broader storage/observability completion, and publication scope, so this is
  not a v1.0 release candidate. A later local-only real-time hardening change
  now bounds every client-event read to three advertised heartbeat intervals;
  it is neither advertised nor deployed while the concrete WebSocket dependency
  and production wiring remain unapproved. A credential-backed Tencent Flash
  test now passes against the current service after a read-only account check
  proved that the persistent local `TENCENT_ASR_USER_ID` value is the account
  UIN rather than the required APPID. No credential or `.env` value was changed
  or exposed. The adapter now preserves vendor word timestamps and expands a
  normalized sentence boundary when Tencent returns a word before that boundary;
  this ASR hardening is local-only and has not been deployed. Post-change
  `make test`, `make lint`, `make contract`, `make build`, and
  `make compatibility` all pass. Server and Console Tag workflows now define
  checksum-covered Linux AMD64/ARM64 OCI archives using digest-pinned bases,
  QEMU, and BuildKit, with streaming validation of blob integrity, exact
  platforms, immutable labels, port `8080`, and non-root `65532:65532` runtime
  identity. Both seven-case Node validator suites, both workflow `actionlint`
  checks, Bash syntax checks, the Console 107-test `pnpm verify` gate, and a
  real local static-archive verification pass. This workstation has no Docker
  engine, so no OCI archive was built locally and the hosted immutable Tag gate
  remains open.
- Corresponding commit: coordinated `0.22.0` candidate merged to all five
  default branches; remaining work is release-gate closure rather than an
  uncommitted candidate.
- Estimated full-GOAL v1.0 completion: approximately 70%. The previous 96%
  estimate measured only the then-current release checklist and overstated the
  broader product scope

## Completed

- Created exactly five independent local Git repositories and matching public
  GitHub repositories under `getio0909`; every default-branch Phase 0 workflow
  is green.
- Established AGPL licensing, README, contributing, security, CODEOWNERS,
  changelog, architecture/ADR, contract pins, and independent CI in every repo.
- Implemented Server health/capability endpoints, OpenAPI 3.1 contract `0.1.0`,
  domain schema, transactional migration runner, and PostgreSQL integration test.
- Implemented honest, buildable Console, Android, MCP, and bilingual Site
  foundations that consume or record contract `0.1.0`.
- Added fail-closed capability validation in Console and MCP, real MCP stdio/HTTP
  integration tests, Android emulator CI, dependency audits, license checks,
  secret scans, SBOMs, and pinned third-party Actions.
- Proved the Phase 0 baselines with these immutable GitHub Actions records:

| Repository | Commit                                     | Green run                                                                                |
| ---------- | ------------------------------------------ | ---------------------------------------------------------------------------------------- |
| Server     | `93d24228976f1bdd7ec0a8ae981cd25c549a091a` | [29472180011](https://github.com/getio0909/voice-asset-server/actions/runs/29472180011)  |
| Console    | `abb3cd040854fd3eac25bba391b056f10be0d6de` | [29472656283](https://github.com/getio0909/voice-asset-console/actions/runs/29472656283) |
| Android    | `a6067f8c30f3916ed3b8add489f525825808c9ff` | [29473983934](https://github.com/getio0909/voice-asset-android/actions/runs/29473983934) |
| MCP        | `5df2f92dd0b828c8383ad6fc1288d892993954aa` | [29472179992](https://github.com/getio0909/voice-asset-mcp/actions/runs/29472179992)     |
| Site       | `8c48f820b061f9c5575810152a5ae749bfc54693` | [29472658617](https://github.com/getio0909/voice-asset-site/actions/runs/29472658617)    |

- Completed the Server-side Phase 1 path locally: owner bootstrap and secure web
  sessions, workspace-scoped assets, verified resumable WAV ingestion,
  PostgreSQL-backed transcription jobs and leases, deterministic Mock ASR,
  immutable provider JSON and raw transcript timelines, and authenticated
  GET/HEAD/Range audio playback.
- Added one isolated-schema E2E that executes the complete HTTP and worker flow
  with a two-part WAV and proves raw-plus-normalized transcript publication and
  `206` playback.
- Published the implemented Server API as OpenAPI `0.2.0` and added the
  one-command Compose topology with PostgreSQL migration gating, separate API
  and worker processes, loopback-only development exposure, and shared durable
  object storage.
- Hardened local storage against cross-process replacement and intermediate
  symlink escapes; uploads now compensate state changes independently of
  request cancellation and clean terminal or expired parts when observed.
- Implemented the Console Phase 1 workflow with cookie-only login, full-file and
  per-part SHA-256, resumable upload progress, bounded job polling,
  authenticated audio, and a clickable synchronized raw-transcript timeline.
- Proved the complete browser path twice: a deterministic Playwright API mock
  validates request integrity and accessibility, while an isolated live run
  migrated PostgreSQL, started the real API and worker, stored a WAV, completed
  Mock ASR, loaded audio/transcript in Chromium, signed out, and removed its
  schema, processes, binaries, logs, and object directory.
- Advanced Console, Android, MCP, and Site contract records to `0.2.0`; MCP
  fails closed at that version and Site now checks the bilingual status pin.
- Deployed the uncommitted Phase 1 candidate under `/data/apps/caddy/voice` on
  the authorized Debian host. Dedicated systemd units run migrations, API,
  worker, and an independent Caddy gateway at `https://api.getio.net:10443`;
  the existing Caddy process and its 80/443 configuration were not reloaded or
  modified.
- Installed a loopback-only PostgreSQL 15 test database with peer
  authentication, exposed only `10443/tcp` through UFW, and retained the
  bootstrap Owner credential as a root-only `0600` host file.
- Passed the complete live Chromium Phase 1 workflow through the remote TLS
  gateway: login, verified WAV upload, Mock ASR, authenticated playback,
  synchronized transcript, zero axe violations, empty browser token storage,
  and logout.
- Published additive OpenAPI contract `0.3.0` with `audio/mp4` upload/playback
  and the `m4a_uploads` capability. The bounded ISO BMFF parser requires one
  supported AAC track and validates `esds`/`AudioSpecificConfig` rather than
  trusting only the extension, MIME declaration, or `mp4a` sample-entry name.
- Implemented the Phase 2 Android source path: validated HTTPS profiles,
  custom-CA and full-leaf-fingerprint trust, login plus fail-closed capability
  negotiation, Keystore-protected sessions, Room recording/sync checkpoints,
  foreground M4A capture, exact resumable parts, constrained WorkManager jobs,
  retry/auth blocking, cold-start recovery, stable per-recording idempotency
  keys, and optional transcription enqueue.
- Hardened the Android recovery candidate so server-recorded parts are skipped
  only after their SHA-256 matches the exact local byte range. Added deterministic
  lost-response/process-restart tests, transcription job polling, fail-closed
  job/revision validation, a Room-backed immutable revision cache, and Compose
  transcript display. A queued job is no longer reported as complete.
- Updated the isolated Debian deployment to
  `0.1.0-dev+phase2.20260716.1`/contract `0.3.0` with atomic rollback backups.
  Real mono and stereo AAC/M4A files, including a 19,054-byte r2 fixture,
  passed authenticated upload, server validation,
  Mock transcription, immutable-revision publication, HEAD media type, and
  byte-exact `206` range playback. Existing Caddy 80/443 and the independent
  gateway were not restarted.
- Published additive OpenAPI contract `0.4.0` and advanced the recorded contract
  pins in Console, Android, MCP, and Site. The Phase 3 build advertises only
  implemented ASR/LLM profile, hotword, glossary, correction, and approval
  capabilities.
- Implemented provider-neutral ASR capabilities, safe errors, retry,
  concurrency, eligible primary/fallback routing, and Alibaba/Tencent Flash
  adapters. Sanitized official-protocol fixtures preserve and normalize vendor
  responses; credential-backed live tests are opt-in.
- Verified Tencent Flash against the live service with a generated 16 kHz mono
  WAV. Read-only STS/CAM checks proved the configured key pair is active and
  identified the local UIN/APPID mismatch without disclosing either value.
  Regression coverage now proves out-of-sentence vendor word timestamps expand
  the normalized segment while the word timeline and raw response remain intact.
- Added workspace-scoped encrypted ASR and LLM profile storage with optimistic
  versioning, health history, redacted public configuration, and separate
  AES-GCM namespaces. Provider URLs are HTTPS-only and reject unsafe targets.
- Added independently versioned ASR hotwords and LLM glossaries with scoped
  resolution and immutable job snapshots; neither system writes into the other.
- Added Mock and OpenAI-compatible LLM correction providers. Transcript text is
  isolated as untrusted input, output must be a structured patch, and the server
  rejects original-text, numeric, semantic, ratio, schema, and timeline
  violations before publishing a revision.
- Transcription now atomically publishes `raw_asr` and an identity
  `normalized` child. Durable correction jobs create immutable
  `llm_corrected` revisions; append-only decisions and approval create
  `human_edited` and `approved` children without modifying their sources.
- Passed the complete real-PostgreSQL suite, including Phase 3 HTTP/worker E2E:
  create glossary and encrypted Mock LLM profile, correct a normalized
  transcript, accept a selected change, approve it, preserve the provider raw
  object, reject duplicate approval, and prove source immutability.
- Implemented the Console Phase 3 correction workspace with glossary and Mock
  LLM profile creation, health checks, durable correction polling, structured
  per-change/bulk decisions, conservative approval, and immutable result display.
  Its full verify gate passes 25 Vitest tests, lint, typecheck, build, licenses,
  and a mocked Chromium ASR-to-approved flow with zero axe violations.
- Deployed `0.1.0-dev+phase3.20260716.1`/contract `0.4.0` to the authorized
  Debian `10443` topology. Migration 4 and a remotely generated Profile master
  key are active; API/worker have zero restarts, and both the independent
  gateway PID and the existing public Caddy PID remained unchanged.
- Passed a real remote Chromium workflow through internal-CA TLS: secure login,
  verified WAV upload, Mock ASR, raw-plus-normalized publication, glossary and
  enabled Mock LLM profile creation, correction, selected acceptance, and
  approval. The resulting asset has exactly one revision of each required kind,
  two review records, two successful jobs, and two immutable provider objects.
- Published additive OpenAPI contract `0.5.0` with workspace-scoped asset title
  search and deterministic `created_at + id` opaque cursors bound to the
  normalized query. Literal `%` search, pagination, malformed cursor, limit,
  scope, HTTP, and real-PostgreSQL tests pass.
- Added fail-closed immutable read audit records for asset list/read and
  transcript list/revision access. Agent-role principals persist as
  `actor_type=agent`; unit, HTTP failure, and real-database tests pass.
- Implemented eight typed MCP read-only tools: capability discovery, paginated
  asset list/search, asset/metadata reads, specified transcript revisions,
  latest parent lineage traversal, and exact half-open millisecond segment
  citations. MCP remains REST-only and contains no database driver.
- Verified the official MCP Go SDK `v1.6.1` as the current stable module and the
  2025-11-25 specification line. Bounded responses, safe non-2xx errors,
  cancellation, scope denial, stdio/Streamable HTTP, tool schemas, read-only
  annotations, pagination, and per-IP rate-limit tests pass.
- Extended the isolated cross-repository E2E: Chromium creates an approved Mock
  ASR/LLM lineage, then a real MCP session searches the asset, follows the
  specified Revision, returns an exact time-range citation, and proves all
  three read action classes exist in `audit_logs` before dropping the schema.
- Deployed `0.1.0-dev+phase4.20260716.1`/contract `0.5.0` plus the MCP binary to
  the authorized Debian topology. Strict external CA/hostname verification,
  authenticated search, session revocation, readiness, and capabilities pass;
  API/worker/gateway have zero restarts and both Caddy PIDs remained unchanged.
- Published additive contract `0.6.0` with workspace API-key create/list/revoke
  endpoints. Tokens contain 256 random bits, are returned once, stored only as
  SHA-256 plus a display prefix, expire within 5 minutes to 365 days, cannot
  exceed the creator's scopes, and resolve as auditable Agent principals.
- Added unit, HTTP, migration, and real-PostgreSQL API-key coverage for
  one-time plaintext delivery, scope escalation rejection, redacted listing,
  workspace isolation, last use, idempotent revocation, and post-revoke 401.
  The isolated Console/MCP E2E now creates a temporary key, attributes reads
  to it, revokes it, and removes all temporary state after applying 5 migrations.
- Deployed `0.1.0-dev+phase4.20260716.2`/contract `0.6.0` and migration 5 to the
  authorized Debian topology. MCP listens only on `127.0.0.1:18090`, uses a
  read-only Server key, and exposes `/mcp` through the separate `10443` gateway
  with another bearer token. The existing public Caddy remained PID `18314`.
- Strict external CA/hostname verification and the official MCP Go SDK pass
  against the final r3 deployment. Missing inbound auth returns 401; successful
  calls persist `asset.listed` as `actor_type=agent` with API-key attribution.
  API, worker, MCP, and independent gateway have zero restarts and zero
  error-priority entries for their current processes. The retained r3 archive
  SHA-256 is `56d3fc3175a22971901f077f32b7cb015fc414ecca32cfcb9ac5052a5fa17a92`;
  the pre-upgrade rollback backup remains host-local.
- Published additive OpenAPI contract `0.7.0` with collection, tag, annotation,
  processing-status, asset-metadata, audio-clip, and transcript-export APIs.
  Migrations 6 and 7 preserve workspace isolation, immutable artifact lineage,
  exact millisecond ranges, one-hour expiry, and Agent/API-key attribution.
- Implemented argument-safe FFmpeg clip generation with a five-minute and
  16 MiB ceiling, fixed arguments, a 45-second timeout, mono 16 kHz PCM WAV,
  verified immutable storage, compensation, authenticated HEAD/GET/Range, and
  fail-closed read audit. JSON, Markdown, SRT, and WebVTT exports use the same
  bounded storage and audit boundary.
- Expanded MCP to 12 read tools, five resources, six prompt workflows, and nine
  explicit write tools. Writes remain disabled unless
  `VOICE_ASSET_MCP_ENABLE_WRITES=true`; all data still crosses only the public
  REST API, and transcript text is marked as untrusted prompt input.
- Deployed `0.1.0-dev+phase4.20260716.3`, contract `0.7.0`, migrations 1–7,
  Console assets, and the write-enabled MCP service to the authorized Debian
  topology. Debian FFmpeg `5.1.9-0+deb12u1` is installed. API, worker, MCP, and
  gateway are active with zero restarts; gateway PID `96419` and public Caddy
  PID `18314` were unchanged. The retained release archive SHA-256 is
  `037979350711a800ec58949db9f424c1c2de6859e15275f0665a9077e4b792fb`.
- Passed strict-TLS official-SDK remote E2E with discovery of all 21 tools. The
  workflow selected a ready asset and immutable Revision, read exact segments,
  created WAV clips and WebVTT exports, then verified authenticated HEAD/GET/Range,
  sizes, SHA-256, RIFF/WAVE, and WebVTT content. Successful create/read audits
  are all `actor_type=agent` and carry the new key ID; the prior read-only key
  returned 403 for clip creation with no success audit, was revoked, and now
  returns 401 on protected APIs.
- Added offline `adminctl backup`, independent `backup-verify`, and clean-target
  `restore`. A versioned manifest hashes the PostgreSQL custom archive, every
  local-storage file, and the database object inventory; credentials stay out
  of arguments and backup metadata, links and unexpected files fail closed,
  and restore refuses non-empty database or storage targets.
- Passed a real test-host disaster-recovery drill with the packaged PostgreSQL
  tools: pre/post verification succeeded, all 30 user tables had identical row
  counts, all 13 stored files matched path, size, and SHA-256, and all temporary
  databases/files were removed. Public Caddy PID `18314` and gateway PID `96419`
  remained unchanged.
- Added a complete single-host Compose model with a dedicated unprivileged
  Console/Caddy same-origin gateway, loopback diagnostic API, separate
  PostgreSQL/object/backup volumes, and an administration container for owner,
  backup, verification, and restore commands. Docker runtime validation remains
  delegated to CI because neither available host has Docker.
- Expanded Site to 24 aligned Chinese/English pages and generated a 59-operation
  OpenAPI `0.7.0` reference plus downloadable contract. Astro content/type,
  required-page, i18n, static accessibility, build, audit, and 49-page internal
  link checks pass.
- Added monitoring, security, privacy, deployment, upgrade/rollback, recovery,
  troubleshooting, download, and contributor guidance. All five repositories
  now have Tag-triggered draft-prerelease pipelines with checksums and SBOMs;
  all ten CI/release workflow files pass `actionlint`, and Server/MCP release
  commands cross-compile for Linux, Windows, and macOS AMD64/ARM64.
- Re-ran the isolated cross-repository flow after the Phase 5 changes. Chromium
  passed upload, Mock ASR, Mock LLM correction, selected review, and approval in
  8.2 seconds; the official MCP SDK then passed search, Revision, exact-range,
  and read-audit verification before all temporary state was removed.
- Deployed Server `0.1.0-dev+phase5.20260716.4`/contract `0.7.0` to the authorized
  Debian topology after exercising an automatic rollback on an intentionally
  too-early MCP health probe. The corrected deployment has seven migrations,
  zero service restarts/error-priority logs, unchanged Caddy PIDs, and archive
  SHA-256 `89e52e330f9731242ebe601a5c60b60f3caa4e638d6be5385f47431fd3af4e7c`.
- Added Console API-key administration with least-privilege scope and expiry
  selection, redacted inventory, one-time memory-only plaintext display, and
  revocation. Its 31 Vitest cases and mocked Chromium workflow prove that the
  plaintext is never written to local or session storage and is cleared on
  dismissal, revocation, route exit, and sign-out.
- Added the worker's bounded expired clip/export reaper. Unit and real-PostgreSQL
  tests cover integrity failure, retry-safe conditional deletion, idempotent
  absence, permanent-source preservation, immutable system audits, and
  continuation after one corrupt candidate.
- Deployed Server `0.1.0-dev+phase5.20260716.5` and the new Console bundle to the
  isolated `10443` topology. An incorrect deployment-time database-name check
  triggered automatic rollback to `.4`; the corrected run completed with seven
  migrations, zero restarts, unchanged gateway/public Caddy PIDs, and Server
  archive SHA-256 `5a729584836038ac4ef6368c1101e851d10803b239bf3605ff5756ccf8ddeefd`.
  The Console archive SHA-256 is
  `95e32f9d3b31c8716d73db52e5b94ff2607b95142d9a6e326c838dae75d86dfe`.
- Passed deterministic remote reaper and deployed Chromium API-key lifecycle
  E2E. The reaper removed only the expired file/metadata, retained its source
  Revision, and wrote one audit; the browser created a one-scope key, verified
  one-time display and empty web storage, revoked it, and left zero active test
  keys. Strict TLS chain and `api.getio.net` hostname verification also loaded
  the deployed `/api-keys` bundle.
- Added Console ASR profile and hotword administration for the exact three
  implemented adapters. Administrators can compare capabilities, publish and
  enable immutable hotword versions, create Mock/Alibaba/Tencent profiles,
  check health, change state, and rotate write-only vendor credentials with
  optimistic ETags. Credential values remain component-local and are stripped
  from Pinia state and public response models.
- Extended the isolated real-PostgreSQL browser orchestrator with hotword v1/v2,
  state changes, Mock ASR profile creation, health, and disable flows. Both live
  Chromium cases, the MCP read/audit workflow, mocked Provider credential
  create/rotation checks, and zero-violation axe scans pass; the disposable
  schema, credentials, processes, and files are removed afterward.
- Deployed the Provider/Hotword Console bundle to the isolated `10443` gateway.
  Strict Node and remote CA/hostname checks, readiness, and a real deployed
  read-only browser session pass. Public Caddy PID `18314`, gateway PID `96419`,
  and API/worker/MCP PIDs were unchanged, with zero error-priority journal lines
  after deployment. The retained Console archive SHA-256 is
  `a8f07a9766b7c9eff89c595fca0ed616be131a921cd0da284087a4d0b648f8a1`.
- Added complete Console administration for the exact two implemented LLM
  adapters and immutable glossary sets. Administrators can compare capability
  limits, publish and enable glossary versions, create Mock or
  OpenAI-compatible profiles, check health, change state, and rotate API keys
  plus bounded custom headers. Credentials remain component-local, are never
  placed in Pinia, and are cleared on submit, provider change, route exit, and
  sign-out. Manual review remains the default while administrators may opt one
  profile into the narrower `validated_glossary_only` policy.
- Extended the isolated orchestrator to three real Chromium cases. Glossary
  v1/v2, state changes, Mock LLM profile creation, health, and disable pass
  alongside manual then glossary-only automatic approval and the ASR
  administration path; MCP read/audit verification then passes before the
  unique schema and all transient state are removed. Mocked OpenAI-compatible
  create/rotation also proves exact ETags, safe custom headers, zero credential
  persistence, and zero axe violations.
- Deployed the LLM/Glossary Console bundle to the isolated `10443` gateway.
  Strict Node and remote CA/hostname checks, readiness, and a real read-only
  ASR-plus-LLM browser session pass. Public Caddy PID `18314`, gateway PID
  `96419`, and API/worker/MCP PIDs were unchanged, with zero error-priority
  journal lines after deployment. The retained Console archive SHA-256 is
  `034f7c2e233e032e5857f9e10388b225c3b597bd4a110e81a97d4d9be2ea13c6`.
- Implemented fail-closed `validated_glossary_only` auto-approval. Eligibility
  requires a non-empty effective glossary and deterministic change set plus all
  existing text, numeric, semantic, ratio, and timeline validations. The same
  transaction creates `llm_corrected`, system `human_edited` and `approved`
  descendants, one automated review, and one system audit; empty/unsafe changes
  stay pending and a later duplicate approval is rejected.
- Deployed Server `0.1.0-dev+phase5.20260716.6` and the matching Console bundle.
  An incorrect loopback version-path assertion exercised automatic rollback
  before the corrected retry. Strict external CA/hostname checks and a read-only
  Chromium provider smoke pass; API/worker are PID `107775`/`107776`, while MCP
  `105001`, gateway `96419`, and public Caddy `18314` stayed unchanged. Current
  process error journals are empty. Retained Server and Console archive SHA-256
  values are `550bb0019a8c1ab19bc7001b35862378f5c2125b4c0a89495f8937b7837d1df0`
  and `1bf50f47b874b8b10950166004d3ff0ba540728cf34d058f559133d5ce127e2c`.
- Published additive OpenAPI contract `0.8.0` and migration 8. Browser login now
  issues separately path-scoped Secure/HttpOnly access and refresh cookies;
  refresh rotation is atomic, only SHA-256 digests are stored, recognizable
  devices can be listed/revoked by their owner, API keys cannot enumerate
  personal sessions, and create/refresh/revoke actions are audited.
- Hardened OpenAI-compatible provider egress against DNS rebinding and ambient
  proxy bypass. The server validates one DNS result set, rejects mixed public and
  special-use addresses, pins the approved IP for dialing, and disables redirects
  and environment proxies. Auth/RBAC/revocation, rate-limit, upload, storage,
  provider URL, and cookie/Origin security suites pass.
- Advanced all five repository pins to `0.8.0`. Server ordinary and direct Linux
  race suites, Console's 61 Vitest and mocked browser gates, Android core API/JVM
  and Ktlint gates, MCP tests/vet/build, and Site's generated 62-operation,
  24-page/49-file gates pass. Android app compilation/device gates remain open.
- Deployed Server and MCP `0.1.0-dev+phase5.20260717.1`, migration 8, and the
  matching Console bundle to the isolated `10443` topology. A validator-only
  SIGPIPE false failure exercised automatic rollback before the corrected retry.
  Strict CA/hostname checks, an official-SDK read-only MCP test, and a real
  Chromium access/refresh rotation plus current-device revocation flow pass.
  The session run persisted exactly one create, refresh, and revoke audit and
  left no new active session. API/worker/MCP are PID `114085`/`114098`/`114100`;
  public Caddy PID `18314` and gateway PID `96419` remained unchanged with zero
  restarts and no error-priority logs. Retained Server and Console SHA-256 values
  are `3ea26300e43bfc8bc97758819b39d504c753d067ab47e03d96002e82aac91177`
  and `01cf3c35bcb7e51083da69acf8b3cf4b202b174b524be7418673a3b262fad8e2`.
- Published additive OpenAPI contract `0.9.0` and migration 9. Asset cursors are
  bound to normalized title, Collection, Tag, status, and inclusive/exclusive UTC
  date filters; assigned tags have a scoped read API; and exact-version trash/
  restore preserves immutable audio, revisions, tag relationships, and the prior
  active status while writing immutable audits. Unit, HTTP, and real-PostgreSQL
  tests cover validation, workspace isolation, stale versions, filters, audits,
  legacy backfill, and upgrades from every schema version 1–8.
- Advanced all five contract pins to `0.9.0`. Server tests/vet/build/OpenAPI,
  Console format/lint/typecheck/64 tests/build/default Chromium, Android
  all-module test/lint/Debug APK, MCP tests/vet/build, and Site's 65-operation,
  25-page/51-file gates pass locally. The installed Google SDK is
  `C:\tools\Android\Sdk`; the current Codex process needed the persisted user
  `ANDROID_HOME` copied into its process environment before Gradle ran.
- Deployed Server/MCP `0.1.0-dev+phase5.20260717.2`, migration 9, and the
  matching Console to the isolated `10443` topology. Preflight validated the
  uploaded SHA-256, all embedded versions, nine migration checksums, a side-port
  candidate API, and two deterministic builds per archive. A failure-safe real
  Chromium flow passed filters, assigned-tag reads, metadata, trash/default
  exclusion/restore, axe, and empty Web Storage; official-SDK strict-CA MCP reads
  also pass. API/worker/MCP are PID `118688`/`118689`/`118707`; public Caddy PID
  `18314` and gateway PID `96419` are unchanged, all units have zero restarts,
  and the post-deploy error journal is empty. Retained Server and Console hashes
  are `413f7c07ec63c75355dbc3975b2970937c24d6bd08c54d59a35a394217799b02`
  and `a8c1dd4fb92e7d574495952eabe4457dd0d2585a6edfb6e75405fbcc09bba7cc`.
- Added a side-effect-free Phase 6 control-plane performance gate against the
  isolated strict-TLS `10443` deployment. After 16 warm-up requests, 400
  readiness/capability requests at concurrency 8 completed with zero failures,
  42.8 req/s throughput, 180.520 ms p50, 217.871 ms p95, 270.894 ms p99, and
  292.618 ms maximum latency. All service PIDs and zero-restart counts remained
  unchanged, error-priority journals stayed empty, and no active test session
  was left behind.
- Added an isolated PostgreSQL asset-path performance gate. A fully migrated
  disposable schema with 5,000 seeded assets passed 100 production-service
  asset/audit creates at concurrency 8 with zero failures, 900.6 ops/s, and
  41.758 ms p95, plus 400 list/title-search reads with zero failures, 194.2
  ops/s, and 54.092 ms p95. Cleanup and an external query proved zero residual
  `asset_perf_%` schemas.
- Added a representative supported local data-pipeline gate. Eight concurrent
  two-part 5.24 MiB WAV upload/storage/probe operations passed at 9.6 ops/s and
  499.027 ms p95; eight Mock Worker/transcript publications passed at 53.6 ops/s
  and 107.040 ms p95; 32 full-hash audio opens passed at 186.9 ops/s and 32.332
  ms p95. Every operation succeeded, and the schema and temporary objects were
  removed.
- Added deterministic real-PostgreSQL upgrade coverage from every prior schema
  version 1–7 to version 8. A separate staged v1 → v2 → v8 test preserved a
  representative workspace, user/membership, asset, raw transcript, Provider,
  system setting, access session, and queued job; version 8 correctly backfilled
  `Legacy session` without inventing a refresh credential. The complete
  migration package also passes first/idempotent apply, checksum-tamper rejection,
  and all development down migrations.
- Replaced Server and MCP inline release builds with locally reproducible,
  fail-closed scripts. Each repository passed two complete Linux, Windows, and
  macOS AMD64/ARM64 builds with identical archive hashes. Verification covered
  safe extraction, exact package contents, contract pins, Go target metadata,
  embedded versions/revisions, host runtime version commands, and every SHA-256
  entry. Server now exposes side-effect-free version JSON from all four binaries;
  MCP Tag builds now inject the tag instead of reporting `dev`. The exact local
  hashes and uncommitted-worktree limitation are retained in the
  [release artifact validation record](../operations/release-artifacts.md).
- Added a real FFmpeg clip performance gate using the production clipper.
  Twelve 30-second clips from a 5.24 MiB PCM source run at concurrency 3; every
  output must be mono 16 kHz, fully readable, within the 16 MiB ceiling, and
  removed on close. Two consecutive runs passed the 3-second p95 and 1 ops/s
  floors; the latest reached 12.9 ops/s and 454.337 ms p95 with zero failures.
- Added an authoritative cross-repository v1.0 release-notes draft and matching
  Chinese/English Site pages. They distinguish retained candidate evidence from
  the open CI, artifact, Docker, Android, S3, and live-vendor gates and explicitly
  prohibit a Stable claim. The Site now passes 25-page locale parity and content,
  build, accessibility, and internal-link checks across 51 HTML files.
- Verified the user-provided Google command-line tools archive against Google's
  published SHA-1 and installed a user-scoped SDK under `C:\tools\Android\Sdk`.
  Platform/Build Tools 37.0.0, platform-tools 37.0.0, emulator 36.6.11, and the
  API 35 Google APIs x86_64 image are installed; `voiceasset-api35` is retained
  under `C:\tools\Android\avd`. `ANDROID_HOME`, the compatibility
  `ANDROID_SDK_ROOT`, `ANDROID_AVD_HOME`, and the three SDK PATH entries are
  user-scoped; machine environment variables were unchanged.
- Closed the Android source/build gate by moving to `compileSdk 37` while keeping
  `targetSdk 36` and `minSdk 26`. All 42 JVM tests, Ktlint, first-party core/app
  lint analysis, Debug APK assembly, the 18-test instrumentation APK compilation,
  Room schema export, and the 141-component CycloneDX license policy pass. The
  debug APK is 13,868,835 bytes with SHA-256
  `dbd9082aba4ed762163c4acf0a7182f234abd2c421b8b9adbb3079e3e5499e7c` and a valid
  v2 debug signature. Lint retains only seven reviewed version/target advisories.
- Hardened the Android candidate with explicit no-backup extraction rules,
  API-correct microphone foreground-service dispatch, plural resources,
  monochrome adaptive icons, and tested fail-closed custom-CA/fingerprint trust.
  CI and Tag workflows now run the all-module `test` aggregate rather than
  silently omitting `core:api` and `core:model` tests.
- Extended the Android Tag candidate to build and fail-closed verify an unsigned
  release APK and AAB, 141-component CycloneDX SBOM, and exact SHA-256 manifest.
  The local APK is 9,923,588 bytes with SHA-256
  `a4bcfd2f70b3d344a80df278807ffca0557893d7e0808ff9dede571d9fe55c36`;
  the AAB is 9,481,844 bytes with SHA-256
  `5a8e5c78a67dff8c2ecb61a58f972e90d4010e3a81e26ac0a33c2d7c47416fca`.
  Repository guidance keeps upload/release keys outside CI and requires external
  signing plus device validation before publication.
- Smoke-tested the external Android signing guide with a one-day throwaway key:
  zip alignment, APK v2/v3 signing and certificate verification, AAB JAR signing,
  and final SHA-256 generation all passed. The temporary keystore and signed
  copies were deleted; this verifies commands, not a release identity.
- Added and locally proved deterministic Console and Site release packaging.
  Each static bundle was built, licensed, checksummed, safely extracted, and
  compared twice. Identical Console and Site archive SHA-256 values were
  `b093e0c9bd6028cb5a09090c624d0dc7b5adcd76e2b3f41dfaa2d41275e6d861` and
  `df631d2cc7e8f09d98d55ae6f738d7d896906362cd7224c6e6e3740fcfb6244d`.
- Reviewed all four Site Dependabot pull requests. Integrated the signed
  `pnpm/action-setup` `v6.0.9` commit, Sharp `0.35.3`, and Astro `7.1.0` after
  pnpm 11's default 24-hour minimum-release-age elapsed; retained TypeScript 6.x
  because Astro rejects TypeScript 7. PRs #1, #2, and #4 are locally integrated,
  while #3 was closed without merging. The same signed Action commit now covers
  every Console CI/browser/Tag job, eliminating the shared Node.js 20 Action
  runtime warning. No PR was merged or dependency policy bypassed.
  A 2026-07-17 read-only refresh confirms #1 and #2 have green
  build/secret-scan/SBOM checks. #4 remains red only because its 2026-07-16 build
  ran before Astro 7.1.0 cleared `minimumReleaseAge`; the same dependency passes
  the current local full gate, and no remote check was rerun.
- Found that Pagefind could emit the `en` and `zh-cn` language-map keys in
  scheduler-dependent order. Static builds now canonicalize that generated JSON;
  a fresh double build proved byte-identical archives rather than weakening the
  exact-tree comparison.
- Integrated Site Dependabot PR #4's Astro `7.0.9` -> `7.1.0` scope locally only
  after the registry package exceeded the enforced 24-hour minimum-release-age.
  A targeted update attempted to move Vite to `8.1.5`; the lock was deliberately
  kept on its reviewed `8.1.4` version so no unrelated dependency drift remained.
  Frozen install, Astro check, contract/API/i18n checks, 51-page static build,
  accessibility, links, licenses, and high-severity audit all pass. The GitHub
  PR remains open and was not rerun or merged. Two formal release builds passed
  checksum, safe-extraction, and exact-tree verification with identical archive
  SHA-256 `0f02adc4221f027e04820691af49a7a7a0e21b008f8d049eabbab47ba6d2bf44`
  after the bilingual monitoring evidence update; two normalized static trees
  also matched at
  `2e72d14afc2539225b3ded85506f1a5bdbb790ee737d47d96029c14962948906`.
- Re-ran the current local five-repository gate after release/dependency changes:
  Server test/vet/build, Console formatting/contract/lint/66 tests/typecheck/build/
  licenses, Android all-module tests/Ktlint/Release lint/APK/AAB/SBOM, MCP
  vet/coverage tests/build, and Site 67-operation contract/i18n/build/accessibility/links/
  licenses all pass. This is local candidate evidence, not default-branch CI.
- Advanced all five repositories to additive contract `0.11.0`. Upload completion
  atomically queues a bounded `generate_waveform` job; Worker renders a
  deterministic 1600x256 PNG with fixed FFmpeg settings, commits exactly one
  immutable derivative, and authenticated GET/HEAD verifies MIME, size, SHA-256,
  ETag, and ranges. Console adds decoded waveform display, pointer/keyboard seek,
  timestamps, 0.75–2x playback, and an audio-only pending fallback.
- Deployed Server/MCP `0.1.0-dev+phase6.20260717.2`, migration 11, and the
  matching Console to the isolated `10443` topology after independently verified
  code and database/object backups. Migration backfilled nine originals; two
  real browser runs brought retained state to 11 originals, 11 waveforms, 11
  succeeded jobs, zero failed/pending jobs, and six waveform-read audits. The
  final Chromium run verified authenticated PNG bytes/signature, decoded UI,
  playback speed, search, Mock ASR, manual review, and automatic approval in
  14.0 seconds. Strict-CA performance and official-SDK MCP reads pass. The
  retained deployment bundle SHA-256 is
  `3084cf2769e9ffeff0b37b2f61fbbf83815cc4d8b421dba078143f37a11a0a6f`;
  public Caddy PID `18314` and gateway PID `96419` stayed unchanged, all service
  restart counts remain zero, and post-cutover error journals are empty.
- Added a production-renderer waveform performance gate. Two consecutive runs
  each rendered and fully validated twelve 1600x256 PNGs at concurrency 3 with
  complete temporary-file cleanup; the latest reached 11.5 ops/s and 317.440 ms
  p95, within the 1 op/s and 3 second release-candidate floors.
- Started the S3-compatible storage release slice without changing the active
  local deployment. Object results now carry a typed `local`/`s3` backend;
  original, provider, waveform, clip, and export commits persist that actual
  backend instead of hard-coding `local`, and every read/reaper path rejects a
  mismatched driver before touching a key. The shared Driver uses cancellable,
  seekable object snapshots so a remote implementation can remove downloaded
  temporary files on close. S3 configuration now validates HTTPS or loopback
  development endpoints, canonical buckets/prefixes, paired static credentials,
  custom-CA use, path-style mode, and a dedicated temporary root. Added the
  SDK-independent S3 semantic core: conditional create-only publication,
  byte-exact retry/conflict detection, verified multipart assembly, disposable
  seekable read snapshots, ETag-guarded integrity deletion, and bounded exact-
  prefix part cleanup. Fake-protocol tests cover tampering, pagination, foreign
  keys, cancellation, cross-instance races, and cleanup. The concrete S3 SDK
  network adapter and its dependency are not yet added.
- Advanced all five local repositories to additive contract `0.12.0`. Owner-only
  permanent deletion now requires an already-trashed asset, the exact canonical
  asset UUID, `If-Match`, and an idempotency key. A hidden `purging` state and
  durable job delete integrity-checked storage before the relational graph,
  retain audits, and safely resume terminal failures without crossing prefixes
  or workspaces.
- Passed the complete local `0.12.0` candidate gate: Server tests/vet/build/
  OpenAPI, Console format/lint/typecheck/69 tests/build/licenses plus an
  accessible mocked purge workflow, Android 42 JVM tests/Ktlint/lint/Debug APK,
  MCP verify, and Site's generated 69-operation bilingual reference. Disposable
  real-PostgreSQL schemas passed fresh/repeated/down migrations, every upgrade
  from versions 1–11, legacy preservation, waveform immutability, storage-first
  purge, integrity rejection, and terminal resume.
- Deployed Server/MCP `0.1.0-dev+phase6.20260717.3`, migration 12, and the
  matching Console to the isolated `10443` topology after a verified 0.11 code
  snapshot and offline database/object backup. A strict-TLS Chromium flow
  uploaded a dedicated WAV, completed Mock ASR, trashed it, typed its exact UUID,
  and observed the durable purge job succeed. The asset graph and both stored
  objects were gone, retained asset/object totals returned to 11/42, request and
  completion audits remained, and browser cookie/Web Storage were empty after
  sign-out. The official MCP SDK again passed unauthenticated 401, 21-tool
  discovery, capability, and list calls. The retained deployment archive SHA-256
  is `34eb142be272356522a2fc80346944be39f4247835401b3dc7be0450b7fec1c2`;
  public Caddy PID `18314` and gateway PID `96419` stayed unchanged, service
  restart counts are zero, and post-cutover error journals are empty.
- Closed a follow-up Console privacy gap: after a purge reaches `succeeded`, the
  matching upload/audio/transcript workflow is now removed from browser memory
  without requiring a refresh; nonmatching and nonterminal purges leave local
  state intact. The 69-test Console gate and all three default mocked Chromium
  flows pass, and a
  new opt-in deployed test repeated a real upload/transcription/purge, waited for
  all asset jobs to become terminal, observed the Server `404`, confirmed the
  immutable result disappeared immediately, and signed out with empty Cookie
  and Web Storage. The Console-only r2 archive SHA-256 is
  `0a7a72f6f523d76c9ef05ca4b2277f2a79094873228bf4766c9c77e27cb93c39`;
  its rollback snapshot is `backups/console-before-contract-0.12.0-r2`. Final
  totals are again 11 assets/42 objects, both Caddy PIDs stayed unchanged, all
  four services have zero restarts, and their error-priority journals are empty.
- Added bounded process-local Prometheus HTTP metrics and complete Info-level
  request logs without raw path/query labels. Unsafe caller request IDs are
  replaced before logging. Local tests/vet/build and an exact-tree Linux race
  run pass. Server API/Worker `.4` is deployed with direct-loopback scrape,
  method rejection, log-redaction, and strict-TLS gateway non-exposure checks.
  The retained archive SHA-256 is
  `21b59922f3bc8aa3679f7e98cd4fb37b5583ceebe57ca4c67ebed94faefe0abb`;
  rollback snapshot `server-before-metrics-20260717.4-r1` verifies cleanly.
  API/Worker PIDs are `131104`/`131105`; unchanged MCP `124649`, gateway `96419`,
  public Caddy `18314`, zero restart counts, and empty error journals confirm no
  unrelated service was changed. The CI-pinned `govulncheck v1.6.0` reports no
  reachable vulnerabilities in the current Server tree.
- Upgraded MCP's existing indirect `golang.org/x/sys` dependency from `0.41.0`
  to `0.44.0` after `govulncheck v1.6.0` identified the unreachable Windows
  advisory `GO-2026-5024`. Tests, vet, build, and a final verbose vulnerability
  scan pass with no reachable or required-module vulnerabilities.
- Replaced request-duration sums/counts with fixed cumulative Prometheus
  histogram buckets and deployed Server API/Worker `.5`. Local tests/vet/build,
  the exact-source Linux race suite, live bucket checks, and a rollback exercise
  pass. API/Worker PIDs are `136112`/`136118`; their SHA-256 values are
  `4da170d0e28c692b6f4fc4d1013147db772ac5476ece66dfc573f5842e25f2c5` and
  `d55ba25f1b2b08831021d1b76d50ce9e91526edaa073206ef8e711eba805dbef`.
  The retained `.5` archive SHA-256 is
  `8f2ff94d780bdefcbd95a98ec63db085a5fd94e5f1a4cdd1d1fec0cd10f04dc7`;
  rollback snapshot `server-before-histogram-20260717.5-r1` verifies cleanly.
- Added and deployed checksum-pinned Prometheus 3.13.1 on loopback port `19090`
  with 7-day/1-GiB retention, two healthy targets, and four unit-tested healthy
  alert rules. Its TSDB preserves range-query history across restart, the service
  hardening score is `2.9 OK`, configuration reload preserves its PID, UFW has no
  private-port rule, and public HTTP probes receive no Prometheus response. The
  CI workflow now validates the same configuration/rules with the official
  archive SHA-256
  `962b812371aff838d152b6ff2d56fdb7a6396f5542f48ebf73421b9721f0d103`.
- Advanced all five repositories to contract `0.13.0` and implemented bounded
  `admin:read` Job, Audit Log, and System Status models. Server unit and real-
  PostgreSQL tests cover workspace isolation, filter-bound cursors, reduced
  fields, and fail-closed read auditing; Console adds Job Center, Audit Log,
  live Dashboard, and System Status with 76 unit tests and accessible browser
  coverage. Deployed API/Worker `.6`, MCP `.4`, and the corrected Console bundle
  pass strict-TLS Chromium and official-SDK MCP read smokes. Only `10443` is
  public, all candidate services have zero restarts, and the public Caddy PID
  and configuration hash are unchanged.
- Advanced all five local repositories to contract `0.14.0` and added
  workspace profile and member administration. Server exposes an audited
  `admin:read` profile and filter-bound member inventory plus Owner-only,
  exact-ETag profile rename, member creation, and conditional role/status
  updates. It enforces last-active-Owner safety, hashes write-only passwords,
  and atomically revokes sessions/API keys on disable. Unit, HTTP, OpenAPI,
  full Go, and disposable real-PostgreSQL migration 1–14/lifecycle tests pass;
  the repository update timestamp is strictly monotonic even when the
  application clock trails PostgreSQL. Console adds Workspace and Members routes
  with Admin-read/Owner-write controls and immediate password-field clearing; 89
  Vitest tests and all five default mocked Chromium flows pass, including
  exact ETags, empty Web Storage, no explicit Authorization header, and zero
  axe violations. Android's 71 JVM tests/lint/debug build, MCP verification,
  and Site's 77-operation bilingual static gate pass at the same pin. This
  local slice was not deployed, committed, pushed, tagged, or released.
- Advanced all five local repositories to contract `0.15.0` and added
  session-only personal password rotation. Server verifies the current password,
  atomically replaces its PBKDF2 hash, revokes every active session for that user
  across workspaces, and writes a credential-free audit under a separate
  five-per-minute limit. Unit, HTTP, OpenAPI, full Go, and disposable real-
  PostgreSQL tests prove rollback on audit/hash races, old password/token
  rejection, cross-workspace revocation, monotonic timestamps, and clean audit
  metadata. Console adds an Account route that clears all three password fields
  before validation or I/O and clears local identity without a redundant logout.
  It also adds a public, read-only Version Information route over the shared
  fail-closed capability store; incompatible observations remain diagnostic and
  never become ready state. Its 95 Vitest tests and all six default mocked
  Chromium flows pass with zero axe violations. Android's typed redacting client
  passes 73 JVM tests, Ktlint, lint, and Debug APK assembly; MCP verification and
  Site's generated 78-operation,
  25-page bilingual/51-file static gate pass at the same pin. This slice was not
  deployed, committed, pushed, tagged, or released; the isolated deployment
  remains unchanged on `0.13.0`/migration 12.
- Accepted ADR 0013 after confirming that the existing `system_settings` table
  is deployment-global while Owner and `admin:*` authority is workspace-scoped.
  Deployment settings remain operator-owned; future workspace settings require
  dedicated tenant-keyed, versioned persistence and transactional audits. No
  unsafe settings mutation API was added.
- Rebuilt the current Android `0.15.0` Debug APK for physical-device testing.
  Build Tools 37.0.0 produced package `com.voiceasset.android` version `0.1.0`
  for minSdk 26/targetSdk 36. `apksigner` verifies one Android Debug RSA signer
  with APK Signature Scheme v2; the 14,124,998-byte APK SHA-256 is
  `d18c8ba9a0352956ee02b7e525016e9b4446617dbb4b341a6680d253b2ee5d47`.
  A byte-identical copy is available in the user's Downloads directory for
  manual installation. This is development-signing evidence only; physical-
  device execution and release signing are not claimed.
- Added an Android compatibility path for supported servers without the optional
  `incremental_sync` capability. The typed client validates one-workspace asset
  pages, stable descending order, strict fields, UUIDs, timestamps, limits, and
  cursor progress. WorkManager follows at most 100 pages from the stable catalog;
  Room upserts only newer resource versions, preserves higher tombstones, leaves
  the incremental checkpoint untouched, and never infers deletion from absence.
  Profile save/select and **Refresh server assets** enqueue the unique worker.
  A credential-redacted strict-TLS smoke against the isolated 10443 deployment
  verified contract `0.13.0`, absent `incremental_sync`, two valid assets at
  `limit=2`, and a continuation cursor; the temporary session was revoked and no
  credential value entered output or the workspace; explicit logout returned
  `204`. No Server binary, migration,
  service, gateway, or public Caddy configuration changed.
- Added the Android mobile-administration control-plane slice without a new
  dependency. The typed API strictly validates bounded administration jobs,
  internally consistent workspace status, credential-free ASR/LLM Profile
  families, UUIDs, timestamps, versions, and strong response ETags. The app
  resolves only the active Profile and Keystore session, displays system/job/
  Provider state on explicit refresh, maps permission/TLS/conflict failures,
  and sends only `enabled` or `disabled` with exact `If-Match`; no SSH, shell,
  arbitrary command, or Provider credential reaches Compose. Health checks are
  also explicit and validate profile identity, time, status/error semantics,
  and ASR-versus-LLM error families before retaining only the safe
  classification. The isolated
  `0.13.0`/10443 Owner smoke returned 2 bounded jobs, 0 ASR Profiles, and 1 LLM
  Profile with valid system totals. A reversible LLM state smoke initially
  stopped after the disable response while checking header casing; the recovery
  request immediately used the returned latest version to restore `enabled`.
  Final verification proved 1 enabled LLM Profile, zero queued/running/retry-
  wait jobs, an explicitly revoked session, and both public Caddy and the
  isolated gateway active with zero restarts. No credential value entered
  output or the workspace. A separate Mock LLM health smoke returned `healthy`
  at `2026-07-18T02:31:43.39150768Z`, revoked its temporary session, and again
  left both Caddy services active with zero restarts.

## In Progress

- The coordinated `0.22.0` slice is merged and deployed. It covers the
  authenticated WebSocket realtime transport, signed Owner Webhooks, SDK-backed
  S3 storage, and loopback OpenTelemetry/Alertmanager evidence. The isolated
  `10443` deployment remains healthy; physical Caddy and the public certificate
  are unchanged.
- The full v1.0 product scope is not complete. Remaining gates include the
  Android physical-device/process-death/network-recovery acceptance, the
  complete Compose installation workflow beyond the hosted HTTP smoke, QR
  scanning and the remaining safe configuration surfaces, broader policy/device
  models, and the complete A–E acceptance scenarios in `GOAL.md`. Hosted Linux
  OCI candidate builds and retained digests now pass.
- Alibaba and Tencent offline fixture gates pass. Tencent Flash has current
  credential-backed live WAV evidence; Aliyun still lacks a complete supported
  credential pair, so no Aliyun real-cloud success is claimed.
- The release notes remain a draft and `v1.0.0` is intentionally untagged until
  the unchecked release checklist items have retained evidence.

## Next Work

Close the remaining release gates in this order: run the Android physical-device
acceptance (or obtain an accelerated emulator), execute the full A–E scenarios,
complete the Compose installation workflow beyond the hosted HTTP smoke,
complete QR and remaining safe-configuration flows, and finish Aliyun live
evidence if a valid credential pair is available. Update the release checklist
only from retained test/deployment evidence; do not tag or publish `v1.0.0`
before every required item is checked.

## Blockers

No blocker remains for Mock-based development or the merged default-branch CI.
Docker is unavailable on this Windows host and on the Debian test host, so a
local Compose installation cannot be run here; hosted Server CI now proves the
full startup path through the Console gateway, and hosted Tag OCI builds with
their retained digests are green.
The Google Android SDK is installed at `C:\tools\Android\Sdk`; JVM, lint,
release, signing, and Hosted Emulator gates pass, but local hardware acceleration
is unavailable and no physical device has been attached. Enabling a Windows
hypervisor/driver requires separate administrator authorization; a physical
device is the non-system alternative. The user-supplied PostgreSQL endpoint is
used only through disposable schemas with redacted output.
Windows Go race builds remain blocked by the host cgo toolchain. The exact
Server worktree passed `go test -race -count=1 ./...` on the Debian host with a
checksum-verified official Go 1.26.5 Linux archive; the merged hosted gates cover
the submitted candidate. No v1.0 tag or release has been created.

Vendor live ASR is not a release-candidate blocker, by design. Tencent Flash
has a current credential-backed live WAV success after the account APPID was
used instead of its UIN; Alibaba is skipped until either a token-only credential
or a complete AccessKey pair is available alongside its AppKey. Fixtures and
Mock providers remain green, and no stored credential was changed or exposed.

The authorized Debian test host has no Docker. Its existing Caddy 80/443 routes
remain unchanged; the tested native artifacts use independent systemd units
under `/data/apps/caddy/voice` on port `10443`. The managed PostgreSQL endpoint
is reachable from the workstation but timed out from the Debian host, so the
test deployment uses a dedicated loopback-only PostgreSQL 15 database.

## Cross-Repository Dependencies

- Server: `https://github.com/getio0909/voice-asset-server`
- Console: `https://github.com/getio0909/voice-asset-console`
- Android: `https://github.com/getio0909/voice-asset-android`
- MCP: `https://github.com/getio0909/voice-asset-mcp`
- Site: `https://github.com/getio0909/voice-asset-site`
- Console, Android, MCP, and Site record Server contract `0.22.0`; Console,
  Android, and MCP reject incompatible API/contract versions.
- `make compatibility` executes the real Server offline capability model and
  verifies exactly five repositories, every contract/API pin, 32 Console and
  six Android required features, MCP identity, and the byte-identical Site
  OpenAPI copy. Eight isolated compatibility fixtures and `actionlint` pass;
  hosted run `29655623342` also passed against the merged default branches.

## Recent Test Results

Validated on Windows amd64 and the isolated Linux amd64 host on 2026-07-17–18 UTC.
The merged default-branch checks are also green: Server CI `29655623363`,
Workspace Compatibility `29655623342`, Console CI `29655632775`, Android CI
`29655641245`, MCP CI `29655652283`, and Site CI `29655685955`.

- Server (Go 1.26.5): current contract `0.22.0`, ordinary tests, vet, command builds,
  OpenAPI lint, module verification, backup/artifact tests, refresh/device-session
  unit/HTTP/real-PostgreSQL tests, full-text asset search, waveform generation/
  delivery, permanent asset purge, administration read/member/workspace models,
  session-only atomic password rotation and all-user-session revocation, and
  migration 1–18 upgrade tests, transactional incremental-asset change and
  personal-notification tests, SSRF
  DNS-pin tests, and bounded
  local FFmpeg smoke plus repeated 12-clip and 12-waveform real performance runs
  passed. After the storage-backend/context refactor and SDK-backed S3,
  WebSocket, and Webhook slices,
  `go test -count=1 ./...`, `go vet ./...`, formatting, and diff checks passed
  again across commands, domains, E2E, and performance packages. The new
  workspace compatibility gate and all five repository/capability/OpenAPI drift
  fixtures pass against the real `adminctl capabilities` output. The bounded
  failed-job retry endpoint additionally passes unit, HTTP, OpenAPI, and real-
  PostgreSQL coverage for workspace isolation, unsupported lifecycle states,
  duplicate requests, the hard 20-attempt ceiling, and atomic asset/job/audit
  transitions. One-time device pairing additionally passes service, HTTP,
  atomic real-PostgreSQL, migration, strict-origin, replay, expiry, rate-limit,
  cookie, and OpenAPI coverage. The read-only deployment settings projection
  additionally passes service/startup/HTTP tests for its exact allowlist,
  malformed workspace and permission denial, query rejection, mutation `405`,
  and credential-free audit. Storage package
  coverage is 73.5%; the cross-instance conditional-publication test also passed
  20 consecutive runs. Typed backend propagation includes S3-valued repository
  fixtures without requiring network credentials. Disposable real-PostgreSQL
  schemas also pass storage-first purge, wrong-integrity rejection, terminal
  resume, every historical upgrade, and waveform-trigger preservation. The
  preceding `0.18.0` full Windows `-race` suite passed with process-local
  compiler flags. The installed GCC 16/cgo path now exits before compiling the
  `0.22.0` race rerun, so this slice does not claim a fresh Windows race pass.
  The latest waveform run reached 11.5 ops/s and 317.440 ms p95. The
  exact pre-waveform tree also passed
  `go test -race -count=1 ./...` on
  Linux. The runtime image/Compose CI gate is defined but not run locally because
  Docker is absent.
- Console (Node 24.15, pnpm 11.5): Prettier, zero-warning ESLint, contract-pin
  check, typecheck, 110 Vitest tests, production build, licenses, Webhook UI,
  and seven local Chromium
  catalog/detail, ASR-to-approved, API-key, Provider/Hotword, LLM/Glossary, and
  exact-ID permanent-purge plus administration, membership, workspace, and account Chromium
  workflows with zero axe violations passed. The membership flow proves
  password-field clearing, exact version ETags, safe request bodies, empty Web
  Storage, and cookie-only browser authentication. The workspace flow proves
  current-version `If-Match`, audited-profile semantics, and the same browser
  credential constraints. Pairing-session creation additionally validates the
  exact custom URI, keeps its secret in memory only, masks it by default, and
  clears it on expiry, navigation, refresh, or explicit action. The public
  Version Information view reports the
  observed Server/API/contract identity and sorted features without credentials
  or writes, remains fail-closed on incompatibility, and passes axe. The Account
  flow proves immediate three-field password clearing on both failure and success,
  cookie-only transport, no redundant
  logout, local identity removal, and zero axe violations. The new System
  Settings flow proves two safe GETs, no mutation request, no form/save control,
  empty Web Storage, and zero axe violations.
  Deployed Chromium runs, including the `0.12.0` exact-ID permanent-purge flow,
  additionally prove strict access/refresh cookie attributes, token rotation,
  personal-device inventory/revocation, asset processing/annotation reads,
  versioned metadata restoration, Collection/Tag/status/date filtering,
  assigned-tag reads, trash/default exclusion/restore, authenticated waveform
  PNG/signature/decoded display, playback speed, immutable transcript export/
  download integrity, cleanup, and empty browser token storage.
  The deployed `0.13.0` operations flow additionally validates Job filters,
  exact reduced-field allowlists, Audit Log, Dashboard/System Status, no
  unexpected writes, and empty Web Storage.
- Android (JDK 21, Gradle 9.5, contract `0.22.0`): all 134 current JVM tests pass. They cover
  strict pairing-URI parsing, one-time claim, Keystore persistence and profile-
  save compensation, complete encrypted access/refresh persistence, serialized
  native refresh, and revalidated two-step device-session revocation in addition
  to dual-cookie validation, strict Origin/Cookie behavior, stable retry keys, exact local/server
  part comparison, job terminal-state recovery, strict revision validation,
  offline transcript invariants, strict real-time events, lost-ACK replay,
  reconnect/final-result consistency, local-first PCM capture, WAV repair, and
  network fallback. Their Ktlint checks, all
  app/main/test/androidTest Ktlint checks, debug runtime dependency resolution,
  secret-pattern scan, and the 141-component CycloneDX license gate passed on
  the preceding `0.11.0` candidate. At contract `0.22.0`, all 134 JVM tests,
  Ktlint, `lintDebug`, and `assembleDebug` pass. The SBOM policy recognizes
  only same-build `project_path` modules as first-party and still rejects a
  synthetic external dependency without license metadata.
  The all-module `test`, Ktlint, first-party core/app Release lint, Debug/Test APK,
  unsigned release APK/AAB, Room schema, and 141-component CycloneDX policy pass.
  The 134 JVM tests have zero failures; 43 instrumentation methods compile and
  the merged Hosted Emulator run executes all 44 instrumentation tests. Room
  migrations 2 through 4, atomic per-page cursor/cache
  updates, deletion tombstones, capability-selected incremental/catalog paging,
  legacy catalog cursor bounds, and transaction rollback have dedicated coverage.
  The active profile's Room cache now feeds a
  bounded Compose list of its 50 most recently updated assets while retaining the
  full offline count in UI state. Saved profiles can now be selected explicitly;
  recording, transcript, and asset state switch together, while capture blocks
  profile changes. A second bounded projection exposes the active profile's 50
  most recent local recordings with upload/transcription state, progress, offline
  transcript availability, and errors even when the Server lacks 0.16 incremental
  sync. One case-insensitive query filters both cached assets and local recordings
  by stable identity, metadata, status, and error fields while retaining total/
  match counts and the 50-row cap; hidden active playback keeps standalone
  controls. Failed and blocked rows now expose a manual retry that reconstructs the
  durable checkpoint, preserves asset/upload idempotency, and advances a persisted
  transcription generation instead of replaying a terminal job. New profiles
  independently select upload and batch-transcription policies. WorkManager chains
  the two stages with their own network/charging constraints, while manual upload
  and transcription expose explicit row actions at durable checkpoints. The next
  recording can inherit either Profile default or snapshot a nullable override;
  restart, retry, and manual actions resolve the persisted session choice.
  Non-trashed cached assets can now load their latest public-API representation
  and strong ETag, then replace title, language, and nullable Collection with an
  exact `If-Match`. Conflicts and ambiguous transport failures require a fresh
  read; successful responses refresh only the matching Room row without moving
  the incremental cursor, and stale pages cannot regress a newer resource
  version. Explicit mobile-administration refresh now projects validated
  workspace status, at most 20 recent credential-free jobs, and ASR/LLM Profile
  states. Versioned Profile enable/disable sends an exact `If-Match`, updates
  the local count only after an identity/family/state/version-consistent
  response, and exposes permission, conflict, TLS, and protocol failures without
  retaining Provider config in Compose. An explicit health action validates the
  family-specific response and retains only status, safe error class, and check
  time; the deployed Mock LLM returned `healthy`. Saved
  recording playback and export share Room identity, canonical-path, byte-length,
  and SHA-256 verification; writes, traversal, and corrupt files fail closed.
  Playback models prepare/play/pause/resume/stop/failure, keeps one active engine,
  and handles audio focus and noisy-output changes. Export grants a non-exported,
  read-only M4A/WAV content URI. The replacement 14,619,252-byte application APK
  has a valid v2 debug signature and SHA-256
  `7eb84ec921b27140b151cd3bfe2bcb8136e5837c67718de1d916721ebcbadfd2`.
  The unsigned release APK/AAB pass exact metadata, structure,
  SBOM, and checksum verification. The API 35 x86_64 image and AVD are installed,
  but the emulator exits before ADB because no Android hypervisor driver is
  installed. The administrator-only recovery step is documented; microphone/
  device recovery remains open until it is executed.
  Hosted run `29655641245` passes on the merged Android default branch; the
  signed release artifact is retained from run `29655228357`.
- MCP (Go 1.26.5, MCP Go SDK 1.6.1): the `0.22.0` pin, ordinary tests, vet, build,
  12 read and nine opt-in write tools, five resources, six prompts,
  scope/failure/pagination/cancellation/rate-limit tests, and real stdio/HTTP
  tool calls passed. The deployed read-only test additionally proves strict CA,
  unauthenticated `401`, 21-tool discovery, `0.13.0` capabilities, asset listing, and
  immutable Agent read audit plus latest-Revision Segment search with exact
  timecodes without creating artifacts. `govulncheck v1.6.0` reports no
  vulnerabilities after the bounded `golang.org/x/sys` `0.44.0` update. The
  preceding `0.18.0` Windows `-race` suite passes; the current cgo toolchain
  blocks a fresh `0.22.0` race build before package compilation.
- Site (Astro 7.1.0, Starlight 0.41.3, Sharp 0.35.3): contract `0.22.0`,
  generated 91-operation API reference, 25-page locale parity, type/content
  checks, static build, accessibility structure, dependency audit, and links
  across 51 HTML files pass. Two complete release builds produced identical
  safe-extraction-verified archives with SHA-256
  `0f02adc4221f027e04820691af49a7a7a0e21b008f8d049eabbab47ba6d2bf44`.
- Performance (isolated Debian `10443`, current `0.22.0` deployment): the
  system-trust/hostname, no-redirect,
  no-ambient-proxy control-plane smoke completed 400 measured requests at
  concurrency 8 with zero failures, 42.0 req/s throughput, and 227.286 ms p95
  against the isolated VoiceAsset gateway. The 500 ms p95
  and 20 req/s acceptance budgets pass.
- Performance (isolated PostgreSQL schema): with 5,000 seed rows, 100
  asset-create/audit transactions passed at 900.6 ops/s and 41.758 ms p95; 400
  list/title-search reads passed at 194.2 ops/s and 54.092 ms p95. Both ran at
  concurrency 8 with zero failures, and cleanup left zero matching schemas.
- Performance (supported local pipeline): eight two-part 5.24 MiB WAV
  upload/storage/probe operations passed at 9.6 ops/s and 499.027 ms p95; eight
  Mock Worker/transcript operations passed at 53.6 ops/s and 107.040 ms p95;
  32 full-hash audio opens passed at 186.9 ops/s and 32.332 ms p95. All ran at
  concurrency 4 with zero failures and disposable state.
- Migration (real PostgreSQL): versions 1–17 each upgrade independently to
  version 18, and staged v1 → v2 → v18 preserves representative legacy data,
  the access-session/lifecycle backfills, and deterministically queues existing
  originals for waveform generation while backfilling active version-one
  memberships. `TestApplyAgainstPostgreSQL` also passes initial/idempotent
  application, checksum-change rejection, and all eighteen down
  migrations; every test schema is disposable.
- The current post-slice read-only remote audit found API/worker/MCP/Prometheus/
  gateway PIDs `154944`/`154946`/`154945`/`136995`/`146764`, all active with zero
  restarts; public
  Caddy remains PID `18314`. Only gateway `10443` is public for VoiceAsset,
  private services remain loopback. The independent gateway now reuses the
  public `api.getio.net` certificate through restricted root-managed symlinks
  and a gateway-only reload timer; external system-trust validation passes with
  the same leaf on 443 and 10443 (certificate SHA-256
  `8caf123add29eca48bb2a9d2d40185a74589bbd291f8fd732990479cc71de0ff`,
  valid through 2026-10-15 UTC). The public Caddyfile SHA-256 remains
  `b5758330e82589f33ead4f0cb4556544275f3adcbc9098268123e151dfc766ae`, and
  error-priority journal entries since this slice remain zero. The API reports
  version `v0.1.0-dev+workspace.20260718.11`, contract `0.22.0`, 45 sorted
  capabilities, and schema 18. The retained deployment archive has SHA-256
  `8f86c245063e244b57b0c2a779f8f099c78404342e3e7128ba3905eef5de23f5`;
  the exact pre-cutover backup archive is
  `c5848c1ffd2291093c02f467b2fb1a853ec98a5f0d05825a1deab2a936613abf`.
  Strict-TLS personal-event acceptance passed Session/API-key boundaries, 35
  safe ordered events, an empty checkpoint, authenticated workspace/user cursor
  binding, tamper/method rejection, safe audits, logout, and post-logout denial.
  The preceding System Settings acceptance passed 401, exact allowlist read,
  query/mutation denial, immutable audit, and logout. The preceding pairing
  acceptance passed exact-Origin denial, claim, session
  reads, replay rejection, explicit logout, SHA-256-only persistence, and log
  redaction. The earlier disposable real
  retry kept the same Job UUID, moved a failed transcription asset back to
  processing, preserved attempts `3`, raised `max_attempts` from `3` to `4`,
  rejected a duplicate with `job_not_retryable`, and left one credential-free
  immutable audit after mutable fixture cleanup.
- All ten GitHub CI/release workflow files pass `actionlint`; Server and MCP each
  pass two full six-target release builds with identical archive hashes, complete
  SHA-256 verification, target/contract/content inspection, and host runtime
  version checks. Console and Site each pass two deterministic static release
  builds with exact archive/content/license/checksum verification. No
  high-confidence secrets or committed `.env` files were found.
- Current Server unit suites and `go vet ./...` pass for auth, asset, upload,
  storage, bounded WAV/AAC-M4A parsing, jobs, ASR adapters/failover, hotwords,
  encrypted provider profiles, LLM adapters, glossaries, correction, review,
  transcription, HTTP API, configuration, secret envelopes, and migrations.
- Credential-backed PostgreSQL integration passes for migrations, rotating
  sessions, assets, uploads, jobs, audio, transcript reads, provider/vocabulary
  storage,
  atomic raw-plus-normalized publication, correction, review, manual approval,
  and validated glossary-only automatic approval.
  Every case creates and drops a unique schema; none resets `public`.
- `TestPhase1OwnerUploadMockTranscriptionAndPlayback` passes against that
  PostgreSQL endpoint: login, create asset, upload two parts, complete WAV,
  enqueue and run Mock ASR, publish raw and normalized revisions, and request
  audio with GET/HEAD/Range. `TestPhase3MockLLMCorrectionReviewAndApproval`
  passes both glossary/profile/correction/partial-review/approval and atomic
  glossary-only automatic-approval lineage.
- `scripts/run-live-console-e2e.ps1` passes the full Phase 3 Mock flow, real
  Provider/Hotword and LLM/Glossary administration, and Phase 4 MCP read workflow
  against the
  same PostgreSQL endpoint with ephemeral Owner/Profile credentials and schema
  isolation. The final run applied all seven migrations, passed all three
  Chromium cases in 15.9 seconds, including manual and automatic correction,
  passed MCP search/revision/exact-range and API-key audit behavior, revoked
  temporary credentials, and removed all transient state.
- The Debian 12 deployment passed migration and service startup with zero
  restarts, contract `0.2.0` readiness through internal-CA TLS, strict external
  certificate-chain/hostname verification, and an external Chromium Phase 1
  flow in 7.3 seconds. Post-test state contained one user, asset, upload, job,
  and revision with two transcript segments and two immutable object files.
- Remote exposure checks show Caddy on `10443`, API only on
  `127.0.0.1:18080`, MCP only on `127.0.0.1:18090`, PostgreSQL only on loopback
  `5432`, no UFW rules for private ports, and no error-priority entries for the
  current API/worker/MCP/gateway processes.
- A full stop/start of only the four `voiceasset-*` units re-ran the migration
  gate, restored API/worker/gateway readiness, retained the deployed user,
  asset, and transcript, and left the existing 80/443 Caddy PID unchanged.
- A cross-compiled Linux storage test binary ran on the authorized Debian host
  for 20 iterations, proving separate-store publication races, immutable-object
  conflict handling, symlink escape rejection, and reopen durability; the
  temporary binary was removed afterward.
- The Debian deployment advertises Server
  `0.1.0-dev+phase6.20260717.5`, MCP `.3`, and contract `0.12.0`; strict external CA chain and
  `api.getio.net` hostname verification pass. The official SDK calls remote MCP
  with separate inbound and six-scope Server credentials; the read-only smoke is
  Agent-attributed. The current key expires in October 2026 and the previous key
  is revoked. Only `10443` is externally reachable while API, MCP, and PostgreSQL
  remain loopback. Migration 12, live file SHA-256, release archive, and the
  pre-upgrade custom-format database/code backup are verified.
- The `.5` API exposes fixed cumulative duration histogram buckets only on
  loopback. Prometheus 3.13.1 retains those samples for 7 days or 1 GiB on its
  isolated TSDB, reports both API and self-scrapes healthy, evaluates four
  healthy/inactive rules, survives reload and restart with historical range
  samples intact, and listens only on `127.0.0.1:19090`. External strict-TLS
  `/metrics` resolves to Console content, direct public HTTP to `19090` times
  out with zero bytes, and no UFW/NAT rule exposes the listener.
- The `.5` worker passed a deterministic filesystem/PostgreSQL reaper E2E and
  left the expired-artifact backlog at zero. Deployment validation also proved
  rollback for an incorrect database-name check before the corrected retry;
  all four binaries are mode `0750`, retained archives are mode `0640`, and
  post-deploy error-priority journal count is zero.
- The `.6` deployment passed a rollback exercise, corrected API/worker restart,
  strict-TLS readiness/capability/static-asset checks, and one credential-free
  Chromium administration smoke. Only `10443` is externally exposed; API, MCP,
  and PostgreSQL remain loopback, all five current-process error journals are
  empty, and neither Caddy nor MCP restarted.
- The deployed Console consumes the contract `0.12.0` asset inventory directly:
  PostgreSQL title/latest-Transcript search, query-bound cursor paging with
  Collection/Tag/status/date/Provider/Speaker filters, bounded Segment hits,
  complete Collection, Tag, assigned-tag, and
  annotation paging, authenticated audio, processing-job history, validated
  note/bookmark creation, detail refresh, and full title/language/Collection
  replacement with exact resource-version ETags. The Console also creates audited
  JSON/Markdown/SRT/WebVTT artifacts for an explicit immutable Revision and
  constructs only UUID-bound same-origin download URLs. Versioned trash/restore
  hides deleted assets from normal reads and preserves immutable source data.
  The gate passes 69 Vitest
  tests, zero-warning lint, type checks, production build, license/contract
  checks, three mocked Chromium workflows, and axe. A new isolated-deployment
  Chromium case filtered, read processing/annotation/tag state, changed metadata,
  trashed, verified default exclusion, and restored one real asset while leaving
  Web Storage empty. A second deployed
  case exported and downloaded an existing immutable Revision, then matched MIME,
  byte length, body, SHA-256, and bounded expiry. The `0.12.0` deployed case then
  uploaded/transcribed a dedicated asset, moved it to trash, submitted its exact
  UUID, observed a succeeded purge, and left no cookie or Web Storage. The
  deterministic retained combined deployment archive is
  `34eb142be272356522a2fc80346944be39f4247835401b3dc7be0450b7fec1c2`;
  public Caddy PID `18314` and gateway PID `96419` remained unchanged, all
  VoiceAsset error-priority journals were empty, and private services remained
  loopback-only.
- Published and deployed OpenAPI contract `0.10.0` and migration 10. Asset discovery
  now uses PostgreSQL full-text vectors for titles and latest immutable
  Transcript Segments, retains literal fallback for non-tokenized languages,
  composes Collection/Tag/status/date/Provider/Speaker filters into cursor
  identity, and returns at most five chronological Segment hits with immutable
  IDs and timecodes. Real PostgreSQL proves reversed-term full-text matches,
  Chinese literal matching, latest-Revision isolation, workspace isolation,
  Provider/Speaker filtering, all five indexes, both generated columns, and
  upgrades from every schema version 1–9. Strict-TLS Chromium and official-SDK
  MCP runs prove the deployed search/timecode workflow; the final post-refactor
  browser run completed the real upload-to-approval path in 13.3 seconds. Both
  Caddy PIDs stayed unchanged and all four post-cutover error journals are empty.
- Added and deployed bounded Console bulk lifecycle actions for up to 100 loaded
  assets. Every trash/restore call carries that asset's exact version, continues
  after an individual conflict, and retains failed rows with a safe message and
  Request ID. Store coverage proves partial success plus two-version restore;
  real Chromium changed/restored metadata, bulk-trashed/restored one deployed
  asset, passed accessibility and empty Web Storage checks, and restored source
  state. The deterministic Console archive SHA-256 is
  `08a8c2ffd4f5078825b31187f1c54767b136db5a404a677cd609b4c87d3ba5a5`;
  all service/Caddy PIDs and zero-restart counts remained unchanged.
- Published and deployed OpenAPI contract `0.11.0` and migration 11. Upload
  completion queues the waveform job inside the original commit; Worker uses a
  fixed, shell-free FFmpeg invocation and atomic PostgreSQL commit to publish one
  immutable PNG. The read path revalidates workspace, asset lifecycle, MIME,
  file size, and SHA-256 before serving conditional/range responses and a
  fail-closed audit. Real PostgreSQL and remote backfill tests cover uniqueness,
  immutability, cross-workspace denial, trashed reads, retry-safe completion, and
  all historical upgrades. Console's real browser gate proves authenticated PNG
  bytes, decode, seek controls, 1.5x playback, search, and the complete Mock
  correction path; the official MCP SDK remains API-only and passes unchanged.
- Published and deployed OpenAPI `0.12.0` and migration 12. The owner-only permanent-purge API, durable
  storage-first worker, retained audits, exact inventory fingerprint, idempotent
  replay/resume, and accessible Console danger-zone flow pass unit, HTTP,
  mocked Chromium, disposable real-PostgreSQL, and strict-TLS deployed tests.
  The real purge removed its asset graph and two integrity-checked files, retained
  user/system audits and the detached succeeded purge job, and restored the
  deployment to its pre-test 11 assets/42 objects. All five repositories pin
  `0.12.0`; hosted CI has not yet run for this slice.
- Published and deployed OpenAPI `0.13.0` without a schema migration. The
  workspace-scoped Job, Audit Log, and System Status reads plus Console
  administration pages pass unit, HTTP, disposable real-PostgreSQL, mocked
  Chromium, and strict-TLS deployed tests. The official MCP Go SDK passes
  unauthenticated `401`, 21-tool discovery, capabilities, and asset listing.
  All five repositories pin `0.13.0`; hosted CI has not yet run for this slice.
- Validated local OpenAPI `0.14.0` and migration 14 without changing the
  isolated deployment. Audited workspace profile read/conditional rename,
  member creation/list/update, cross-workspace denial,
  stale-version rejection, last-Owner safety, login denial after disable,
  session/API-key revocation, audit retention, every-version upgrade, and all
  client contract pins pass. Migrations 13–14 remain undeployed until the
  real-time adapter/startup path is approved and complete; hosted CI has not
  run for this uncommitted slice.
- Validated local OpenAPI `0.15.0` without a schema migration or deployment.
  Session-only password rotation, current-password verification, transactional
  cross-workspace session revocation, credential-free audit, old-token/password
  denial, Console sensitive-field clearing, Android typed/redacted transport,
  and all client contract pins pass. The isolated environment remains on
  `0.13.0`/migration 12; hosted CI has not run for this uncommitted slice.
- Validated local OpenAPI `0.16.0` and migration 15 without deploying application
  code or the migration. Asset mutations write workspace-bound ordered changes in
  their original transaction; fixed-high-watermark paging, immutable snapshots,
  permanent-deletion tombstones, tenant isolation, rollback, backfill, and
  upgrades from versions 1–14 pass on real PostgreSQL. Android Room migrations 2 through 4
  atomically persist per-profile pages/cursors, retry generations, and per-recording
  policy snapshots while safely skipping servers without
  `incremental_sync`; the active profile's cache is now visible offline in a
  bounded Compose list, and profiles can be switched outside an active capture.
  The local recording/sync/transcript join also remained useful against the then-
  deployed compatible 0.13 Server because it does not require the incremental feed.
  A unified case-insensitive offline query filters both cached Server assets and
  local recordings while retaining total/match counts, bounded rendering, and
  controls for an active playback row hidden by the filter.
  Failed/blocked local rows can now be manually retried from the last durable
  checkpoint with a new transcription generation. Upload and batch-transcription
  policies are independently selectable at Profile and recording scope, including
  explicit manual stage actions. Saved recordings can now be played in-app or
  exported only after shared identity, path, size, and SHA-256 verification.
  Playback has explicit prepare/play/pause/resume/stop/failure state, one active
  engine, audio-focus handling, and noisy-output pause behavior. Its replacement
  v2-debug-signed APK is
  `VoiceAsset-0.1.0-dev-contract-0.16.0-provider-health-debug-20260718T023016Z.apk`;
  that checkpoint artifact was superseded after the 0.17 retry slice.
  Console, MCP, Android, and Site pin `0.16.0`; Console's
  95 tests/six mocked browser flows, Android's 109 JVM tests/full build plus 32
  compiled instrumentation tests, MCP's
  coverage/vet/build, and Site's 79-operation static gate pass. The isolated
  application remained `0.13.0`/migration 12 at that checkpoint; only its independent gateway changed
  to the existing system-trusted public certificate, leaving public Caddy
  untouched. The five-repository runtime-capability compatibility gate also
  passes locally; its hosted workflow has not run for this uncommitted slice.
- Completed and deployed the `0.18.0` device-pairing slice without changing the
  deployed gateway or public Caddy. ADR 0015 defines the versioned `voiceasset://pair` trust
  boundary; migration 16 stores only the secret digest and consumption state.
  `POST /api/v1/auth/pairing-sessions` requires a personal session and revokes
  older unclaimed issues, while the unauthenticated claim endpoint uses exact
  Origin, an independent five-per-minute/IP limiter, generic replay/expiry
  errors, strict cookies, and one transaction for account checks, consumption,
  session creation, and audits. Console adds a masked, memory-only copy/clear
  fallback and Android adds strict paste/claim plus Keystore persistence with
  compensation on profile-save failure. Server full PostgreSQL and race tests,
  vet/build/OpenAPI/migration gates, Console's 105 tests and production build,
  Android's 117 JVM tests/full build/Ktlint/Lint plus 35 compiled instrumentation
  methods, MCP coverage/vet/build/race, Site's 82-operation 51-page static gate,
  and all five compatibility fixtures pass. The installable 14,351,328-byte V2
  development-signed APK is
  `VoiceAsset-0.1.0-dev-contract-0.18.0-device-pairing-debug-20260718T044721Z.apk`
  with SHA-256
  `456ab75b011ba0d4b521ff945eb2f806004003469efd271edb7577b7ac9f3d9f`.
  The live acceptance found a subsecond response/URI expiry mismatch; a RED/
  GREEN HTTP regression canonicalizes both to the same conservative whole
  second, and full ordinary/race gates pass. The current deployment is
  `.20260718.3`/schema 16, with verified pre-upgrade and pre-patch backups plus
  the retained archive above. Actual QR rendering/scanning still requires
  explicit production-dependency approval; hosted CI and physical-device
  pairing remain open. This slice is uncommitted and is not v1.0.
- Closed the Android access-token lifetime gap without changing the deployed
  Server or either Caddy service. Login and pairing now encrypt the access token,
  refresh token, and both expiries as one versioned Keystore payload; one shared
  provider serializes rotation and clears unusable local state after rejected
  refresh. A new personal **Device sessions** card explicitly loads only
  credential-free rows, requires a second confirmation, re-reads the Server
  inventory before deleting the exact UUID, and clears this device's local
  credential only after remote success. **Reconnect current server** replaces
  the encrypted session on the exact existing Profile, so self-revocation or
  legacy expiry does not require a duplicate Profile and does not split offline
  recording identity. The password is removed from UI state before network I/O
  and never stored; authentication failure leaves any stored session unchanged.
  All 134 JVM tests, Debug/Release Lint, and Debug/Release APK assembly pass; 41
  instrumentation methods compile but remain unexecuted on a device. The
  14,619,252-byte V2 development-signed APK is
  `VoiceAsset-0.1.0-dev-contract-0.18.0-device-sessions-reconnect-debug-20260718T062602Z.apk`
  with SHA-256
  `9253a623451db78596d1f645b4bb038d8f6e07f4b0423f82e5200ea6481ae1ec`.
  Android and Site now document an exact pairing/offline-record/reconnect
  physical-device acceptance flow without a default password or secret-bearing
  evidence. `adb devices -l` reported no attached device, so that flow remains
  unexecuted. This follow-on is uncommitted and is not v1.0.
- Completed and deployed the `0.19.0` safe System Settings slice without a
  schema change. RED/GREEN service and HTTP tests prove malformed workspace
  denial, `admin:read` authorization, exact field allowlisting, credential-free
  audit, query rejection, and pre-authentication `405` for POST/PUT/PATCH/DELETE.
  API startup derives the projection only from already validated runtime config;
  it never reads the deployment-global table. Console adds typed client/store
  state and an operator-managed, read-only page with no inputs or save action.
  The focused Chromium flow proves two GET reads, zero unsafe requests, empty Web
  Storage, and zero axe violations. Contract `0.19.0` exposes 83 operations and
  42 sorted features; the seven-fixture workspace gate additionally requires
  the advertised route and Console capability pin. Server ordinary tests/vet/
  build/OpenAPI, Console's 107 tests and seven local Chromium flows, Android's
  134 JVM tests/full Debug and Release build plus 41 compiled instrumentation
  methods, MCP tests/vet/build, and Site's 51-page static gate pass. Windows
  race compilation is currently blocked by local cgo/GCC tooling and is not
  reported as passed for this slice. The isolated deployment now runs
  `.20260718.4` on schema 16. Its verified pre-cutover backup is
  `/srv/voiceasset-backups/2026-07-18T0714Z-before-contract-0.19.0-r1`; the
  retained 19,444,593-byte archive SHA-256 is
  `9cfbcc553b8e0907751f40fb926ba5757cf27fe13baf558c42a17ec9349c03aa`.
  Strict-TLS 401, exact allowlist, query/mutation denial, audit, and logout
  acceptance pass with zero error-priority journal entries. Public Caddy,
  independent gateway, Prometheus, both Caddy configs, and the reused certificate
  remained unchanged. The matching 14,619,252-byte V2 development-signed APK is
  `VoiceAsset-0.1.0-dev-contract-0.19.0-device-sessions-reconnect-debug-20260718T071900Z.apk`
  with SHA-256
  `82708cc07bf0b8c148dfaf951314e111ff480b37d5800a7abdbcbfe2e4845a57`.
  Physical-device execution and hosted CI remain open; this is uncommitted and
  is not v1.0.
- Completed and deployed contract `0.20.0` plus migration 17 as
  `v0.1.0-dev+phase6.20260718.5`. RED/GREEN service, repository, HTTP, and real
  PostgreSQL tests cover interactive-Session authorization, API-key denial,
  workspace/user cursor binding, deterministic backfill, terminal retries,
  transaction rollback, recipient isolation, ordering, stable empty
  checkpoints, row immutability, safe fields, and fail-closed read auditing.
  ADR 0016 deliberately separates this replayable pull feed from unimplemented
  outbound Webhooks and live-push transports. OpenAPI and all five fail-closed
  pins are `0.20.0`; the Site reference contains 84 operations and the workspace
  gate has eight fixtures without making Console, Android, or MCP require the
  new capability. All five local gates pass. The development-signed Android APK
  is
  `VoiceAsset-0.1.0-dev-contract-0.20.0-personal-notifications-debug-20260718T085854Z.apk`,
  14,619,252 bytes, SHA-256
  `7eb84ec921b27140b151cd3bfe2bcb8136e5837c67718de1d916721ebcbadfd2`.
  Both `/srv/voiceasset-backups/2026-07-18T0902Z-before-contract-0.20.0-r1`
  and exact-cutover r2 were verified; r2 restored into disposable targets and
  advanced to schema 17 with all 42 storage files before live migration. The
  retained r2 archive SHA-256 is
  `c5848c1ffd2291093c02f467b2fb1a853ec98a5f0d05825a1deab2a936613abf`;
  the deployed bundle SHA-256 is
  `8f86c245063e244b57b0c2a779f8f099c78404342e3e7128ba3905eef5de23f5`.
  Strict public-TLS acceptance returned 35 safe ordered events and passed
  unauthenticated/API-key denial, empty checkpoint, authenticated cursor
  binding, tamper/method rejection, safe audit, logout, and post-logout denial.
  API, Worker, MCP, gateway, Prometheus, and public Caddy are active with zero
  restarts and empty error-priority journals. Public Caddy/gateway PIDs and
  configuration hashes, certificate symlink targets, and the shared 443/10443
  leaf certificate did not change. This slice remains uncommitted and is not
  v1.0.
- Hardened the unadvertised real-time core after the deployed `0.19.0` slice.
  RED tests first proved that both the initial event and established stream read
  had no deadline. Every read now receives a context deadline after three
  advertised heartbeat intervals (45 seconds at the default 15-second cadence).
  An idle established session is persisted as `interrupted`, retains the exact
  Provider stream only for the existing 90-second reconnect window, and remains
  eligible for idempotent resume; a pre-session idle connection creates no row
  or stream. The focused tests, all Server tests, vet, OpenAPI lint, command
  builds, seven compatibility fixtures, and the real five-repository gate pass.
  No WebSocket dependency, runtime capability, API startup wiring, deployment,
  or contract-version claim was added.
- Earlier deployed the coordinated `0.17.0` candidate to the isolated 10443 environment
  as Server API/Worker and MCP version
  `0.1.0-dev+phase6.20260718.1`, migration schema 15, the matching Console, and
  the 80-operation OpenAPI copy. The verified pre-upgrade backup is
  `/srv/voiceasset-backups/2026-07-18T0316Z-before-contract-0.17.0-r1`; the
  retained 19,421,634-byte deployment archive has SHA-256
  `3950784247ce2870f684d4d42b8a209cc025bc144f5260004cdb20aa72b98f61`.
  A real workspace-scoped failed-job retry passed its lifecycle, same-UUID,
  bounded-attempt, duplicate-conflict, safe-audit, and cleanup checks. All five
  services are active with zero restarts and no error-priority journal entries.
  Public Caddy's configuration hash remains
  `b5758330e82589f33ead4f0cb4556544275f3adcbc9098268123e151dfc766ae`;
  both 443 and 10443 still present the same system-trusted `api.getio.net` leaf.
  That checkpoint's development-signed replacement APK is
  `VoiceAsset-0.1.0-dev-contract-0.17.0-job-retry-debug-20260718T032403Z.apk`,
  14,348,236 bytes, SHA-256
  `5a5afed75d841ddef861e58fdf30b1f6a60b8323790414a3792288d6d10965c2`.
  Console's 95 tests/six mocked browser flows, Android's 112 JVM tests/full build
  plus 32 compiled instrumentation methods, MCP coverage/vet/build, Site's
  80-operation static gate, and all five compatibility fixtures pass. Physical-
  device execution and hosted CI remain open; this is not a v1.0 release.

## Unresolved Risks

Highest product risks remain vendor API drift and missing Aliyun live evidence (R-001),
offline recording loss (R-002), correction integrity (R-003), defense-in-depth
egress policy beyond the verified application SSRF controls (R-004),
cross-repository drift (R-005), and unimplemented/unmeasured S3 performance
before that path is promoted (R-014). The real FFmpeg clip and waveform paths
now have retained baselines. Phase 3 now has immutable lineage, validation,
fixture, redaction, real-database, Console review, and remote browser evidence,
and Tencent Flash now has current real-cloud success evidence; Aliyun remains
unproven. R-012 is mitigated for the test
deployment by scoped, revocable Agent keys, a separate inbound MCP bearer, and
the deployed Console lifecycle UI. Rotation, post-revocation 401, one-time
display, and zero browser persistence are recorded; automated rotation reminders
remain operational work.
The current Android Phase 2 source passes SDK compile, lint, JVM, Room schema,
Debug/Test APK, unsigned release APK/AAB, SBOM, and checksum gates on this
workstation.
Runtime lifecycle, Keystore, WorkManager, MediaPlayer/audio focus, microphone,
kill, and network recovery remain unproven without an accelerated emulator or
physical device (R-009). The installed API 35 x86_64 AVD reaches Emulator
initialization, but `emulator -accel-check` reports that AEHD is not installed;
the no-acceleration fallback exits before ADB registration. Installing the
kernel driver or enabling a Windows hypervisor feature requires an explicit
elevated system change. The
Site canonical URL (R-010) remains undecided. Compose runtime behavior remains
unproven because neither the Windows workstation nor the Debian test host has
Docker, although its model and both images now have hosted CI gates. The remote
gateway reuses the existing public certificate through restricted symlinks and
its own reload timer; the test-only exposure and renewal coupling remain tracked
in R-011. Backup integrity risk (R-007) is mitigated for local storage by
manifest/tamper tests, the earlier 30-table/13-file drill, and the current
42-object/42-file schema 17 restore and upgrade drill; scheduled retention and
off-host automation are still operator responsibilities. R-016 is mitigated for
the deployed personal pull feed by Session-only authorization, recipient-bound
cursors, safe projections, transactional persistence, and live acceptance;
outbound Webhook delivery remains unimplemented pending endpoint, signature,
retry, and SSRF decisions.
Expired upload-part cleanup remains access-triggered. Expired clip/export bytes
are now removed by an integrity-checking bounded worker reaper; deterministic
remote evidence leaves the current backlog at zero, but operators must still
monitor repeated failures. The Console's Web Crypto whole-file digest may add
memory pressure near the 512 MiB upload limit and needs large-file browser
profiling.
Console title/latest-Transcript/Collection/Tag/status/date/Provider/Speaker
search, paging, Segment hits, assigned-tag controls, authenticated audio and
waveform, processing/annotation detail, note/bookmark creation, versioned
metadata, trash/restore, bounded bulk lifecycle, waveform seek, timestamps, and
playback speed are implemented and deployed. The safe permanent-deletion policy,
API, worker, and Console confirmation flow are locally and remotely verified in
the `0.12.0` candidate, but remain unverified by hosted CI; the broader upload/
storage/playback/lifecycle release item stays open until the S3 and publication
gates pass.
