# ADR 0004: Phase 3 Provider and Correction Lineage

- Status: Accepted
- Date: 2026-07-16

## Context

Cloud ASR and user-configured LLM endpoints introduce vendor drift, server-side
secrets, retries, failover, untrusted model output, and correction history that
must remain independently auditable. ASR hotwords affect recognition before a
transcript exists, while LLM glossaries constrain later corrections; combining
them would make both behavior and lineage ambiguous.

## Decision

ASR and LLM adapters implement provider-neutral interfaces and expose explicit
capabilities. Workspace profiles store public configuration separately from an
AES-GCM credential envelope. Provider URLs must use HTTPS and pass hostname and
private-address policy checks. Public APIs, errors, snapshots, and audit logs
never contain credential values. Live vendor tests are opt-in; sanitized vendor
fixtures remain the offline release gate.

Hotword sets and glossary sets are separate, immutable version streams. Each
job stores the exact profile, hotword, and glossary snapshots it used. ASR
publication creates `raw_asr` followed by an identity `normalized` child; the
provider JSON remains an immutable object linked to the raw revision.

LLM correction is a durable job. The model receives transcript text as
untrusted user content and must return a structured patch. The server validates
segment identity, original text, numeric and semantic invariants, change ratio,
and timeline preservation before creating an `llm_corrected` child. Review
decisions are append-only. Approval creates `human_edited` and `approved`
children rather than updating any existing revision.

The default profile policy is `never`. `validated_glossary_only` may approve
only a non-empty patch produced by a non-empty effective glossary when every
normal correction validation passes. The commit remains one transaction: it
persists `llm_corrected`, an automated review, system audit, and immutable
`human_edited` then `approved` children. It cannot skip lineage or make a second
manual approval valid.

## Consequences

Retries and worker restarts do not erase provenance, but every derived revision
and snapshot consumes additional storage. Provider profile changes affect only
future jobs. Automated approval is deliberately narrower than general model
acceptance and uses the same validation and immutable revision path; it does not
bypass review records or mutate a proposal.
