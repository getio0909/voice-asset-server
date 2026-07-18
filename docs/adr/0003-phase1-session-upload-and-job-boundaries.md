# ADR 0003: Phase 1 Session, Upload, and Job Boundaries

- Status: Accepted
- Date: 2026-07-16

## Context

The first runnable slice crosses browser authentication, large untrusted WAV
uploads, local object storage, PostgreSQL jobs, and a background ASR worker.
Partial completion, tenant confusion, leaked bearer tokens, or replacing source
bytes would make the slice unsafe even when the happy path appears to work.

## Decision

Web login returns an opaque access token only as an `HttpOnly`, `SameSite=Strict`
cookie; PostgreSQL stores its SHA-256 digest. Cookie-authenticated mutations
must match the configured `Origin`, while API clients may use the same opaque
token as Bearer authentication. Every domain lookup includes `workspace_id`.

Uploads declare total size and SHA-256, use server-sized numbered parts, and
publish through temporary files, file `fsync`, an atomic no-replace hard link,
and parent-directory `fsync` on Linux. Rooted filesystem operations reject
intermediate symbolic-link escapes. Completion verifies the assembled checksum
and exact RIFF length before recording the immutable original; playback
revalidates its SHA-256. Public responses never expose storage keys.

Transcription requests create idempotent PostgreSQL jobs. Workers claim with
`FOR UPDATE SKIP LOCKED`, bounded attempts, and expiring leases using the later
of caller and database clocks. Provider raw JSON is atomically persisted with
the normalized immutable revision, segments, successful attempt, job result,
asset state, and audit record. The Phase 1 provider is deterministic `mock_asr`
and requires no cloud credential.

## Consequences

API and worker processes must share PostgreSQL and the same durable storage
volume. A failed final attempt marks the asset failed; expired leases are safe
to reclaim. Raw files may be written before their metadata transaction, so a
retry uses stable job-derived object identity and identical Mock ASR bytes.
Future non-deterministic providers must define per-attempt raw-object lineage.
