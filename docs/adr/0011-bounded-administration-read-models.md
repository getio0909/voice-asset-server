# ADR 0011: Bounded Administration Read Models

- Status: Accepted
- Date: 2026-07-17

## Context

Operators need a Job Center, audit inventory, and system-status view without
direct database access. Reusing worker payloads or broad internal models would
expose idempotency data, lease identities, provider details, or unbounded tenant
history. Administration reads must preserve workspace isolation and remain
auditable themselves.

## Decision

Expose three additive `admin:read` REST resources: paginated job summaries,
paginated audit entries, and a generated workspace status snapshot. Job results
omit payloads, idempotency keys, and lease owners. Audit results contain only
the persisted controlled metadata object. Status reports bounded aggregate
counts and logical storage bytes; it does not probe credentials or return
provider configuration.

List endpoints accept exact allow-listed filters, a limit of 1–100, and opaque
cursors bound to the workspace and complete filter set. Ordering uses a stable
timestamp-plus-UUID tuple. Unknown or duplicate query parameters fail closed.
Every successful read appends its own audit event before the response is sent;
an audit-write failure therefore fails the read instead of creating invisible
administrative access. The audit-list event is naturally visible only to a
subsequent request because the query precedes its read audit.

## Consequences

- Console pages can consume safe public API models instead of privileged SQL.
- Cursors cannot be replayed across workspaces or different filters.
- Aggregate status is an inventory snapshot, not an infrastructure health or
  billing measurement.
- Administration reads increase audit volume predictably and may return an
  error when governance persistence is unavailable.
