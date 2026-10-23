#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

target="${1:-all}"
case "$target" in
  windows|linux|all) ;;
  *) printf 'Usage: bash scripts/build.sh [windows|linux|all]\n' >&2; exit 2 ;;
esac

if [[ "$(uname -s)" != Linux ]]; then
  printf 'Compile on the approved Linux build VM, not on the Mac.\n' >&2
  exit 1
fi

export GOTOOLCHAIN=local
version="$(go env GOVERSION)"
if [[ ! "$version" =~ ^go([0-9]+)\.([0-9]+) ]] ||
   (( BASH_REMATCH[1] < 1 || (BASH_REMATCH[1] == 1 && BASH_REMATCH[2] < 25) )); then
  printf 'Go 1.25 or newer is required; found %s.\n' "$version" >&2
  exit 1
fi

if [[ ! -f third_party/doubletake/cmd/aircast-engine/main.go ]]; then
  printf 'The casting engine source is missing. Refusing a discovery-only build.\n' >&2
  exit 1
fi

out="${AIRCAST_DIST_DIR:-$PWD/dist}"
mkdir -p "$out"
out="$(cd "$out" && pwd)"
build_work="$(mktemp -d "$out/.build.XXXXXX")"
trap 'rm -rf -- "$build_work"' EXIT
export GOMAXPROCS="${GOMAXPROCS:-2}"
export GOAMD64=v1
export GOARCH=amd64
flags=(-mod=readonly -buildvcs=false -trimpath)

check_output_file() {
  if [[ -L "$1" || ( -e "$1" && ! -f "$1" ) ]]; then
    printf 'Refusing unsafe output destination: %s\n' "$1" >&2
    exit 1
  fi
}

build_platform() {
  local platform="$1" suffix="" gui_ldflags='-s -w' compiler engine_cgo=0 name
  if [[ "$platform" == windows ]]; then
    suffix=.exe
    compiler="${WINDOWS_CC:-${CC:-x86_64-w64-mingw32-gcc}}"
    gui_ldflags+=' -H=windowsgui'
    engine_cgo=1
  else
    compiler="${LINUX_CC:-gcc}"
  fi
  local names=("aircast-$platform-amd64$suffix" "aircastctl-$platform-amd64$suffix"
    "aircast-engine-$platform-amd64$suffix" "SHA256SUMS-$platform")
  for name in "${names[@]}"; do check_output_file "$out/$name"; done
  command -v "$compiler" >/dev/null
  CGO_ENABLED=1 GOOS="$platform" CC="$compiler" go build "${flags[@]}" \
    -ldflags="$gui_ldflags" -o "$build_work/aircast-$platform-amd64$suffix" ./cmd/aircast
  CGO_ENABLED=0 GOOS="$platform" go build "${flags[@]}" -ldflags='-s -w' \
    -o "$build_work/aircastctl-$platform-amd64$suffix" ./cmd/aircastctl
  (
    cd third_party/doubletake
    CGO_ENABLED="$engine_cgo" GOOS="$platform" CC="$compiler" go build "${flags[@]}" -ldflags='-s -w' \
      -o "$build_work/aircast-engine-$platform-amd64$suffix" ./cmd/aircast-engine
  )
  (
    cd "$build_work"
    sha256sum "aircast-$platform-amd64$suffix" "aircastctl-$platform-amd64$suffix" \
      "aircast-engine-$platform-amd64$suffix" > "SHA256SUMS-$platform"
  )
  for name in "${names[@]}"; do
    check_output_file "$out/$name"
    mv -fT -- "$build_work/$name" "$out/$name"
  done
}

if [[ "$target" == all ]]; then
  build_platform linux
  build_platform windows
else
  build_platform "$target"
fi
