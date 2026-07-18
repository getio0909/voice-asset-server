# ADR 0009: Create-Only Object Storage Driver Semantics

- Status: Accepted
- Date: 2026-07-17

## Context

VoiceAsset persists immutable originals and derivatives while upload parts are
retryable and temporary. Local files already use create-only publication and
full-content verification, but an S3-compatible backend must preserve those
properties across processes and network races rather than treating object keys
as ordinary overwriteable blobs.

## Decision

All storage implementations satisfy one context-aware `Driver` boundary and
identify their backend in every persisted object result. Reads return seekable
snapshots; a remote driver materializes a private temporary file and removes it
on close. Services reject database/backend mismatches before opening a key.

S3 publication uses a conditional create (`If-None-Match: *`). Losing a race is
reported as reuse only after the existing object's exact size and SHA-256 match;
different bytes are a conflict. Assembly re-reads and hashes every referenced
part. Object deletion first verifies the complete expected content and then
uses the observed ETag as an `If-Match` precondition. Part cleanup lists only the
server-derived upload prefix, rejects foreign keys or invalid pagination, and
is bounded to 10,000 keys. One object is capped at 512 MiB.

Configuration accepts AWS's default credential chain or a complete explicit
credential pair. Custom endpoints require HTTPS except for loopback development;
custom CAs apply only to HTTPS. The reviewed AWS SDK v2 adapter is now wired into
API and Worker startup; unsupported SDK errors are sanitized at the boundary.

## Consequences

- Retries cannot overwrite an immutable object, including across processes.
- Reads and conflict checks transfer the full object to preserve integrity; S3
  latency and throughput need a representative release gate.
- Temporary disk must hold the largest permitted object and be protected as
  private application data.
- Backup tooling now snapshots and restores S3 database-referenced objects with
  the same create-only and full-integrity guarantees. Compatibility,
  performance, and a clean-instance backup/restore gate pass on the isolated
  test host; production operators must still use a clean destination prefix.
