#!/usr/bin/env bash
set -euo pipefail

compose_file=${COMPOSE_FILE:-deploy/compose/compose.yaml}
project=${COMPOSE_PROJECT_NAME:-voiceasset-ci}

compose() {
  docker compose --project-name "$project" --file "$compose_file" "$@"
}

cleanup() {
  status=$?
  if [[ "$status" -ne 0 ]]; then
    compose logs --no-color --tail=100 gateway api worker >&2 || true
  fi
  compose down --volumes --remove-orphans
  return "$status"
}
trap cleanup EXIT

# The CI image tags are built by the workflow. Keeping the project name explicit
# prevents this smoke from touching a developer's unrelated Compose volumes.
compose up --detach worker
compose up --detach --wait --wait-timeout 180 api gateway

compose ps --status running --services | grep -Fx worker >/dev/null

export COMPOSE_SMOKE_EMAIL="${COMPOSE_SMOKE_EMAIL:-compose-owner@example.test}"
export COMPOSE_SMOKE_PASSWORD="${COMPOSE_SMOKE_PASSWORD:-compose-ci-only-password-20260718}"
printf '%s\n' "$COMPOSE_SMOKE_PASSWORD" | compose run --rm --no-deps -T admin \
  create-admin --email "$COMPOSE_SMOKE_EMAIL" --workspace "Compose Smoke" --password-stdin >/dev/null

curl --fail --silent --show-error --retry 20 --retry-all-errors --retry-delay 2 \
  http://127.0.0.1:8080/readyz >/dev/null

capabilities=$(curl --fail --silent --show-error \
  http://127.0.0.1:8080/api/v1/system/capabilities)
printf '%s\n' "$capabilities" | grep -F '"api_version":"v1"' >/dev/null
printf '%s\n' "$capabilities" | grep -F '"contract_version"' >/dev/null

node scripts/compose-http-smoke.mjs

printf 'Compose smoke passed through the Console gateway.\n'
