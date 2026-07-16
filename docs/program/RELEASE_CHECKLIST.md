# v1.0 Release Checklist

Unchecked items are not complete and must not be inferred from project files.

## Governance and Contract

- [x] Server license, contribution, security, changelog, CODEOWNERS, and CI baseline
- [x] Server architecture, domain model, ADRs, OpenAPI 3.1 draft, and migration framework
- [ ] All five repositories independently build and run their CI
- [ ] Cross-repository compatibility matrix is proven by integration tests

## Product

- [ ] Complete asset upload, storage, playback, and lifecycle
- [ ] Mock ASR and Mock LLM end-to-end path
- [ ] Alibaba Cloud and Tencent Cloud fixture and live-test paths
- [ ] Immutable transcript correction, review, and approval flow
- [ ] Console lifecycle management and accessibility checks
- [ ] Android offline recording, recovery, upload, and sync
- [ ] MCP stdio and Streamable HTTP tools, resources, scopes, and audit
- [ ] Chinese and English Site with generated API reference and no broken links

## Security and Operations

- [ ] Authentication, RBAC, revocation, rate limits, SSRF and upload defenses
- [ ] Provider secret envelope encryption and redaction tests
- [ ] Backup, verification, clean-instance restore, upgrade, and rollback tests
- [ ] Dependency audit, secret scan, license check, and SBOM are green in every repository
- [ ] Linux amd64 and arm64 release artifacts and container images
- [ ] Android APK, AAB, checksums, and external signing instructions

## Acceptance

- [ ] Scenarios A-E in `GOAL.md` pass with retained evidence
- [ ] No real secrets, critical placeholders, skipped gates, or fabricated results
- [ ] Release notes and `v1.0.0` artifacts are reproducible
