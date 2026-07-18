# ADR 0017: Signed Outbound Job Webhooks

- Status: Accepted
- Date: 2026-07-18

## Context

Interactive clients can replay personal terminal-job events through the
Session-only notification feed, but external automation needs a durable push
boundary. A generic callback implementation would create material SSRF,
credential disclosure, replay, retry-storm, tenant-isolation, and endpoint
retargeting risks.

## Decision

Add workspace-scoped outbound Webhook endpoints and a PostgreSQL delivery
outbox with the following constraints:

- Only an Owner authenticated by a browser Session may create, update, rotate,
  test, or inspect Webhooks. API keys cannot administer this boundary.
- Endpoints subscribe only to `job.succeeded`, `job.failed`, and
  `job.cancelled`. Payloads reuse the bounded immutable notification projection;
  they never include job payloads, provider responses, credentials, lease data,
  idempotency material, user email, or arbitrary metadata.
- Endpoint URLs must be absolute HTTPS URLs without user information, query, or
  fragment. The client rejects local and special-use hosts, validates every DNS
  answer, dials only a validated address, disables ambient proxies and
  redirects, and requires TLS 1.2 or newer.
- The Server generates a 256-bit signing secret, returns it only in create or
  rotate responses, and stores only an encrypted envelope bound to workspace,
  Webhook ID, and secret version. Lists and ordinary updates expose only
  `secret_configured: true`.
- Each notification insert transactionally creates one delivery for every
  matching enabled Webhook. Migration does not backfill historical events.
  Deliveries bind to the endpoint version that existed when they were created;
  disabling, changing, or rotating an endpoint cancels its unfinished older
  deliveries instead of retargeting them.
- The Worker claims deliveries with `FOR UPDATE SKIP LOCKED` and an expiring
  lease. A delivery receives at most five attempts with bounded exponential
  delay. Transport failures, timeouts, HTTP 408/425/429, and 5xx responses retry;
  other non-2xx responses fail permanently. Response bodies are bounded and
  never persisted.
- Requests carry `X-VoiceAsset-Delivery`, `X-VoiceAsset-Event`, a Unix
  `X-VoiceAsset-Timestamp`, and
  `X-VoiceAsset-Signature: v1=<hex HMAC-SHA256>` over
  `<timestamp>.<exact body>`. A stable event ID supports receiver deduplication.
- Creation, update, rotation, test enqueue, and delivery outcomes are audited
  with credential-free metadata. Operators can inspect bounded delivery state,
  HTTP status, attempt count, and safe error class, but never response content.

## Consequences

Webhook delivery remains asynchronous and cannot delay the job transaction or
modify its result. Receivers must verify the signature, reject stale timestamps,
deduplicate event IDs, and return a 2xx response only after accepting the event.

This decision does not add arbitrary custom headers, inbound Webhooks, alert
receiver configuration, WebSocket push, or delivery of full asset/transcript
content. Those require separate contracts and threat analysis.
