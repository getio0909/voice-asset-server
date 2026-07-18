# ADR 0007: Bound Agent artifacts behind authenticated REST

- Status: Accepted
- Date: 2026-07-16

## Context

Agents need exact audio excerpts and portable transcript formats, but large
Base64 responses, storage keys, unrestricted media processing, and permanent
temporary files would expand the trust and resource boundaries.

## Decision

Create clips and transcript exports only through workspace-scoped public REST
operations. Clip creation requires `audio:read` and `metadata:write`, accepts an
integer-millisecond interval of at most five minutes, and invokes FFmpeg with a
fixed argument vector rather than a shell. Output is mono 16 kHz PCM WAV.
Transcript export requires `transcripts:read` and `metadata:write` and supports
JSON, Markdown, SRT, and WebVTT from one immutable revision.

Store both results as immutable `asset_objects`, record their parent lineage,
cap each object at 16 MiB, and expire access after one hour. Return only compact
metadata and an authenticated relative download URL. Downloads verify size and
SHA-256, support one byte range, and fail closed when their read audit cannot be
written. Creation and audit records commit in one PostgreSQL transaction; a
failed commit removes the just-written object.

## Consequences

Agents can cite or download bounded artifacts without database or object-store
access, and no API response embeds large audio. Deployments must provide
FFmpeg. The worker reaps expired clips and exports in bounded batches using
size/SHA-256 verification, retryable file-first deletion, conditional metadata
deletion, and a system audit. Expiry remains the authorization boundary; the
reaper controls disk retention.
