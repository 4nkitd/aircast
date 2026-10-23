#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ "$(uname -s)" != Linux ]]; then
  printf 'Run packaging tests on the disposable Linux build VM.\n' >&2
  exit 1
fi
root="$PWD"
work="$(mktemp -d "${TMPDIR:-/tmp}/aircast-package-tests.XXXXXX")"
trap 'rm -rf -- "$work"' EXIT
printf 'keep this sentinel\n' > "$work/sentinel"
for name in aircast-linux-amd64 aircastctl-linux-amd64 aircast-engine-linux-amd64 SHA256SUMS-linux; do
  out="$work/$name"
  mkdir "$out"
  ln -s "$work/sentinel" "$out/$name"
  if AIRCAST_DIST_DIR="$out" bash scripts/build.sh linux > "$work/build.log" 2>&1; then
    printf 'Build accepted symlink destination: %s\n' "$name" >&2
    exit 1
  fi
  grep -q 'Refusing unsafe output destination' "$work/build.log"
done
for kind in archive checksum directory; do
  out="$work/package-$kind"
  mkdir "$out"
  if [[ "$kind" == checksum ]]; then
    ln -s "$work/sentinel" "$out/aircast-linux-amd64-casting.tar.gz.sha256"
  elif [[ "$kind" == directory ]]; then
    mkdir "$out/aircast-linux-amd64-casting.tar.gz"
  else
    mkdir "$work/external-directory"
    printf 'keep external bundle\n' > "$work/external-directory/bundle.tar.gz"
    ln -s "$work/external-directory" "$out/aircast-linux-amd64-casting.tar.gz"
  fi
  if (
    source "$root/scripts/package-common.sh"
    AIRCAST_DIST_DIR="$out" package_init linux
  ) > "$work/package.log" 2>&1; then
    printf 'Packaging accepted unsafe %s destination\n' "$kind" >&2
    exit 1
  fi
  grep -q 'Refusing unsafe output destination' "$work/package.log"
done
(
  source "$root/scripts/package-common.sh"
  AIRCAST_DIST_DIR="$work/normal" package_init linux
  printf 'test payload\n' > "$package_stage/payload.txt"
  ln "$work/sentinel" "$package_out/$package_name.tar.gz.sha256"
  package_archive tar.gz
  cd "$package_out"
  sha256sum --check "$package_name.tar.gz.sha256"
)
test "$(cat "$work/sentinel")" = 'keep this sentinel'
test "$(cat "$work/external-directory/bundle.tar.gz")" = 'keep external bundle'
printf 'Packaging path safety tests passed.\n'
