# ADR 0016: Transactional Personal Job Notifications

- Status: Accepted
- Date: 2026-07-18

## Context

Interactive users need a durable way to discover completed background work.
Polling every job wastes requests, while outbound webhooks introduce endpoint
ownership, SSRF, signing-secret, retry, and delivery-governance decisions that
are not yet settled. A WebSocket transport would still need a replayable source
of truth after disconnects.

## Decision

Add an append-only `notifications` projection and Session-only
`GET /api/v1/events` endpoint.

- A PostgreSQL trigger inserts one personal event in the same transaction as
  each job transition to `succeeded`, `failed`, or `cancelled`. Migration 17
  backfills existing terminal jobs in deterministic order. A retried job may
  therefore produce distinct failed and succeeded events; rolled-back job
  transitions produce none.
- The recipient is the job creator in the same workspace. Events expose only
  sequence, identifiers, job kind/state, optional asset/revision references,
  a bounded safe error code, and occurrence time. Job payloads, credentials,
  idempotency material, lease data, and arbitrary provider responses are never
  copied or returned.
- Rows are immutable. Pagination uses a stable high-watermark and an opaque
  cursor bound to both authenticated workspace and user. API keys are rejected,
  even when they carry transcript scopes. Successful reads create a safe audit
  containing only result count and `has_more`.

## Consequences

The database trigger covers every current and future job producer without
duplicating notification writes across services, at the cost of explicit schema
coupling to terminal job transitions. Integration tests therefore cover
backfill, isolation, retries, rollback, ordering, and immutability.

The endpoint is a replayable pull feed, not a claim that Console, Android, or
MCP consumes notifications. Outbound webhooks and live push transports remain
separate decisions and must define endpoint validation, signatures, retries,
revocation, observability, and SSRF controls before implementation.
