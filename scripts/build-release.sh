#!/usr/bin/env bash

set -euo pipefail

usage() {
  echo "usage: $0 <version> <commit> <output-directory>" >&2
}

fail() {
  echo "build-release: $*" >&2
  exit 1
}

if (( $# != 3 )); then
  usage
  exit 2
fi

version=$1
commit=$2
output_argument=$3

[[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]] ||
  fail "version must be a semantic version tag such as v1.2.3 or v1.2.3-rc.1"
[[ $commit =~ ^[0-9A-Fa-f]{7,64}$ ]] || fail "commit must be a 7-64 character hexadecimal revision"

for command in go tar gzip mktemp; do
  command -v "$command" >/dev/null 2>&1 || fail "required command is unavailable: $command"
done

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
mkdir -p -- "$output_argument"
output_dir=$(cd -- "$output_argument" && pwd -P)
[[ -z $(find "$output_dir" -mindepth 1 -maxdepth 1 -print -quit) ]] ||
  fail "output directory must be empty: $output_dir"

temp_root=$(cd -- "${TMPDIR:-/tmp}" && pwd -P)
staging=$(mktemp -d "$temp_root/voiceasset-server-release.XXXXXX")
cleanup() {
  case $staging in
    "$temp_root"/voiceasset-server-release.*) rm -rf -- "$staging" ;;
    *) echo "build-release: refusing to clean unexpected path: $staging" >&2 ;;
  esac
}
trap cleanup EXIT

targets=(
  linux/amd64
  linux/arm64
  windows/amd64
  windows/arm64
  darwin/amd64
  darwin/arm64
)
commands=(api worker migrate adminctl)
ldflags="-s -w -buildid= -X github.com/getio0909/voice-asset-server/internal/platform/product.ServerVersion=$version -X github.com/getio0909/voice-asset-server/internal/platform/product.Commit=$commit"

cd -- "$repo_root"
export SOURCE_DATE_EPOCH=0
for target in "${targets[@]}"; do
  IFS=/ read -r goos goarch <<<"$target"
  package="voiceasset-server-$version-$goos-$goarch"
  package_dir="$staging/$package"
  extension=
  [[ $goos != windows ]] || extension=.exe
  mkdir -p -- "$package_dir"

  for command in "${commands[@]}"; do
    CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch GOWORK=off go build \
      -buildvcs=false -trimpath -ldflags="$ldflags" \
      -o "$package_dir/voiceasset-$command$extension" "./cmd/$command"
  done

  cp -- LICENSE CHANGELOG.md CONTRACT_VERSION "$package_dir/"
  cp -R -- migrations contracts "$package_dir/"
  tar --sort=name --mtime='@0' --owner=0 --group=0 --numeric-owner \
    -C "$staging" -cf - "$package" | gzip -n >"$staging/$package.tar.gz"
  mv -- "$staging/$package.tar.gz" "$output_dir/"
  rm -rf -- "$package_dir"
done

echo "built ${#targets[@]} release archives in $output_dir"
