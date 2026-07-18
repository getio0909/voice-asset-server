# Performance Validation

## Remote Control-Plane Gate

The release-candidate smoke test measures the isolated deployment's public,
read-only control plane. It sends 16 warm-up requests followed by 400 measured
requests at concurrency 8, alternating between `/readyz` and
`/api/v1/system/capabilities` through the independent `10443` gateway.

The gate requires strict certificate-chain and hostname validation, exact API/contract
capabilities, zero request failures, p95 latency at or below 500 ms, and at least
20 requests/second. It disables redirects and ambient proxy use, sends no bearer
or account credentials, and does not create database or audit rows. These values
are a bounded test-host acceptance budget, not a production SLO.

The gateway reuses the host's publicly trusted `api.getio.net` certificate, so
the default operating-system trust store is sufficient:

```bash
make perf-remote
```

PowerShell without GNU Make:

```powershell
$env:VOICEASSET_REMOTE_PERF = '1'
$env:VOICEASSET_REMOTE_BASE_URL = 'https://api.getio.net:10443'
$env:VOICEASSET_REMOTE_CONTRACT_VERSION = '0.13.0'
go test ./tests/performance -run '^TestRemoteReadControlPlane$' -count=1 -v
```

Clear all three environment variables after the run. Never copy the public Caddy
private key or disable certificate verification. Run this smoke only against an
authorized test deployment. The database asset path is covered separately below;
the database, local upload/storage, worker, audio, and FFmpeg clip paths are
  covered separately below. The production S3-compatible SDK adapter now has an
  HTTP-compatible lifecycle test; a separate opt-in remote S3 lifecycle/performance
  probe is documented below. The clean-instance S3 backup/restore gate is now
  recorded in the backup runbook.

## Remote S3-Compatible Gate

The opt-in S3 gate uses a disposable bucket and credentials supplied only through
the environment. It exercises four concurrent 256 KiB part uploads, exact
assembly and SHA-256 verification, a full object snapshot read, immutable object
publication, and integrity-checked deletion. It uses a loopback HTTP endpoint for
an isolated development-compatible service; production endpoints must use HTTPS.

PowerShell example (replace values with a disposable test bucket and credentials;
never commit them):

```powershell
$env:VOICEASSET_S3_PERF = '1'
$env:VOICEASSET_S3_ENDPOINT = 'http://127.0.0.1:19000'
$env:VOICEASSET_S3_REGION = 'us-east-1'
$env:VOICEASSET_S3_BUCKET = 'voiceasset-perf'
$env:VOICEASSET_S3_PREFIX = 'run-<unique>'
$env:VOICEASSET_S3_ACCESS_KEY_ID = '<disposable-access-key>'
$env:VOICEASSET_S3_SECRET_ACCESS_KEY = '<disposable-secret-key>'
go test ./tests/performance -run '^TestRemoteS3Lifecycle$' -count=1 -v
Remove-Item Env:VOICEASSET_S3_PERF,Env:VOICEASSET_S3_ENDPOINT,Env:VOICEASSET_S3_REGION,Env:VOICEASSET_S3_BUCKET,Env:VOICEASSET_S3_PREFIX,Env:VOICEASSET_S3_ACCESS_KEY_ID,Env:VOICEASSET_S3_SECRET_ACCESS_KEY
```

The test fails when enabled and configuration or any lifecycle operation is
invalid; it never silently skips an enabled gate. It does not claim backup or
restore correctness; that is the separate clean-instance gate documented in
`docs/operations/backup-restore.md`.

The 2026-07-18 clean-instance S3 gate used the same isolated loopback service
and disposable credentials. It backed up an S3 original plus an unfinished
upload part, independently verified the archive, restored into a new database
and prefix, compared both object hashes and database inventories, and removed
all temporary state. The run produced two storage files and two database
objects; no production bucket or service was touched.

## Database Asset Gate

The opt-in database gate applies all migrations in a unique schema, creates a
test Owner, and bulk-seeds 5,000 assets as setup data. It then measures two
production-service paths at concurrency 8:

- 100 asset creations, each including the normal audit transaction;
- 400 alternating asset-list and title-search reads returning 25 rows.

The create gate requires p95 at or below 1.5 seconds and at least 5 operations
per second. The read gate requires p95 at or below 750 ms and at least 20
operations per second. Both require every scheduled operation to succeed. These
are portable release-candidate floors, not production SLOs.

Provide a test-only PostgreSQL URL through the environment; never place it on a
command line or in source control:

```bash
make perf-data
```

PowerShell without GNU Make:

```powershell
$env:VOICEASSET_DATA_PERF = '1'
go test ./tests/performance -run '^Test(DatabaseAsset|UploadWorkerAudio)Performance$' -count=1 -v
Remove-Item Env:VOICEASSET_DATA_PERF
Remove-Item Env:TEST_DATABASE_URL
```

The test drops its schema during cleanup, including on failed assertions. Use a
dedicated non-production database role that can create and drop schemas.

## Upload, Worker, and Audio Gate

The second `perf-data` test creates eight assets and, at concurrency 4, uploads
one 5,243,904-byte WAV per asset as two independently hashed parts. The measured
operation includes the upload-session transactions, part persistence, exact
assembly, whole-file hash verification, immutable publication, WAV probing,
asset metadata update, audits, and part cleanup.

It then measures eight concurrent Mock ASR Worker claims and immutable transcript
publications, followed by 32 audio opens. Every audio open loads the private
object mapping, verifies file size, hashes the complete 5 MiB object, resets the
file, and closes it. The portable release-candidate floors are:

| Operation                      | p95 budget | Minimum throughput  |
| ------------------------------ | ---------- | ------------------- |
| Multipart upload/local storage | 4 seconds  | 1 operation/second  |
| Mock Worker/transcript         | 2 seconds  | 2 operations/second |
| Audio open/full hash           | 1 second   | 5 operations/second |

All eight assets, uploads, jobs, original objects, raw responses, revisions, and
audits live only in the disposable schema and temporary storage directory.

## FFmpeg Media Gates

The opt-in media gate calls the production `FFmpegClipper` against a 5,243,904-
byte PCM WAV source. It generates twelve 30-second clips at concurrency 3. Each
operation includes process startup, deterministic mono 16 kHz PCM conversion,
WAV probing, complete output reading, metadata/size validation, close, and
temporary-file removal. Every operation must succeed; p95 must not exceed 3
seconds and throughput must remain at least 1 operation/second.
The same invocation renders twelve production 1600x256 PNG waveforms at
concurrency 3, validates each complete PNG and its dimensions/size ceiling, and
requires the same p95 and throughput floors with an empty temporary directory.

```bash
make perf-media
```

Override `FFMPEG_PATH` when `ffmpeg` is not on `PATH`. PowerShell without GNU
Make:

```powershell
$env:VOICEASSET_MEDIA_PERF = '1'
$env:VOICEASSET_FFMPEG_PATH = 'C:\tools\ffmpeg\bin\ffmpeg.exe'
go test ./tests/performance -run '^TestFFmpeg(Clip|Waveform)Performance$' -count=1 -v
Remove-Item Env:VOICEASSET_MEDIA_PERF
Remove-Item Env:VOICEASSET_FFMPEG_PATH
```

This is a portable release-candidate floor, not a production SLO. Run it on the
same architecture and FFmpeg build intended for release. The opt-in test fails
when the configured executable is unavailable; it never silently skips an
enabled gate.

## Verified Baselines

### Remote control plane

On 2026-07-17, the deployed Server `0.1.0-dev+phase6.20260717.6`/contract
`0.13.0` passed from the Windows development host through the independent
gateway after it switched to the system-trusted public certificate:

| Requests | Concurrency | Failures | Throughput | p50        | p95        | p99        | Maximum    |
| -------- | ----------- | -------- | ---------- | ---------- | ---------- | ---------- | ---------- |
| 400      | 8           | 0        | 42.0 req/s | 185.815 ms | 227.286 ms | 252.098 ms | 269.603 ms |

The API, worker, MCP, Prometheus, independent gateway, and public Caddy retained
their post-switch PIDs and zero-restart counts; error-priority journals remained
empty. This baseline is environment-specific and must be rerun for each release
candidate.

The post-cutover `.20260718.9`/contract `0.22.0` read-only rerun also passed
through the same gateway: 400 requests at concurrency 8, zero failures,
43.4 requests/second, p50 177.268 ms, p95 220.237 ms, p99 229.824 ms, and a
257.809 ms maximum. The gateway and public Caddy remained on their existing
processes and certificate.

### Database asset path

On 2026-07-17, the Windows development host and its test PostgreSQL endpoint
passed against a disposable, fully migrated schema:

| Operation          | Requests | Concurrency | Failures | Throughput  | p50       | p95       | p99       | Maximum   |
| ------------------ | -------- | ----------- | -------- | ----------- | --------- | --------- | --------- | --------- |
| Asset create/audit | 100      | 8           | 0        | 900.6 ops/s | 5.455 ms  | 41.758 ms | 58.466 ms | 62.412 ms |
| Asset list/search  | 400      | 8           | 0        | 194.2 ops/s | 37.903 ms | 54.092 ms | 56.333 ms | 58.390 ms |

An external post-run query found zero `asset_perf_%` schemas. Upload assembly,
object storage, worker processing, audio delivery, and media probing are covered
by the next baseline.

### Local upload, Worker, and audio path

On 2026-07-17, the Windows development host and test PostgreSQL endpoint passed
the supported local-storage pipeline:

| Operation                      | Requests | Concurrency | Failures | Throughput  | p50        | p95        | p99        | Maximum    |
| ------------------------------ | -------- | ----------- | -------- | ----------- | ---------- | ---------- | ---------- | ---------- |
| Multipart upload/local storage | 8        | 4           | 0        | 9.6 ops/s   | 389.987 ms | 499.027 ms | 499.027 ms | 499.027 ms |
| Mock Worker/transcript         | 8        | 4           | 0        | 53.6 ops/s  | 57.041 ms  | 107.040 ms | 107.040 ms | 107.040 ms |
| Audio open/full hash           | 32       | 4           | 0        | 186.9 ops/s | 18.848 ms  | 32.332 ms  | 33.232 ms  | 33.232 ms  |

The test removed its schema and temporary object directory. The SDK-backed
S3-compatible adapter has its own local HTTP lifecycle coverage; the isolated
remote compatibility/performance baseline is recorded next, while backup/restore
evidence remains required before that path is promoted as release-grade.

### Remote S3-compatible lifecycle

On 2026-07-18, the SDK adapter passed the opt-in lifecycle probe against a
disposable `rclone serve s3` instance on the isolated test host, reached only
through an SSH tunnel to loopback `127.0.0.1:19000`. Four 256 KiB parts were
assembled into a 1 MiB object, reopened and byte-verified, then an immutable
object was published and both objects were deleted with integrity checks. The
measured run completed in 5.382 seconds at 0.2 MiB/s; this is a compatibility
baseline for the isolated service, not a production SLO. The temporary service,
credentials, bucket data, and tunnel were removed after the run. Backup/restore
evidence remains open.

### FFmpeg clip path

On 2026-07-17, the Windows development host passed two consecutive real FFmpeg
runs. The latest retained baseline was:

| Clips | Clip duration | Source bytes | Concurrency | Failures | Throughput | p50        | p95        | p99        | Maximum    |
| ----- | ------------- | ------------ | ----------- | -------- | ---------- | ---------- | ---------- | ---------- | ---------- |
| 12    | 30 seconds    | 5,243,904    | 3           | 0        | 12.9 ops/s | 155.248 ms | 454.337 ms | 454.337 ms | 454.337 ms |

All outputs were mono 16 kHz PCM WAV files within the 16 MiB ceiling, were read
completely, and removed on close.

### FFmpeg waveform path

The same two runs generated production waveform PNGs from the 5,243,904-byte
source. The latest retained baseline was:

| Waveforms | Dimensions | Source bytes | Concurrency | Failures | Throughput | p50       | p95       | p99       | Maximum   |
| --------- | ---------- | ------------ | ----------- | -------- | ---------- | --------- | --------- | --------- | --------- |
| 12        | 1600x256   | 5,243,904    | 3           | 0        | 11.5 ops/s | 262.880 ms | 317.440 ms | 317.440 ms | 317.440 ms |

Every PNG was fully read, validated against the 4 MiB ceiling, and removed on
close; the shared temporary directory was empty after each run. S3-compatible
storage remains outside these local-media results.
