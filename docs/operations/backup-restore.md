# Backup and Restore Runbook

This runbook creates a consistent PostgreSQL and object backup, verifies it
independently, and restores it into disposable clean targets. Commands use
the packaged `voiceasset-adminctl`; source builds may substitute
`go run ./cmd/adminctl`.

The workflow supports both `VOICEASSET_STORAGE_BACKEND=local` and the AWS-SDK-
compatible `s3` driver. S3 backups copy every database-referenced object,
including unfinished upload parts, into the archive, verify size and SHA-256,
and restore with create-only writes into a clean target prefix. Keep S3
credentials in the environment; never put them in command arguments or a
backup manifest.

## Prerequisites

- Install matching or newer `pg_dump` and `pg_restore` client tools.
- Confirm enough space for the database archive plus the full object tree.
- Create a private destination parent owned by the service account:

```bash
sudo install -d -o voiceasset -g voiceasset -m 0700 /srv/voiceasset-backups
```

Never put a backup inside the active storage root. Encrypt and copy completed
backups off-host; they contain private account data and credential ciphertext.
For S3, choose a new destination prefix for restore and confirm that the target
prefix is empty. The source and target prefixes may share one bucket.

## Create and Verify

Stop every process that can mutate PostgreSQL or object storage. The independent
gateway and unrelated host Caddy service do not need to stop.

```bash
sudo systemctl stop voiceasset-mcp voiceasset-worker voiceasset-api
sudo -u voiceasset bash -c '
  set -a
  . /data/apps/caddy/voice/config/server.env
  exec /data/apps/caddy/voice/bin/voiceasset-adminctl backup \
    --output /srv/voiceasset-backups/2026-07-16T2000Z \
    --confirm-offline
'
sudo -u voiceasset /data/apps/caddy/voice/bin/voiceasset-adminctl \
  backup-verify --backup /srv/voiceasset-backups/2026-07-16T2000Z
sudo systemctl start voiceasset-api voiceasset-worker voiceasset-mcp
```

If any step fails, keep writers stopped until the incomplete operation has
exited. A failed creation never publishes its destination; remove only its
tool-created hidden staging directory after checking the exact path.

For an S3 deployment, load the existing service environment and use a new
archive directory. The configured bucket and prefix are read from the
environment; `--storage` is not needed during creation:

```bash
sudo -u voiceasset bash -c '
  set -a; . /data/apps/caddy/voice/config/server.env; set +a
  export VOICEASSET_STORAGE_BACKEND=s3
  exec /data/apps/caddy/voice/bin/voiceasset-adminctl backup \
    --output /srv/voiceasset-backups/2026-07-18T1330Z-s3 \
    --confirm-offline
'
```

## Restore to Clean Targets

Verify first. Create a new database owned by the application role and choose a
new or empty storage directory. Do not point `DATABASE_URL` at production.

```bash
sudo -u postgres createdb --owner voiceasset voiceasset_restore
sudo -u voiceasset env \
  DATABASE_URL='host=/var/run/postgresql user=voiceasset dbname=voiceasset_restore' \
  /data/apps/caddy/voice/bin/voiceasset-adminctl restore \
  --backup /srv/voiceasset-backups/2026-07-16T2000Z \
  --storage /srv/voiceasset-restore/objects \
  --confirm-empty-target
```

Point a disposable API/worker configuration at both restored targets, apply no
new migrations, and verify `/readyz`, asset counts, playback ranges, and recent
transcripts. Switch production only after this isolated validation. If restore
fails after PostgreSQL commits, drop the target database, remove only the new
target storage directory, recreate both, and retry.

For S3, create the target database without migrations, set
`VOICEASSET_STORAGE_BACKEND=s3` and a clean `VOICEASSET_S3_PREFIX`, then pass a
new empty local staging path with `--storage`. The staging path is removed
after remote objects and database rows match the manifest:

```bash
sudo -u postgres createdb --owner voiceasset voiceasset_restore_s3
sudo -u voiceasset bash -c '
  set -a; . /data/apps/caddy/voice/config/server.env; set +a
  export DATABASE_URL="host=/var/run/postgresql user=voiceasset dbname=voiceasset_restore_s3"
  export VOICEASSET_STORAGE_BACKEND=s3
  export VOICEASSET_S3_PREFIX=restore-$(date -u +%Y%m%dT%H%M%SZ)
  exec /data/apps/caddy/voice/bin/voiceasset-adminctl restore \
    --backup /srv/voiceasset-backups/2026-07-18T1330Z-s3 \
    --storage /var/tmp/voiceasset-s3-restore-stage \
    --confirm-empty-target
'
```

## Recovery Drill Evidence

The 2026-07-16 test-host drill restored contract `0.7.0` into a new database and
storage root. All 30 user tables had identical row counts, all 13 stored files
matched path, size, and SHA-256, and both pre/post restore verifications passed.
The public Caddy and isolated `10443` gateway PIDs remained unchanged.

On 2026-07-18, the contract `0.18.0` preflight independently verified and
restored the 42-object live backup into a disposable database and object root.
The target migration applied schema 16, and the staged API advertised 41 sorted
features including `device_pairing` on a temporary loopback port. The database,
objects, process, and log were removed by the cleanup trap; the live services,
independent gateway, and public Caddy remained unchanged.

The later contract `0.19.0` deployment changed no schema or storage model. Its
offline preflight backup at
`/srv/voiceasset-backups/2026-07-18T0714Z-before-contract-0.19.0-r1` was
independently verified with 42 database objects, 42 storage files, and 430,371
bytes before any binary or Console replacement. Schema remained 16 after the
cutover.

Before contract `0.20.0`, the exact r2 cutover backup at
`/srv/voiceasset-backups/2026-07-18T0904Z-before-contract-0.20.0-r2` was
independently verified with 42 database objects, 42 storage files, and 430,780
bytes. The staged `0.20.0` Admin binary verified and restored it into a
disposable database/object root; migration 17 advanced that restored database
to schema 17 while all 42 files remained present. The cleanup trap then removed
only the named disposable targets. The retained backup archive SHA-256 is
`c5848c1ffd2291093c02f467b2fb1a853ec98a5f0d05825a1deab2a936613abf`.

On 2026-07-18, an isolated `rclone serve s3` endpoint passed the S3
backup/restore gate with two objects (original plus unfinished part). The
archive contained two database objects and two verified files; restore into a
new database and destination prefix reproduced one `asset_objects` row, one
`upload_parts` row, all 18 migrations, and both SHA-256 values. The temporary
database, bucket data, credentials, staging files, and loopback service were
removed after the check.
