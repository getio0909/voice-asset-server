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

Phase 0 implements the process boundary and schema foundation. Storage,
authentication, uploads, provider adapters, and durable job claiming arrive as
tested vertical slices rather than disconnected framework stubs.
