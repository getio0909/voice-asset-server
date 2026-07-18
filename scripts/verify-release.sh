#!/usr/bin/env bash

set -euo pipefail

usage() {
  echo "usage: $0 <version> <commit> <release-directory> [--require-sbom] [--require-container]" >&2
}

fail() {
  echo "verify-release: $*" >&2
  exit 1
}

if (( $# < 3 )); then
  usage
  exit 2
fi

version=$1
commit=$2
release_argument=$3
require_sbom=false
require_container=false
shift 3
while (( $# > 0 )); do
  case $1 in
    --require-sbom) require_sbom=true ;;
    --require-container) require_container=true ;;
    *) usage; exit 2 ;;
  esac
  shift
done

[[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]] ||
  fail "invalid semantic version tag: $version"
[[ $commit =~ ^[0-9A-Fa-f]{7,64}$ ]] || fail "invalid hexadecimal revision: $commit"
[[ -d $release_argument ]] || fail "release directory does not exist: $release_argument"
release_dir=$(cd -- "$release_argument" && pwd -P)
repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)

for command in go node tar sha256sum mktemp cmp grep awk tr; do
  command -v "$command" >/dev/null 2>&1 || fail "required command is unavailable: $command"
done

[[ -f $release_dir/SHA256SUMS && ! -L $release_dir/SHA256SUMS ]] ||
  fail "SHA256SUMS must be a regular, non-symlink file"
if $require_sbom; then
  [[ -f $release_dir/voiceasset-server-source.spdx.json && ! -L $release_dir/voiceasset-server-source.spdx.json ]] ||
    fail "required SPDX SBOM is missing"
fi

targets=(
  linux/amd64
  linux/arm64
  windows/amd64
  windows/arm64
  darwin/amd64
  darwin/arm64
)
expected_archives=()
for target in "${targets[@]}"; do
  IFS=/ read -r goos goarch <<<"$target"
  expected_archives+=("voiceasset-server-$version-$goos-$goarch.tar.gz")
done
mapfile -t expected_archives < <(printf '%s\n' "${expected_archives[@]}" | LC_ALL=C sort)
container_name="voiceasset-server-$version-linux-amd64-arm64.oci.tar"
container_path="$release_dir/$container_name"

shopt -s nullglob dotglob
archive_paths=("$release_dir"/*.tar.gz)
mapfile -t actual_archives < <(
  for archive in "${archive_paths[@]}"; do basename -- "$archive"; done | LC_ALL=C sort
)
[[ $(printf '%s\n' "${actual_archives[@]}") == $(printf '%s\n' "${expected_archives[@]}") ]] ||
  fail "release directory does not contain exactly the six expected archives"
container_paths=("$release_dir"/*.oci.tar)
if $require_container && (( ${#container_paths[@]} != 1 )); then
  fail "required multi-platform OCI archive is missing"
fi
if (( ${#container_paths[@]} > 1 )); then
  fail "release directory contains multiple OCI archives"
fi
if (( ${#container_paths[@]} == 1 )) && [[ $(basename -- "${container_paths[0]}") != "$container_name" ]]; then
  fail "release directory contains an unexpected OCI archive"
fi

artifact_paths=("$release_dir"/*.tar.gz "$release_dir"/*.oci.tar "$release_dir"/*.json)
mapfile -t expected_checksum_names < <(
  for artifact in "${artifact_paths[@]}"; do
    [[ -f $artifact && ! -L $artifact ]] || fail "artifact must be a regular, non-symlink file: $artifact"
    printf './%s\n' "$(basename -- "$artifact")"
  done | LC_ALL=C sort
)
mapfile -t checksum_names < <(awk '{name = $2; sub(/^\*/, "", name); print name}' "$release_dir/SHA256SUMS" | LC_ALL=C sort)
[[ $(printf '%s\n' "${checksum_names[@]}") == $(printf '%s\n' "${expected_checksum_names[@]}") ]] ||
  fail "SHA256SUMS does not cover exactly the release artifacts"
while read -r checksum name extra; do
  [[ -z ${extra:-} && $checksum =~ ^[0-9a-f]{64}$ && $name == \*./* ]] ||
    fail "malformed SHA256SUMS entry"
done <"$release_dir/SHA256SUMS"
(
  cd -- "$release_dir"
  sha256sum -c SHA256SUMS
)
if (( ${#container_paths[@]} == 1 )); then
  node "$repo_root/scripts/verify-oci-image.mjs" "$container_path" \
    voiceasset-server "$version" "$commit"
fi

temp_root=$(cd -- "${TMPDIR:-/tmp}" && pwd -P)
staging=$(mktemp -d "$temp_root/voiceasset-server-verify.XXXXXX")
cleanup() {
  case $staging in
    "$temp_root"/voiceasset-server-verify.*) rm -rf -- "$staging" ;;
    *) echo "verify-release: refusing to clean unexpected path: $staging" >&2 ;;
  esac
}
trap cleanup EXIT
host_goos=$(go env GOOS)
host_goarch=$(go env GOARCH)

for target in "${targets[@]}"; do
  IFS=/ read -r goos goarch <<<"$target"
  package="voiceasset-server-$version-$goos-$goarch"
  archive="$release_dir/$package.tar.gz"
  extension=
  [[ $goos != windows ]] || extension=.exe

  listing="$staging/$package.list"
  tar -tzf "$archive" >"$listing"
  [[ -s $listing ]] || fail "archive is empty: $archive"
  while IFS= read -r entry; do
    [[ $entry != /* && ! $entry =~ (^|/)\.\.(/|$) ]] || fail "unsafe archive path: $entry"
    case $entry in
      "$package" | "$package/" | "$package/"*) ;;
      *) fail "archive entry escapes its package root: $entry" ;;
    esac
  done <"$listing"

  extract_dir="$staging/extract-$goos-$goarch"
  mkdir -p -- "$extract_dir"
  tar -xzf "$archive" -C "$extract_dir"
  package_dir="$extract_dir/$package"
  [[ -d $package_dir ]] || fail "package directory is missing: $package"
  [[ -z $(find "$package_dir" -type l -print -quit) ]] || fail "package contains a symbolic link: $package"

  expected_top=(CHANGELOG.md CONTRACT_VERSION LICENSE contracts migrations)
  for command in api worker migrate adminctl; do
    expected_top+=("voiceasset-$command$extension")
  done
  mapfile -t expected_top < <(printf '%s\n' "${expected_top[@]}" | LC_ALL=C sort)
  top_paths=("$package_dir"/*)
  mapfile -t actual_top < <(
    for path in "${top_paths[@]}"; do basename -- "$path"; done | LC_ALL=C sort
  )
  [[ $(printf '%s\n' "${actual_top[@]}") == $(printf '%s\n' "${expected_top[@]}") ]] ||
    fail "unexpected top-level package contents: $package"
  cmp -- "$repo_root/CONTRACT_VERSION" "$package_dir/CONTRACT_VERSION" >/dev/null ||
    fail "CONTRACT_VERSION differs from the repository pin: $package"
  [[ -n $(find "$package_dir/contracts" -type f -print -quit) ]] || fail "contracts are missing: $package"
  [[ -n $(find "$package_dir/migrations" -type f -print -quit) ]] || fail "migrations are missing: $package"

  for command in api worker migrate adminctl; do
    binary="$package_dir/voiceasset-$command$extension"
    [[ -f $binary && ! -L $binary ]] || fail "binary is missing: $binary"
    metadata=$(go version -m "$binary") || fail "cannot inspect Go metadata: $binary"
    grep -Fq $'\tbuild\tGOOS='"$goos" <<<"$metadata" || fail "wrong GOOS in $binary"
    grep -Fq $'\tbuild\tGOARCH='"$goarch" <<<"$metadata" || fail "wrong GOARCH in $binary"
    grep -aFq -- "$version" "$binary" || fail "version was not injected into $binary"
    grep -aFq -- "$commit" "$binary" || fail "commit was not injected into $binary"
  done

  if [[ $goos == "$host_goos" && $goarch == "$host_goarch" ]]; then
    for command in api worker migrate adminctl; do
      version_argument=--version
      [[ $command != adminctl ]] || version_argument=version
      runtime_version=$("$package_dir/voiceasset-$command$extension" "$version_argument") ||
        fail "host $command version command failed"
      runtime_version=$(printf '%s' "$runtime_version" | tr -d '[:space:]')
      grep -Fq '"server_version":'"\"$version\"" <<<"$runtime_version" ||
        fail "host $command reported the wrong version"
      grep -Fq '"commit":'"\"$commit\"" <<<"$runtime_version" ||
        fail "host $command reported the wrong commit"
    done
  fi
done

echo "verified ${#targets[@]} Server archives and SHA-256 checksums"
