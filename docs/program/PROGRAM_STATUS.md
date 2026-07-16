# VoiceAsset Program Status

- Last updated: 2026-07-16 UTC
- Current phase: Phase 0 exit validation
- Corresponding commit: Initial Phase 0 commits pending
- Estimated v1.0 completion: 5%

## Completed

- Created exactly five independent local Git repositories and matching public
  GitHub repositories under `getio0909`.
- Established AGPL licensing, README, contributing, security, CODEOWNERS,
  changelog, architecture/ADR, contract pins, and independent CI in every repo.
- Implemented Server health/capability endpoints, OpenAPI 3.1 contract `0.1.0`,
  domain schema, transactional migration runner, and PostgreSQL integration test.
- Implemented honest, buildable Console, Android, MCP, and bilingual Site
  foundations that consume or record contract `0.1.0`.
- Added fail-closed capability validation in Console and MCP, real MCP stdio/HTTP
  integration tests, Android emulator CI, dependency audits, license checks,
  secret scans, SBOMs, and pinned third-party Actions.

## In Progress

- Create the five initial Conventional Commits, push `main`, and validate every
  GitHub Actions job on the hosted runners.

## Next Work

Begin Phase 1 with the first Server vertical slice: administrator bootstrap,
authentication, asset creation, verified audio upload, Mock ASR, immutable raw
transcript creation, and API integration tests. Update OpenAPI in the same change,
then connect the Console without synthetic data.

## Blockers

No blocker for Mock-based development. Docker and Android SDK are absent on this
Windows host, so PostgreSQL container execution and full Android APK/emulator
validation must run in GitHub Actions. Windows Go race builds are blocked before
tests by the host `cgo` toolchain; Linux CI enforces race execution.

## Cross-Repository Dependencies

- Server: `https://github.com/getio0909/voice-asset-server`
- Console: `https://github.com/getio0909/voice-asset-console`
- Android: `https://github.com/getio0909/voice-asset-android`
- MCP: `https://github.com/getio0909/voice-asset-mcp`
- Site: `https://github.com/getio0909/voice-asset-site`
- Console, Android, MCP, and Site record Server contract `0.1.0`; Console and MCP
  reject incompatible API/contract versions.

## Recent Test Results

Validated on Windows amd64 on 2026-07-16 UTC:

- Server (Go 1.26.5): tests, vet, build, OpenAPI lint, module verification,
  `govulncheck`, and dependency-license policy passed. PostgreSQL integration is
  present and will execute against the CI service.
- Console (Node 24.15, pnpm 11.5): Prettier, ESLint, contract-pin check,
  typecheck, 9 Vitest tests, production build, audit, and license check passed.
- Android (JDK 21, Gradle 9.5): wrapper/help, Ktlint, dependency resolution, and
  a 107-component release-runtime CycloneDX/license check passed. Full unit,
  Android lint, APK, and Compose emulator tests await hosted CI because no local
  Android SDK is installed.
- MCP (Go 1.26.5, MCP Go SDK 1.6.1): formatting, module verification, vet,
  coverage tests, real stdio/HTTP tool calls, build, vulnerability scan, and
  license policy passed.
- Site (Astro 7.0.9, Starlight 0.41.3): clean install, type/content checks, i18n
  parity, static build, root route, internal links, and dependency audit passed.
- All five GitHub workflow files pass `actionlint`; no high-confidence secrets
  or committed `.env` files were found.

## Unresolved Risks

Highest product risks remain vendor API drift (R-001), offline recording loss
(R-002), correction integrity (R-003), SSRF (R-004), and cross-repository drift
(R-005). CI-only Android validation (R-009) and the undecided Site canonical URL
(R-010) are explicitly tracked in `RISK_REGISTER.md`.
