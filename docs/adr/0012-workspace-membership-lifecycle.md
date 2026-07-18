# ADR 0012: Workspace Membership Lifecycle and Revocation

- Status: Accepted
- Date: 2026-07-17

## Context

Workspace administrators need a public API and Console workflow for member
inventory, local-user creation, role changes, and access suspension. Direct SQL
would bypass tenant isolation, optimistic concurrency, credential revocation,
and audit requirements. The current login model resolves one globally unique
email to one workspace membership, so silently attaching an existing user to a
second workspace would make authentication ambiguous.

## Decision

Expose an additive `admin:read` member inventory and require both the Owner role
and `admin:write` for mutations. Creating a member atomically inserts a new
globally unique local user, an active membership, and a controlled audit event.
The password is PBKDF2-hashed before persistence, is write-only in OpenAPI, and
is excluded from responses and audit metadata. An existing email returns a
conflict; cross-workspace invitations are outside this slice.

Memberships have `active` or `disabled` status, a positive version, and a
strictly monotonic update timestamp. Mutations require an exact `If-Match` ETag
and serialize on the workspace row. The last active Owner cannot be demoted or
disabled. Disabling a membership atomically revokes all active workspace
sessions and API keys created by that user, and every authentication lookup
requires an active membership. Reactivation permits a new login but never
clears prior credential revocations.

## Consequences

- Admins can inspect members, while only Owners can alter workspace authority.
- Concurrent updates fail with a version conflict instead of overwriting a
  newer role or status.
- A workspace cannot lose its final active Owner through this API.
- Password reset, invitations, multi-workspace identity, and external identity
  providers require later explicit designs.
