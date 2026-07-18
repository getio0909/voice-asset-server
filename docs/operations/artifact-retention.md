# Expired Agent Artifact Retention

Audio clips and transcript exports are reproducible, one-hour artifacts. The
worker reaps them automatically; original audio, Provider raw responses,
transcript revisions, and audit records are never in scope.

## Runtime behavior

Each worker scheduler cycle gives the reaper a fair turn after transcription
and correction processors. One reaper turn selects at most 25 expired records,
ordered by expiry, then handles every candidate independently:

1. Validate the UUIDs, local storage backend, expiry, size, and SHA-256.
2. Delete the file only if its current bytes still match the database metadata.
3. Conditionally delete the matching `asset_objects` row and cascading clip or
   export row.
4. Append a system audit event with action `artifact.reaped`.

File deletion happens before the database transaction. If PostgreSQL is
temporarily unavailable, the record remains eligible and the next cycle treats
the already-absent file as a safe retry. A checksum, size, kind, backend, or
storage-key mismatch fails closed and preserves database metadata for
investigation.

## Monitoring

Check backlog size and age without reading artifact contents:

```sql
SELECT 'audio_clip' AS kind, count(*), min(expires_at) AS oldest_expiry
FROM audio_clips WHERE expires_at <= clock_timestamp()
UNION ALL
SELECT 'transcript_export', count(*), min(expires_at)
FROM transcript_exports WHERE expires_at <= clock_timestamp();
```

Confirm completed cleanup through immutable audits:

```sql
SELECT target_type, count(*), max(occurred_at) AS last_reaped_at
FROM audit_logs
WHERE actor_type = 'system' AND action = 'artifact.reaped'
GROUP BY target_type;
```

Alert when the oldest expiry remains behind current time for several worker
heartbeats or worker logs repeatedly report `storage deletion` or `metadata
deletion`. Investigate storage integrity and PostgreSQL health; do not manually
remove rows or files independently. Stop the worker before offline backup so a
reaper cycle cannot change the database/object inventory during capture.
