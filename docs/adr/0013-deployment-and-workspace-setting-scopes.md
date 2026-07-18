# ADR 0013: Deployment and Workspace Setting Scopes

- Status: Accepted
- Date: 2026-07-17

## Context

The initial schema contains a deployment-global `system_settings` key/value
table, while every authenticated role and `admin:*` scope is granted through a
workspace membership. Exposing that table directly would let an Owner of one
workspace alter settings that affect every other workspace. It would also blur
the boundary between runtime configuration, workspace preferences, and secrets.

The product still needs a System Settings experience, but a convenient UI is
not sufficient reason to weaken tenant or operator authority.

## Decision

Settings are classified before they receive an API:

- Deployment settings remain operator-owned process or host configuration.
  Workspace principals cannot mutate them. A future write API requires a
  separate deployment-administrator authority, explicit restart semantics, and
  a migration plan for existing global rows.
- Workspace settings require dedicated workspace-keyed persistence, a foreign
  key to the workspace, positive versions, and monotonic update times. Reads
  require `admin:read`; writes require both Owner and `admin:write`, an exact
  `If-Match`, validation against typed allowlisted keys, and an audit in the same
  transaction as the update.
- A setting is added only with an implemented runtime consumer. Provider,
  storage, signing, and authentication secrets never use either setting model.
- The existing `system_settings` table is not exposed through workspace RBAC.
  An `admin:read` endpoint may return only a hard-coded, credential-free
  projection of active runtime facts. It is audited, has no mutation method,
  and never reads or serializes that table. The Console may render this
  projection as operator-managed and read-only; it does not imply settings
  authority.

## Consequences

- A workspace Owner cannot change deployment-wide behavior or another tenant's
  preferences.
- System Settings remains intentionally incomplete until each proposed key has
  a scope, consumer, validation rule, concurrency behavior, and safe audit form.
- Deployment administration may require a separate trust model instead of
  extending the existing workspace roles.
