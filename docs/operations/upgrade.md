# Upgrade and Rollback

VoiceAsset migrations are ordered, transactional, checksum-pinned, and applied
forward by the one-shot migration command. Treat database and immutable object
storage as one release unit. Never run an upgrade against only one of them.

## Preflight

1. Read the release notes and compatibility matrix for the target Server,
   Console, Android, MCP, Site, and contract versions.
2. Create an offline backup and run `backup-verify` against it.
3. Record current binary, migration, Console, and object-manifest checksums.
4. Confirm the target binaries can read the current database backup in an
   isolated restore drill.
5. Keep the previous binaries and verified backup until acceptance completes.

Use a non-production PostgreSQL database to run the deterministic upgrade gate:

```bash
make test-upgrade
```

`TEST_DATABASE_URL` must come from the environment or a secret manager, never a
command-line argument. The test creates and drops isolated schemas. It upgrades
each prior version to the current version, then separately proves representative
legacy data survives a staged upgrade.

## Apply

1. Stop MCP, Worker, and API writes; keep the reverse proxy configuration
   untouched.
2. Run the target migration binary once and require a zero exit status.
3. Start the target API and Worker, then MCP and Console.
4. Verify readiness, advertised Server/contract versions, session rotation,
   asset search, audio range reads, one Mock transcription, and error journals.
5. Retain the maintenance window until row counts and immutable object hashes
   match the preflight inventory.

## Rollback

If failure occurs before a migration commits, restart the previous binaries. If
the schema has advanced, use previous binaries only when the release notes
explicitly confirm backward compatibility. Otherwise stop all writers, restore
the verified pre-upgrade database and object backup into clean targets, point the
previous binaries at those targets, and rerun acceptance checks.

Down migration files exist for development verification; they may drop tables,
columns, or data and are not a production rollback mechanism. Do not modify or
reload an unrelated reverse proxy while rolling back the isolated `10443` test
deployment.

## Verified Candidate

On 2026-07-17, contract `0.11.0` migrations 1–11 passed upgrades from every prior
version. A separate staged v1 → v2 → v11 run preserved representative legacy
records, backfilled the existing access session as `Legacy session` with no
refresh credential, assigned a recoverable prior status to legacy trashed
assets, and queued exactly one deterministic waveform job for each original.
The migration also verified both generated search vectors, all search indexes,
waveform uniqueness, and derivative immutability. Every disposable schema was
removed after the tests.

The contract `0.12.0` candidate subsequently passed fresh-schema application,
idempotent reapplication, down-migration verification, and upgrades from every
prior version through migration 12 on the authorized PostgreSQL test host. The
waveform immutability regression check and storage-first purge success,
integrity-failure, terminal-resume, graph-removal, and retained-audit scenarios
all passed in disposable schemas. Before deployment, the live 0.11 database and
42 local objects were captured in an independently verified offline backup.
The `.3`/`0.12.0` cutover then applied migration 12 and passed strict-TLS
readiness, a real browser upload/Mock-ASR/trash/exact-ID permanent-purge flow,
zero-row graph and absent-file verification, retained audits, and an official
SDK MCP read smoke. Public Caddy PID `18314` and isolated gateway PID `96419`
were unchanged, all VoiceAsset restart counts remain zero, and the post-cutover
error journal is empty.

On 2026-07-18, the coordinated `0.17.0` candidate passed fresh application and
upgrades from every schema version 1–14 through migration 15. Before the live
cutover, database rows and all 42 object files were captured and independently
verified under
`/srv/voiceasset-backups/2026-07-18T0316Z-before-contract-0.17.0-r1`.
The isolated deployment then applied schema 15, started the matching API,
Worker, MCP, and Console, and passed readiness, 40-feature capability, strict-
TLS, and real failed-job retry/duplicate/cleanup checks. All five services have
zero restarts and empty error-priority journals. The gateway continued reading
the existing certificate through restricted symlinks; public Caddy's process
and configuration SHA-256 remained unchanged.

The subsequent `0.18.0` cutover verified
`/srv/voiceasset-backups/2026-07-18T0455Z-before-contract-0.18.0-r1`, restored
it into an isolated database/object root, applied migration 16, and started the
target API on a temporary loopback port before touching the live files. The
live deployment then advertised 41 sorted features and passed one-time pairing.
A live-discovered subsecond expiry serialization mismatch was fixed with a RED/
GREEN regression and full ordinary/race reruns; the `.20260718.3` patch used a
second verified backup and retained both prior application snapshots. Public
Caddy and the independent certificate-reusing gateway retained their exact
PIDs, restart counts, configuration hashes, and certificate symlink targets.

Contract `0.19.0` required no migration. Before replacing files, the operator
created and independently verified
`/srv/voiceasset-backups/2026-07-18T0714Z-before-contract-0.19.0-r1` with 42
database objects and 42 storage files. The staged Linux binaries reported the
embedded `.20260718.4` version, dirty baseline revision, contract, and all 42
sorted capabilities before cutover. Post-deploy strict-TLS checks proved the
System Settings allowlist, authentication, query and mutation rejection,
auditing, and logout. Schema stayed at 16; both Caddy processes and config
hashes, the reused certificate, Prometheus, and zero-restart baseline remained
unchanged.

Contract `0.20.0` added migration 17. The operator created and independently
verified `/srv/voiceasset-backups/2026-07-18T0904Z-before-contract-0.20.0-r2`,
then restored its 42 database objects and 42 storage files into disposable
targets and applied the staged migration before touching the live schema. The
cutover advanced the isolated database to schema 17 and deployed matching
`.20260718.5` API, Worker, MCP, and Console artifacts. Strict public-TLS
personal-event acceptance passed Session/API-key boundaries, safe fields,
ordered empty checkpoints, authenticated workspace/user cursor binding,
tamper and method rejection, safe audits, logout, and post-logout denial.
Public Caddy and the independent gateway retained their exact PIDs,
configuration hashes, shared certificate, and zero restart counts.
