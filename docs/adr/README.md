# Architecture Decision Records

ADRs are immutable decision snapshots. Supersede an accepted record with a new
number instead of rewriting its history.

- `0001`: modular monolith and contract-first clients
- `0002`: stable identifiers and immutable source records
- `0003`: Phase 1 session, upload, and transcription transaction boundaries
- `0004`: Phase 3 provider isolation and immutable correction lineage
- `0005`: Phase 4 asset search, exact citations, and fail-closed read auditing
- `0006`: scoped API keys and isolated remote MCP authentication
- `0007`: bounded, expiring Agent artifacts through authenticated REST
- `0008`: offline consistent backup and clean-target restore
- `0009`: create-only local and S3-compatible storage driver semantics
- `0010`: storage-first permanent asset deletion with retained audits
- `0011`: bounded administration read models with fail-closed read auditing
- `0012`: workspace membership lifecycle, last-Owner safety, and revocation
- `0013`: separate deployment-global and workspace setting authority
- `0014`: transactional incremental asset change feed with durable tombstones
- `0015`: one-time, short-lived device pairing over revocable sessions
- `0016`: transactional personal terminal-job notifications for interactive sessions
- `0017`: signed, SSRF-resistant outbound terminal-job Webhooks with a durable outbox
