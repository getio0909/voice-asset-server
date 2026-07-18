# ADR 0005: Phase 4 Asset Search and Read Audit

- Status: Accepted
- Date: 2026-07-16

## Context

MCP must find assets and read immutable transcript revisions without direct
database access. Agent reads also need workspace scope enforcement, stable
pagination, bounded responses, exact time citations, and durable audit records.

## Decision

Expose `GET /api/v1/assets` in additive contract `0.5.0`. Search is a
case-insensitive literal title substring. Pages are ordered by immutable
`created_at DESC, id DESC`; the Base64URL cursor contains the anchor and a hash
of the normalized query, so it cannot be reused for another search.

Keep authorization in existing Server services. Successful asset list/read and
transcript list/revision reads synchronously append to immutable `audit_logs`.
Audit failure fails the read closed. A principal with role `agent` is recorded
as `actor_type=agent`; other authenticated sessions remain `user`.

MCP receives only public REST representations. Exact transcript citations use
the half-open interval `[start_ms, end_ms)` and include asset, revision, segment,
and overlap boundaries.

## Consequences

- MCP remains independent of PostgreSQL and object-storage credentials.
- Cursor ordering is deterministic and query changes are rejected.
- Read availability now depends on audit persistence, intentionally.
- A dedicated scoped, revocable API Token is still required before exposing a
  long-running remote MCP service; a short-lived user session is not a substitute.
