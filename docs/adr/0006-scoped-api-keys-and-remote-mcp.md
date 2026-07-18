# ADR 0006: Scoped API Keys and Remote MCP

- Status: Accepted
- Date: 2026-07-16

## Context

A long-running MCP service cannot depend on a user's short-lived web session.
It needs a durable workspace credential with least privilege, explicit expiry,
immediate revocation, no recoverable plaintext at rest, and audit attribution.
The public MCP endpoint also needs an independent inbound credential so a
Server API key is never shared with MCP clients.

## Decision

Add workspace-scoped API-key lifecycle endpoints in contract `0.6.0`. Creation
requires `admin:write`; requested scopes must be a subset of the caller's
scopes. A key contains 256 random bits, begins with `va_pat_`, is returned once,
and is stored only as a SHA-256 digest plus a non-secret display prefix. Keys
expire after 5 minutes to 365 days. Listing requires `admin:read`; revocation
requires `admin:write`, is workspace-scoped, and is idempotent.

Authenticated keys become `agent` principals with only their stored scopes.
Successful reads include `api_key_id` in immutable audit metadata. Creation and
first revocation are transactional with their lifecycle audit records.

Run remote MCP on `127.0.0.1:18090` with a read-only Server key. Protect its
Streamable HTTP endpoint with a separate bearer token and expose only `/mcp`
through the independent `10443` Caddy gateway. Rewrite the trusted upstream
Host to the loopback address so the MCP SDK's DNS-rebinding guard stays enabled.

## Consequences

- Lost plaintext keys cannot be recovered; create a replacement and revoke the
  old key.
- Deployment secrets remain in root-managed `0640` files, never command lines.
- The test MCP key must be rotated before its recorded expiry.
- This enables the remote read slice; write tools, Resources, Prompts, clips,
  and Console key administration remain separate Phase 4 work.
