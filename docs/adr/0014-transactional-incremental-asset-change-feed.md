# ADR 0014: Transactional Incremental Asset Change Feed

- Status: Accepted
- Date: 2026-07-17

## Context

Android needs an offline-first, workspace-scoped way to discover asset changes
and permanent deletions without repeatedly downloading the full catalog. An
identifier-only feed followed by `GET /assets/{id}` cannot represent trashed or
permanently deleted resources. A message broker or full event-sourced aggregate
would add an operational system that the modular monolith does not otherwise
need.

## Decision

Migration 15 adds an append-only `sync_changes` table and an asset trigger. Each
asset insert, update, or delete appends a change in the same PostgreSQL
transaction as the mutation. A rolled-back mutation therefore leaves no event.

Upserts store a bounded, non-secret JSON snapshot of the public asset fields,
including version and trash time. Permanent deletions store only the workspace,
asset identifier, final version, sequence, and timestamp. The change row does
not reference the asset so its tombstone survives deletion.

`GET /api/v1/sync/changes` requires `assets:read`, orders by the database
sequence, caps pages at 100, and returns an opaque cursor bound to the
authenticated workspace. The cursor is always returned, even for an empty
page; clients persist it only after committing the whole page locally. Existing
assets are backfilled once during migration.

This is a synchronization projection, not the source of business truth and not
an event-sourcing contract. PostgreSQL remains the only required coordination
system. Retention or compaction may be introduced only with a documented reset
protocol that prevents silent client data loss.

## Consequences

- Offline clients can replay exact upserts and permanent-deletion tombstones
  without follow-up requests or cross-workspace leakage.
- Snapshot duplication increases database storage, but avoids missing deleted
  state and keeps polling deterministic.
- Every asset write now includes one append-only row; performance and retention
  must be monitored before broad deployment.
- Other entity types require an explicit contract and trigger rather than being
  inferred from asset events.
