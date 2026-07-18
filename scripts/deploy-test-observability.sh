#!/usr/bin/env bash
set -euo pipefail

# Installs the isolated test-host observability slice. Run as root on the
# authorized host after copying the staged files into /tmp. This script never
# prints or reads the VoiceAsset credential values.

base=/data/apps/caddy/voice
stage=/tmp/voiceasset-observability-install-20260718
otel_version=0.155.0
otel_sha256=229cfddeb0621d2a011bfd1c8894335479e46349b93a0cfbccbe653443a3ec95
alertmanager_version=0.33.1
alertmanager_sha256=93d802cba6a8d27239d747ce117df7648d326ab67394e32247540b030e9842ba

test "$(id -u)" -eq 0
test "$(uname -m)" = x86_64
test -x "$base/bin/prometheus-3.13.1"
test -f "$base/config/server.env"
test -f /tmp/voiceasset-alert-receiver-20260718
test -d /tmp/voiceasset-observability-20260718

rm -rf "$stage"
install -d -o root -g root -m 0700 "$stage" "$stage/otel" "$stage/alertmanager"
install -d -o voiceasset -g voiceasset -m 0700 \
  "$base/otel" "$base/alerts" "$base/alertmanager" \
  "$base/config/otelcol" "$base/config/alertmanager"

backup="$base/backups/observability-before-${alertmanager_version}-$(date -u +%Y%m%dT%H%M%SZ)"
install -d -o root -g root -m 0700 "$backup"
for filename in \
  "$base/config/prometheus/prometheus.yml" \
  "$base/config/server.env" \
  "$base/config/otelcol/config.yaml" \
  "$base/config/alertmanager/alertmanager.yml"; do
  if test -f "$filename"; then
    cp -p "$filename" "$backup/$(basename "$filename")"
  fi
done

otel_archive="$stage/otelcol-contrib_${otel_version}_linux_amd64.tar.gz"
curl --fail --location --proto '=https' --tlsv1.2 \
  --output "$otel_archive" \
  "https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v${otel_version}/otelcol-contrib_${otel_version}_linux_amd64.tar.gz"
printf '%s  %s\n' "$otel_sha256" "$otel_archive" | sha256sum --check --strict
tar -xzf "$otel_archive" -C "$stage/otel"
install -o root -g root -m 0755 "$stage/otel/otelcol-contrib" "$base/bin/otelcol-contrib-${otel_version}"
ln -sfn "otelcol-contrib-${otel_version}" "$base/bin/otelcol-contrib"

alertmanager_archive="$stage/alertmanager-${alertmanager_version}.linux-amd64.tar.gz"
curl --fail --location --proto '=https' --tlsv1.2 \
  --output "$alertmanager_archive" \
  "https://github.com/prometheus/alertmanager/releases/download/v${alertmanager_version}/alertmanager-${alertmanager_version}.linux-amd64.tar.gz"
printf '%s  %s\n' "$alertmanager_sha256" "$alertmanager_archive" | sha256sum --check --strict
tar -xzf "$alertmanager_archive" -C "$stage/alertmanager"
install -o root -g root -m 0755 "$stage/alertmanager/alertmanager-${alertmanager_version}.linux-amd64/alertmanager" "$base/bin/alertmanager-${alertmanager_version}"
install -o root -g root -m 0755 /tmp/voiceasset-alert-receiver-20260718 "$base/bin/voiceasset-alert-receiver"
ln -sfn "alertmanager-${alertmanager_version}" "$base/bin/alertmanager"

install -o root -g root -m 0644 /tmp/voiceasset-observability-20260718/config.yaml "$base/config/otelcol/config.yaml"
install -o root -g root -m 0644 /tmp/voiceasset-observability-20260718/alertmanager.yml "$base/config/alertmanager/alertmanager.yml"
install -o root -g root -m 0644 /tmp/voiceasset-observability-20260718/prometheus.yml "$base/config/prometheus/prometheus.yml"
install -o root -g root -m 0644 /tmp/voiceasset-observability-20260718/voiceasset-otelcol.service /etc/systemd/system/voiceasset-otelcol.service
install -o root -g root -m 0644 /tmp/voiceasset-observability-20260718/voiceasset-alertmanager.service /etc/systemd/system/voiceasset-alertmanager.service
install -o root -g root -m 0644 /tmp/voiceasset-observability-20260718/voiceasset-alert-receiver.service /etc/systemd/system/voiceasset-alert-receiver.service

# The endpoint is loopback-only and contains no credentials. Keep any existing
# unrelated environment values unchanged.
if grep -q '^VOICEASSET_OTEL_EXPORTER_OTLP_ENDPOINT=' "$base/config/server.env"; then
  sed -i 's#^VOICEASSET_OTEL_EXPORTER_OTLP_ENDPOINT=.*$#VOICEASSET_OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:14318#' "$base/config/server.env"
else
  printf '\nVOICEASSET_OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:14318\n' >> "$base/config/server.env"
fi
chmod 0600 "$base/config/server.env"

chown -R voiceasset:voiceasset "$base/otel" "$base/alerts" "$base/alertmanager"
chown voiceasset:voiceasset "$base/config/otelcol" "$base/config/alertmanager"
chmod 0700 "$base/otel" "$base/alerts" "$base/alertmanager"

"$base/bin/otelcol-contrib" validate --config="$base/config/otelcol/config.yaml"
"$base/bin/promtool-3.13.1" check config "$base/config/prometheus/prometheus.yml"
"$base/bin/promtool-3.13.1" check rules "$base/config/prometheus/voiceasset.rules.yml"
"$base/bin/promtool-3.13.1" test rules "$base/config/prometheus/voiceasset.rules.test.yml"

systemctl daemon-reload
systemctl enable --now voiceasset-alert-receiver voiceasset-otelcol voiceasset-alertmanager
systemctl restart voiceasset-prometheus
systemctl restart voiceasset-api voiceasset-worker

rm -rf "$stage" /tmp/voiceasset-alert-receiver-20260718 /tmp/voiceasset-observability-20260718
printf '%s\n' "$backup"
