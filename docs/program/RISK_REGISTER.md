# Program Risk Register

| ID | Risk | Likelihood | Impact | Mitigation / evidence required | Status |
| --- | --- | --- | --- | --- | --- |
| R-001 | Vendor ASR APIs drift or differ by region | High | High | Implement from current official docs; retain sanitized fixtures; gate live tests by credentials | Open |
| R-002 | Android process death or network loss drops recordings | Medium | Critical | Local file as source of truth, Room operation queue, WorkManager recovery and kill/reboot tests | Open |
| R-003 | Transcript correction mutates or hallucinates source content | Medium | Critical | Immutable raw revisions, structured patches, semantic validation, human approval, audit | Open |
| R-004 | Custom provider URLs enable SSRF or secret exfiltration | Medium | Critical | URL policy, DNS/IP revalidation, egress restrictions, encrypted secrets, redacted logs | Open |
| R-005 | Five independently versioned repositories drift | High | High | Contract versions, capability negotiation, compatibility matrix, cross-repository E2E | Open |
| R-006 | Media processing permits argument injection or resource exhaustion | Medium | High | Fixed FFmpeg argument construction, input limits, sandboxing, timeouts, media security tests | Open |
| R-007 | Backup appears successful but cannot restore immutable objects | Medium | Critical | Hash manifest, clean-instance restore tests, database/object consistency verification | Open |
| R-008 | Phase labels overstate unimplemented behavior | Medium | High | Capability list and all five UIs/docs describe only tested behavior | Mitigated for Phase 0 |
| R-009 | Android build is validated only on hosted runners in this workspace | Medium | Medium | Run unit, lint, APK, and emulator jobs on GitHub; install a local SDK before Phase 2 device work | Open |
| R-010 | Site canonical deployment URL is undecided, so sitemap generation is disabled | Low | Medium | Select the static host, set Astro `site`, and verify canonical URLs before public docs deployment | Open |
