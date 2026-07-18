#!/usr/bin/env bash
set -euo pipefail

compose_file=${COMPOSE_FILE:-deploy/compose/compose.yaml}
project=${COMPOSE_PROJECT_NAME:-voiceasset-ci}

compose() {
  docker compose --project-name "$project" --file "$compose_file" "$@"
}

cleanup() {
  compose down --volumes --remove-orphans
}
trap cleanup EXIT

# The CI image tags are built by the workflow. Keeping the project name explicit
# prevents this smoke from touching a developer's unrelated Compose volumes.
compose up --detach --wait --wait-timeout 180 api worker gateway

curl --fail --silent --show-error --retry 20 --retry-all-errors --retry-delay 2 \
  http://127.0.0.1:8080/readyz >/dev/null

capabilities=$(curl --fail --silent --show-error \
  http://127.0.0.1:8080/api/v1/system/capabilities)
printf '%s\n' "$capabilities" | grep -F '"api_version":"v1"' >/dev/null
printf '%s\n' "$capabilities" | grep -F '"contract_version"' >/dev/null

printf 'Compose smoke passed through the Console gateway.\n'
