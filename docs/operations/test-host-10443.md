# Independent 10443 Test Deployment

This deployment runs the current API candidate, worker, Console, MCP Agent
service, and a separate Caddy process under `/data/apps/caddy/voice`. It does
not read, reload, or modify the host's existing Caddy configuration or its
ports 80 and 443.

## Topology

- `voiceasset-gateway.service` terminates TLS on `10443` and serves Console.
- Requests for `/api/*`, `/healthz`, `/readyz`, and `/version` proxy to the API
  on loopback port `18080`.
- Prometheus scrapes `http://127.0.0.1:18080/metrics` directly. The gateway does
  not expose `/metrics`; labels are bounded and exclude raw paths, query strings,
  and resource identifiers.
- `voiceasset-prometheus.service` listens only on `127.0.0.1:19090`, retains
  samples for 7 days or 1 GiB, and evaluates checked-in availability, 5xx-rate,
  p95-latency, and configuration-reload rules. Alertmanager listens only on
  `127.0.0.1:19093` and delivers to the bounded receiver on `19193`; the
  receiver writes only allowlisted alert fields to a mode-0600 JSONL journal.
- `voiceasset-otelcol.service` runs Collector 0.155.0 on loopback OTLP/HTTP
  `127.0.0.1:14318`, with health on `127.0.0.1:13133`, and writes traces under
  the protected VoiceAsset data directory. API and Worker export only when the
  loopback endpoint is configured; raw URLs, queries, credentials, and alert
  annotations are not persisted by this deployment.
- `/mcp` proxies to the bearer-protected Streamable HTTP service on loopback
  port `18090`. The proxy rewrites only the upstream Host to that loopback
  address so the MCP SDK's DNS-rebinding guard remains enabled. MCP calls the
  Server with a separate scoped API key.
- `voiceasset-api.service` and `voiceasset-worker.service` share one local
  object directory and a dedicated PostgreSQL database. The worker fairly
  interleaves jobs with bounded, integrity-checking expiry reaping for temporary
  clips and exports.
- `voiceasset-migrate.service` gates API and worker startup.
- FFmpeg is installed as a runtime dependency for bounded audio clips and
  immutable waveforms; neither API nor Worker invokes it through a shell.

The current verified candidate is Server API/Worker
`v0.1.0-dev+workspace.20260718.11` and MCP `workspace-20260718.11`, contract
`0.22.0`, migrations 1–18, and the matching Console bundle. The exact
pre-cutover database/object backup is
`/srv/voiceasset-backups/2026-07-18T1116Z-before-contract-0.22.0-r1`; the
binary rollback snapshot is `backups/server-before-workspace-20260718.11`; it
contains 42 database objects and 42 storage files. Migration 18 adds the
signed outbound Webhook endpoint and delivery outbox. The authenticated
WebSocket realtime transcription endpoint uses the `voiceasset.realtime.v1`
subprotocol and passed a remote Mock ASR start/audio/finish flow. Owner Session-only
create/list/update/rotate/test/delivery inspection passed through the public
10443 gateway, including one-time secret responses and cleanup of the test
endpoint. The certificate fingerprint on 443 and 10443 remains identical
(`8CAF123ADD29ECA48BB2A9D2D40185A74589BBD291F8FD732990479CC71DE0FF`).
Before cutover, that backup restored into a disposable database/object root and
the staged migrator advanced it from schema 17 to 18 while retaining all 42
files. Readiness reports 45 sorted features. Strict public-TLS notification
acceptance passed unauthenticated denial, API-key denial, a 35-event safe-field
history, ordered and stable empty checkpoints, authenticated workspace/user
cursor binding, tamper rejection, `405` with `Allow: GET`, safe immutable read
audits, logout, and post-logout denial. The same gateway completed an
authenticated WebSocket start/audio/finish flow with the Mock ASR stream. API,
Worker, MCP, gateway, Prometheus,
and public Caddy are active after the cutover; the post-deploy error-priority
journal is empty. Current API/Worker/MCP PIDs are `163047`, `163043`, and
`163048`; public Caddy PID `18314` and independent gateway PID `146764`
remained unchanged. The shared certificate fingerprint remains
`8CAF123ADD29ECA48BB2A9D2D40185A74589BBD291F8FD732990479CC71DE0FF`.
The deployed API, Worker, migrate, adminctl, and MCP SHA-256 values are
`ce999d176f8d869ec2fb0b8c24016109d2e723b25385eac2aded107c0dbaa86f`,
`939bd1d2bb4e1660b308e9df54263e15de44c74e75aa4b8a16bdd4a31c87ff6f`,
`af3c36eb4478d7fcaf3cc8b361db2445c076ec3cd4e9be840241131ddadc67d1`,
`3d31a0341c2069afdf0e703f0adb013e477d14b6e92bad1be20ae26a61d79eb0`, and
`613e9233f91861c636a69451b85735dedc524dbd704d9dfa5ab10c4e87ccbe3e`.

Earlier `0.13.0` acceptance established strict TLS/hostname validation,
official-SDK MCP reads, and the following end-to-end workflows.
A real browser uploaded a WAV, completed Mock ASR, authenticated and validated
the generated PNG signature, decoded the waveform, changed playback speed,
found its latest immutable Segment by transcript term plus Provider and Speaker,
verified the exact timecode, and completed manual plus glossary-only correction
approval. Separate failure-safe browser cases passed metadata/trash/restore,
immutable export, and credential-free Provider regressions with empty Web
Storage and zero axe violations. The `0.12.0` acceptance flow additionally
uploaded and transcribed a dedicated WAV, moved it to trash, submitted its exact
UUID, and observed its durable permanent-purge job succeed. The asset graph and
both stored objects were absent, retained totals returned to 11 assets/42
objects, request/completion audits remained, and browser cookies/Web Storage
were empty after sign-out.
A Console-only r2 follow-up additionally waits for every asset job to become
terminal, then proves the successful purge removes the matching upload, audio,
and transcript result from browser memory without a refresh. Its self-cleaning
run returned the deployment to 11 assets/42 objects and signed out without
Cookie or Web Storage. The retained Console archive SHA-256 is
`0a7a72f6f523d76c9ef05ca4b2277f2a79094873228bf4766c9c77e27cb93c39`,
and its rollback snapshot is `backups/console-before-contract-0.12.0-r2`.
The `0.13.0` follow-up validated workspace-scoped Job Center filters, Audit Log,
live Dashboard, and System Status through a real Owner browser. It checks exact
response-field allowlists, absence of job payload/idempotency/lease-owner data,
empty Web Storage, no unexpected client writes, and zero axe violations.
At that `0.13.0` acceptance point, the official MCP Go SDK separately proved
unauthenticated `401`, 21-tool discovery, capabilities, and asset listing over
the then-active internal-CA TLS. Public Caddy PID `18314` and independent
gateway PID `96419` were unchanged;
all four VoiceAsset units have zero restarts and their post-deploy error-priority
journal is empty. The retained combined deployment bundle SHA-256 is
`34eb142be272356522a2fc80346944be39f4247835401b3dc7be0450b7fec1c2`.
The histogram Server follow-up passed local tests/vet/build, an exact-source
Linux race run, staged/live SHA-256 comparison, direct scrape and method checks,
structured-log redaction, and gateway non-exposure. Its deterministic archive
SHA-256 is
`8f2ff94d780bdefcbd95a98ec63db085a5fd94e5f1a4cdd1d1fec0cd10f04dc7`;
the verified binary rollback snapshot is
`backups/server-before-histogram-20260717.5-r1`. At that checkpoint, API, Worker, and MCP
PIDs are `142663`, `142665`, and `142664`. Their deployed binary SHA-256 values
are `2e6c07cd74d9b1ff12005eb08dd1388032a5f184fde68173425802609c82ce80`,
`bb312546e7eb138633fbe15ac986b4c1ce380fc2d80ecb708545cc47deaa6222`,
and `2508bb3d416488a7d3d0b5fddea9c2805b6509f29b15954407ad8c54d6410a8a`.
Prometheus 3.13.1 PID `136995` uses the checksum-verified
official binary, listens only on loopback, reports both targets healthy, retains
history across restart, and keeps all four rules healthy/inactive. At that
monitoring acceptance point, gateway PID `96419`, public Caddy PID `18314`, zero
restart counts, and empty error journals proved the isolation boundary remained
intact. That checkpoint's combined deployment
archive SHA-256 is
`447031b5a7e28e65b6dbb70178f0a4c9ac0dfc623d8d83ee50f9a577a89a446c`;
the Console r2 archive SHA-256 is
`01a24dfd000f8577e44da6010b9720c17f928d58b998ca18edb08452806b0974`.
The immediate pre-0.13 snapshot remains under
`backups/phase6-before-contract-0.13.0.20260717.6-r1`; older verified database/
object backups remain under `/srv/voiceasset-backups`.

The independent gateway reads the existing `api.getio.net` public certificate
through root-managed symlinks under `config/tls`; it does not copy the private
key or share Caddy configuration. The gateway runs as the dedicated `caddy`
user with primary group `voiceasset`, so API and Worker processes cannot read
the public key material. A daily timer sends `SIGUSR1` only to the independent
gateway, forcing Caddy to reload the same configuration and renewed certificate
files. The public Caddy process, configuration, and ports 80/443 are untouched.
The certificate switch intentionally restarted only the independent gateway,
whose current PID is `146764`; public Caddy remained PID `18314` with its exact
configuration SHA-256 unchanged and zero restarts. External Windows validation
uses the operating-system trust store without `--insecure` or a custom CA; both
443 and 10443 present the same `api.getio.net` leaf certificate, and the 10443
readiness/capability checks plus the 400-request performance gate pass.
The 0.17 deployment reused those same symlinks without copying the private key
or changing either Caddy configuration. Both 443 and 10443 still present the
same system-trusted leaf (SHA-256
`8caf123add29eca48bb2a9d2d40185a74589bbd291f8fd732990479cc71de0ff`),
and the public Caddy configuration SHA-256 remains
`b5758330e82589f33ead4f0cb4556544275f3adcbc9098268123e151dfc766ae`.
Do not add HSTS on this test port because the hostname also serves another
public route.

## Configuration and Operations

Store only the required PostgreSQL connection string and VoiceAsset settings
in `config/server.env` with mode `0640`. Store the scoped Server API key and a
separate inbound MCP bearer token in root-owned `config/mcp.env` with mode
`0640`; neither token belongs in command arguments, logs, or documentation.
Keep `VOICE_ASSET_MCP_ENABLE_WRITES=false` for read-only deployments. Setting it
to `true` exposes write tools but does not bypass the outbound API key's scopes.
Store non-secret gateway paths and addresses in `config/gateway.env`. Never
upload the workspace root `.env`.
Phase 3 provider profiles additionally require a unique base64-encoded 32-byte
`VOICEASSET_PROFILE_MASTER_KEY`; generate it on the host, never print it, and
keep it with the same root-owned `0640` environment file.

On the current Docker-free test host, PostgreSQL uses a loopback-only Debian
cluster and peer authentication for the unprivileged `voiceasset` service
user. Port `5432` is not exposed. A managed PostgreSQL endpoint may replace it
only after that endpoint allows connections from the test host.

Validate and operate the isolated units with:

```bash
systemd-analyze verify /etc/systemd/system/voiceasset-*.service /etc/systemd/system/voiceasset-*.timer
systemctl restart voiceasset-migrate.service
systemctl enable --now voiceasset-api voiceasset-worker voiceasset-mcp voiceasset-gateway voiceasset-prometheus voiceasset-gateway-cert-reload.timer
systemctl --no-pager --full status voiceasset-api voiceasset-worker voiceasset-mcp voiceasset-gateway voiceasset-prometheus
ufw allow 10443/tcp comment 'VoiceAsset test HTTPS'
```

Validate externally with the operating system's public trust store:

```bash
curl https://api.getio.net:10443/readyz
curl https://api.getio.net:10443/version
curl https://api.getio.net:10443/api/v1/system/capabilities
# Supply the separately managed inbound bearer token to an MCP client at:
# https://api.getio.net:10443/mcp
```

Validate process-local metrics on the host without sending credentials:

```bash
curl --fail --silent --show-error http://127.0.0.1:18080/metrics
curl --fail --silent --show-error http://127.0.0.1:19090/-/ready
curl --fail --silent --show-error http://127.0.0.1:19090/api/v1/targets
curl --fail --silent --show-error http://127.0.0.1:19090/api/v1/rules
cd /data/apps/caddy/voice/config/prometheus
../../bin/promtool check config prometheus.yml
../../bin/promtool test rules voiceasset.rules.test.yml
```

The embedded counters reset with the API process, while the isolated Prometheus
TSDB preserves scraped history within its 7-day/1-GiB limits. Alertmanager and
the local receiver are now configured and were verified with a synthetic alert.
The API and Worker export OTLP/HTTP traces to the loopback Collector when
`VOICEASSET_OTEL_EXPORTER_OTLP_ENDPOINT` is set; a live API trace was observed
in the protected Collector output. Retain systemd journals as the authoritative
service log.

The API and MCP ports must remain loopback-only, and firewall exposure must be
limited to TCP `10443`.

The bootstrap Owner credential is stored only on the host at
`config/bootstrap-owner.env` with owner `root:root` and mode `0600`. Retrieve
or rotate it over the root SSH channel; never print it in logs or copy it into
a repository.

Confirm the isolation boundary after every deployment:

```bash
ss -ltnp | grep -E ':(10443|18080|18090|19090|5432)\b'
ufw status | grep -E '^(10443|18080|18090|19090|5432)'
journalctl -u voiceasset-api -u voiceasset-worker -u voiceasset-mcp -u voiceasset-gateway -u voiceasset-prometheus -p err
systemctl list-timers voiceasset-gateway-cert-reload.timer
```

To take only VoiceAsset offline without affecting the existing Caddy service,
disable `voiceasset-gateway`, `voiceasset-mcp`, `voiceasset-worker`, and
`voiceasset-api` plus `voiceasset-prometheus`, then remove the named `10443/tcp`
UFW rule. Preserve the
database and object path unless an authorized data-destruction procedure
explicitly removes them.
