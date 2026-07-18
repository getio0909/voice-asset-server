# Isolated Observability Runbook

The authorized 10443 test host runs observability services independently of
the public Caddy process:

| Service | Loopback listener | Purpose |
| --- | --- | --- |
| Prometheus 3.13.1 | `127.0.0.1:19090` | Metrics and checked-in alert rules |
| Alertmanager 0.33.1 | `127.0.0.1:19093` | Alert grouping and delivery |
| Alert receiver | `127.0.0.1:19193` | Secret-free local notification journal |
| OTel Collector 0.155.0 | `127.0.0.1:14318` | OTLP/HTTP trace intake |
| OTel health | `127.0.0.1:13133` | Collector readiness |

The receiver persists only `alert_name`, `severity`, `service`, status, and
timestamps in `/data/apps/caddy/voice/alerts/notifications.jsonl` (mode 0600).
It deliberately discards annotations, generator URLs, and unknown labels.
Collector output is protected under `/data/apps/caddy/voice/otel`.

Safe checks on the host:

```bash
curl --fail http://127.0.0.1:19193/healthz
curl --fail http://127.0.0.1:19093/-/ready
curl --fail http://127.0.0.1:13133/
/data/apps/caddy/voice/bin/promtool-3.13.1 check config \
  /data/apps/caddy/voice/config/prometheus/prometheus.yml
systemctl is-active voiceasset-prometheus voiceasset-alertmanager \
  voiceasset-alert-receiver voiceasset-otelcol
```

The OTLP endpoint is loopback-only and is enabled for API/Worker through
`VOICEASSET_OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:14318`. Never expose
ports 13133, 14318, 19090, 19093, 19094, or 19193 through Caddy or UFW.
