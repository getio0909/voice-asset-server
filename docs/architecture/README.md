# Server Architecture

VoiceAsset Server is a modular monolith. A single Go module owns the domain and
public contract while independently deployable API and worker processes share
the same packages and migrations.

```text
Console / Android / MCP
          |
   OpenAPI REST / WebSocket
          |
   API process ---- Worker process
          |              |
          +-- PostgreSQL-+
          |
   Local or S3-compatible object storage
```

The API process handles bounded request work. Durable or expensive media, ASR,
LLM, indexing, and notification work belongs to PostgreSQL-backed jobs consumed
by the worker. PostgreSQL stores metadata and lineage, never large audio blobs.
Clients and MCP use only the public API; database access is never a client
integration surface.

## Module Boundaries

`cmd/` contains composition roots only. Domain behavior belongs under
`internal/<domain>/`; cross-cutting configuration, HTTP, migrations, versioning,
and telemetry belong under `internal/platform/`. SQL is explicit and versioned
under `migrations/`. `contracts/openapi.yaml` is changed with the server
implementation, never after clients have guessed a shape.

## Invariants

- Original audio and raw provider responses are immutable.
- Every transcript edit creates an immutable revision.
- Business IDs are application-generated, stable, and not sequential.
- Timestamps represent UTC instants; audio positions use integer milliseconds.
- Core workflows use explicit states and auditable transitions.
- Cloud credentials remain server-side and are never logged or returned.

Phase 1 implements local authentication, verified WAV storage, a deterministic
Mock ASR adapter, durable job claiming, immutable raw transcripts, and
authenticated Range playback as one tested vertical slice. Later provider and
editing capabilities extend these boundaries rather than bypassing them.

Phase 3 adds encrypted workspace-scoped ASR and LLM profiles, Alibaba and
Tencent batch adapters, explicit capability and failover policies, independent
hotword/glossary version streams, and durable LLM correction jobs. Transcript
lineage is append-only:

```text
raw_asr -> normalized -> llm_corrected -> human_edited -> approved
```

Provider responses remain immutable objects. Derived revisions record the
profile, vocabulary, prompt, validation, and diff snapshots used to create
them. See ADR 0004 for the trust and transaction boundaries.

Phase 4 adds short-lived Agent artifacts without weakening those boundaries.
Audio clips are bounded derivatives of a verified original; transcript exports
serialize one immutable revision. Both are immutable objects, expire after one
hour, remain workspace scoped, and are served only through authenticated,
audited REST endpoints. MCP invokes these endpoints and never receives storage
keys or direct database access. See ADR 0007.

Phase 6 administration exposes only bounded public models. Member reads require
`admin:read`; writes additionally require the Owner role and `admin:write`.
Exact-version updates retain an active Owner, and disabling access atomically
revokes that member's sessions and API keys. See ADR 0012.

System settings do not inherit workspace authority by convenience. Deployment-
global configuration remains operator-owned; future workspace settings require
workspace-keyed persistence, exact versions, typed consumers, and transactional
audits. See ADR 0013.

Offline clients consume an append-only asset synchronization projection rather
than querying the database or requiring a message broker. Asset upserts capture
a non-secret snapshot and permanent deletion captures a durable tombstone in
the same transaction as the source mutation. Workspace-bound opaque cursors
advance only after a client commits a complete page. See ADR 0014.
