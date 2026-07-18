#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

image=${1:-voiceasset-server:ci}
database_host=${VOICEASSET_CI_DATABASE_HOST:-127.0.0.1}
database_user=${VOICEASSET_CI_DATABASE_USER:-voiceasset}
database_password=${VOICEASSET_CI_DATABASE_PASSWORD:-voiceasset-ci-only}
source_database=${VOICEASSET_CI_SOURCE_DATABASE:-voiceasset_test}
target_database="voiceasset_br_${GITHUB_RUN_ID:-$$}"
work_root="$PWD/.ci-backup-restore"
source_url="postgres://$database_user:$database_password@$database_host:5432/$source_database?sslmode=disable"
target_url="postgres://$database_user:$database_password@$database_host:5432/$target_database?sslmode=disable"

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  set +e
  if [[ "$target_database" == voiceasset_br_* ]]; then
    docker run --rm --network host \
      -e PGPASSWORD="$database_password" \
      --entrypoint dropdb "$image" \
      --if-exists --host "$database_host" --username "$database_user" "$target_database" \
      >/dev/null 2>&1
  fi
  if [[ "$work_root" == */.ci-backup-restore ]]; then
    rm -rf -- "$work_root"
  fi
  exit "$status"
}
trap cleanup EXIT INT TERM

query_database() {
  local database=$1
  local query=$2
  docker run --rm --network host \
    -e PGPASSWORD="$database_password" \
    --entrypoint psql "$image" \
    --host "$database_host" --username "$database_user" --dbname "$database" \
    --no-psqlrc --tuples-only --no-align --command "$query"
}

rm -rf -- "$work_root"
mkdir -p "$work_root/objects" "$work_root/backups" "$work_root/restore-parent"
chmod 0777 "$work_root" "$work_root/objects" "$work_root/backups" "$work_root/restore-parent"
printf 'voiceasset backup fixture\n' >"$work_root/objects/fixture.bin"
chmod 0644 "$work_root/objects/fixture.bin"

docker run --rm --network host \
  -e DATABASE_URL="$source_url" \
  "$image" /app/migrate

docker run --rm --network host \
  -e DATABASE_URL="$source_url" \
  -v "$work_root:/work" \
  "$image" /app/adminctl backup \
  --output /work/backups/backup --storage /work/objects --confirm-offline

docker run --rm -v "$work_root:/work" \
  "$image" /app/adminctl backup-verify --backup /work/backups/backup

docker run --rm --network host \
  -e PGPASSWORD="$database_password" \
  --entrypoint createdb "$image" \
  --host "$database_host" --username "$database_user" \
  --owner "$database_user" "$target_database"

docker run --rm --network host \
  -e DATABASE_URL="$target_url" \
  -v "$work_root:/work" \
  "$image" /app/adminctl restore \
  --backup /work/backups/backup \
  --storage /work/restore-parent/objects \
  --confirm-empty-target

docker run --rm -v "$work_root:/work" \
  "$image" /app/adminctl backup-verify --backup /work/backups/backup

cmp --silent "$work_root/objects/fixture.bin" "$work_root/restore-parent/objects/fixture.bin"
source_migrations=$(query_database "$source_database" 'SELECT count(*) FROM voiceasset_schema_migrations')
target_migrations=$(query_database "$target_database" 'SELECT count(*) FROM voiceasset_schema_migrations')
source_tables=$(query_database "$source_database" "SELECT count(*) FROM pg_tables WHERE schemaname = 'public'")
target_tables=$(query_database "$target_database" "SELECT count(*) FROM pg_tables WHERE schemaname = 'public'")
[[ "$source_migrations" == "$target_migrations" ]]
[[ "$source_tables" == "$target_tables" ]]

printf 'CI_BACKUP_RESTORE_OK migrations=%s tables=%s files=1\n' "$source_migrations" "$source_tables"
