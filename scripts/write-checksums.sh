#!/usr/bin/env bash

set -euo pipefail

fail() {
  echo "write-checksums: $*" >&2
  exit 1
}

if (( $# != 1 )); then
  echo "usage: $0 <release-directory>" >&2
  exit 2
fi

command -v sha256sum >/dev/null 2>&1 || fail "required command is unavailable: sha256sum"
[[ -d $1 ]] || fail "release directory does not exist: $1"
release_dir=$(cd -- "$1" && pwd -P)

shopt -s nullglob
artifacts=("$release_dir"/*.tar.gz "$release_dir"/*.oci.tar "$release_dir"/*.json)
(( ${#artifacts[@]} > 0 )) || fail "no release artifacts found in $release_dir"
for artifact in "${artifacts[@]}"; do
  [[ -f $artifact && ! -L $artifact ]] || fail "artifact must be a regular, non-symlink file: $artifact"
done

mapfile -t names < <(
  for artifact in "${artifacts[@]}"; do
    printf './%s\n' "$(basename -- "$artifact")"
  done | LC_ALL=C sort
)

temporary="$release_dir/.SHA256SUMS.$$"
trap 'rm -f -- "$temporary"' EXIT
(
  cd -- "$release_dir"
  sha256sum --binary -- "${names[@]}"
) >"$temporary"
mv -- "$temporary" "$release_dir/SHA256SUMS"
trap - EXIT

(
  cd -- "$release_dir"
  sha256sum -c SHA256SUMS
)
