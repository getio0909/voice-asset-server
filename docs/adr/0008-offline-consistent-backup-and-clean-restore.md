# ADR 0008: Offline Consistent Backup and Clean-Target Restore

- Status: Accepted
- Date: 2026-07-16

## Context

VoiceAsset stores authoritative metadata in PostgreSQL and immutable audio,
upload parts, clips, and exports in either local or S3-compatible storage. A
database-only or object-store-only copy can therefore be unusable. Coordinating
a fully online snapshot across both systems would add storage-specific
infrastructure that is not justified for the self-hosted v1 scope.

## Decision

Backups require API, worker, and MCP writes to be quiesced. `adminctl backup`
then creates a PostgreSQL custom archive and publishes a versioned manifest by
a same-filesystem rename. Local storage copies the complete tree. S3 storage
copies every database-referenced object, including upload parts, into a
portable archive. Both modes record SHA-256 and size for every file plus the
selected object inventory.

`adminctl backup-verify` rejects unknown files, links, malformed paths, checksum
mismatches, invalid PostgreSQL archives, and missing or divergent database
objects. Credentials are passed to PostgreSQL tools through the process
environment, never command arguments or backup metadata.

Restore is allowed only into a database without user objects and a new or empty
storage target. The database archive uses one `pg_restore` transaction. Local
storage is staged and published by rename; S3 storage uses create-only writes
into an empty prefix after staging each object. In-place overwrite is not
supported.

## Consequences

- Creating a backup requires a short write outage; the HTTPS gateway may stay
  online.
- Backup directories are sensitive and must be encrypted, access-controlled,
  copied off-host, and retention-managed by the operator.
- If database restore succeeds but storage publication fails, discard both
  clean targets and retry; the tool never rolls back an already committed
  database across filesystem boundaries.
- Remote recovery drills must compare table inventories and stored-file hashes,
  not merely trust a successful command exit.
