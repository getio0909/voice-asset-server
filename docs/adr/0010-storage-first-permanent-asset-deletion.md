# ADR 0010: Storage-First Permanent Asset Deletion

- Status: Accepted
- Date: 2026-07-17

## Context

Trash is reversible and deliberately preserves immutable audio, transcripts,
revisions, and metadata. A permanent-deletion path must therefore be explicit,
tenant-scoped, retryable across storage or database failures, and unable to
report success while bytes remain. Audit records must survive removal of the
asset graph.

## Decision

Only a workspace Owner with `assets:write` may request purge for an already
trashed asset. The request requires the exact current `If-Match` version, the
canonical asset UUID repeated in the body, and an `Idempotency-Key`. The API
moves the asset to an internal `purging` state and queues a `purge_asset` job;
normal reads, restore, and additional active jobs cannot race that state.

The Worker snapshots a bounded inventory of upload parts and immutable objects,
including backend, size, and SHA-256. It deletes parts and integrity-matching
objects before entering a database transaction. Finalization locks the job and
asset, rejects a changed inventory fingerprint, removes the dependent asset
graph, detaches and completes the purge job, deletes the asset, and appends an
`asset.purged` system audit. Existing request and lifecycle audits remain.

Storage failures retain metadata and enter normal bounded job retry. A terminal
failure may be resumed only with a new idempotency key, the reported purging
version, and the same explicit confirmation. Migration `000012` permits deletion
of immutable rows only while their owning asset is `purging`.

## Consequences

- The API acknowledges durable work, not immediate erasure; clients observe the
  purge job.
- Missing or changed bytes fail closed instead of deleting metadata.
- Storage deletion can precede a failed finalization, so every storage delete is
  idempotent and a later attempt safely completes the metadata transaction.
- Audit identifiers and non-sensitive counts/fingerprints remain for governance;
  source audio, transcript content, and asset-scoped metadata do not.
