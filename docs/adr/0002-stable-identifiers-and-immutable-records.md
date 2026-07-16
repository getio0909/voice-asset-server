# ADR 0002: Stable Identifiers and Immutable Source Records

- Status: Accepted
- Date: 2026-07-16

## Context

Audio and transcripts may be synchronized offline, audited, processed by
untrusted providers, and referenced by Agents. Sequential IDs leak volume and
are unsafe to mint offline. Rewriting source media or ASR output destroys data
lineage and makes corrections impossible to audit.

## Decision

Application code generates cryptographically unpredictable UUID-compatible IDs.
PostgreSQL sequences are not public business identifiers. UTC instants use
`timestamptz`; audio positions use integer milliseconds. Original audio,
provider raw responses, and every transcript revision are immutable. Edits,
normalization, correction, approval, and rollback create new revisions linked
to a parent.

## Consequences

Database migrations include immutability guards for the first persisted source
records. Services must validate state transitions and write audit events.
Storage garbage collection must follow lineage and retention policy instead of
blindly replacing objects.
