#!/usr/bin/env bash
set -euo pipefail

package_init() {
  package_platform="$1"
  package_name="aircast-$package_platform-amd64-casting"
  package_out="${AIRCAST_DIST_DIR:-$PWD/dist}"
  mkdir -p "$package_out"
  package_out="$(cd "$package_out" && pwd)"
  local extension=tar.gz
  if [[ "$package_platform" == windows ]]; then extension=zip; fi
  package_output_file "$package_out/$package_name.$extension"
  package_output_file "$package_out/$package_name.$extension.sha256"
  package_work="$(mktemp -d "$package_out/.package-$package_platform.XXXXXX")"
  trap 'rm -rf -- "$package_work"' EXIT
  package_stage="$package_work/$package_name"
  package_epoch="${SOURCE_DATE_EPOCH:-315532800}"
  if [[ ! "$package_epoch" =~ ^[0-9]+$ ]] || (( package_epoch < 315532800 )); then
    printf 'SOURCE_DATE_EPOCH must be a Unix timestamp from 1980 or later.\n' >&2
    exit 1
  fi
  export TZ=UTC LC_ALL=C GOTOOLCHAIN=local
  mkdir -p "$package_stage/licenses" "$package_stage/source/go-proxy" "$package_stage/docs"
}

package_output_file() {
  if [[ -L "$1" || ( -e "$1" && ! -f "$1" ) ]]; then
    printf 'Refusing unsafe output destination: %s\n' "$1" >&2
    exit 1
  fi
}

package_proxy_asset() {
  local asset="$1" relative
  relative="${asset#"$(go env GOMODCACHE)/cache/download/"}"
  if [[ "$relative" == "$asset" || ! -f "$asset" ]]; then
    printf 'Missing module source asset: %s\n' "$asset" >&2
    exit 1
  fi
  mkdir -p "$package_stage/source/go-proxy/$(dirname "$relative")"
  cp "$asset" "$package_stage/source/go-proxy/$relative"
}

package_go_modules() {
  local module_dir="$1" module_list module metadata path version directory asset relative notice destination
  module_list="$(cd "$module_dir" && go list -mod=readonly -m -f '{{if not .Main}}{{.Path}}@{{.Version}}{{end}}' all)"
  while IFS= read -r module; do
    [[ -n "$module" ]] || continue
    metadata="$(cd "$module_dir" && go mod download -json "$module")"
    if jq -e '.Error != null' >/dev/null <<< "$metadata"; then
      printf '%s\n' "$metadata" >&2
      exit 1
    fi
    path="$(jq -r .Path <<< "$metadata")"
    version="$(jq -r .Version <<< "$metadata")"
    directory="$(jq -r .Dir <<< "$metadata")"
    destination="$package_stage/licenses/go-modules/$path@$version"
    if [[ -f "$destination/module.json" ]]; then
      continue
    fi
    mkdir -p "$destination"
    jq '{Path, Version, Sum, GoModSum, Origin}' <<< "$metadata" > "$destination/module.json"
    while IFS= read -r asset; do
      package_proxy_asset "$asset"
    done < <(jq -r '.Info, .GoMod, .Zip' <<< "$metadata")
    while IFS= read -r -d '' notice; do
      relative="${notice#"$directory/"}"
      mkdir -p "$destination/$(dirname "$relative")"
      cp "$notice" "$destination/$relative"
    done < <(find "$directory" -type f \( -iname '*license*' -o -iname '*copying*' \
      -o -iname '*notice*' -o -iname '*copyright*' -o -iname '*authors*' -o -iname '*patents*' \) -print0)
  done <<< "$module_list"
  module_list="$(cd "$module_dir" && go mod graph | awk '{ print $1; print $2 }' | sort -u)"
  while IFS= read -r module; do
    [[ "$module" == *@* && "$module" != go@* && "$module" != toolchain@* ]] || continue
    metadata="$(cd "$module_dir" && go list -mod=readonly -m -json "$module")"
    asset="$(jq -r .GoMod <<< "$metadata")"
    package_proxy_asset "$asset"
    asset="${asset%.mod}.info"
    if [[ -f "$asset" ]]; then package_proxy_asset "$asset"; fi
  done <<< "$module_list"
}

package_verify_proxy() {
  local module_dir cache
  for module_dir in "$PWD" "$PWD/third_party/doubletake"; do
    cache="$(mktemp -d "$package_work/cold-go.XXXXXX")"
    (
      cd "$module_dir"
      export GOMODCACHE="$cache" GOPATH="$cache/gopath"
      export GOPROXY="file://$package_stage/source/go-proxy" GOSUMDB=off GOTOOLCHAIN=local
      export GOFLAGS=-modcacherw
      go list -mod=readonly -m all >/dev/null
      go mod download all
    )
  done
}

package_sources() {
  local source_files=(cmd internal scripts docs licenses third_party go.mod go.sum FyneApp.toml)
  local path
  for path in ./*.md; do
    [[ ! -f "$path" ]] || source_files+=("${path#./}")
  done
  cp -R licenses/. "$package_stage/licenses/"
  cp THIRD-PARTY.md "$package_stage/licenses/THIRD-PARTY.md"
  mkdir -p "$package_stage/licenses/doubletake" "$package_stage/licenses/go-runtime"
  cp third_party/doubletake/LICENSE third_party/doubletake/COPYING.GPL \
    "$package_stage/licenses/doubletake/"
  cp "$(go env GOROOT)/LICENSE" "$package_stage/licenses/go-runtime/LICENSE"
  if [[ -f "$(go env GOROOT)/PATENTS" ]]; then
    cp "$(go env GOROOT)/PATENTS" "$package_stage/licenses/go-runtime/PATENTS"
  fi
  package_go_modules "$PWD"
  package_go_modules "$PWD/third_party/doubletake"
  package_verify_proxy
  tar --sort=name --mtime="@$package_epoch" --owner=0 --group=0 --numeric-owner \
    --exclude=.git --exclude='._*' --exclude=.DS_Store --exclude='*.log' \
    -cf - "${source_files[@]}" | gzip -n \
    > "$package_stage/source/aircast-source.tar.gz"
  cp docs/BUILDING.md docs/WINDOWS-TESTING.md "$package_stage/docs/"
  cp scripts/quality/reference-1080p.png "$package_stage/quality-chart-1080p.png"
  cp licenses/SOURCE-AND-RELINK.md "$package_stage/SOURCE-AND-RELINK.md"
}

package_metadata() {
  local binary compiler path
  compiler="${LINUX_CC:-gcc}"
  if [[ "$package_platform" == windows ]]; then
    compiler="${WINDOWS_CC:-${CC:-x86_64-w64-mingw32-gcc}}"
    mkdir -p "$package_stage/licenses/mingw"
    for path in /usr/share/doc/mingw-w64-common/copyright \
      /usr/share/doc/mingw-w64-x86-64-dev/copyright /usr/share/doc/gcc-mingw-w64-base/copyright; do
      if [[ ! -f "$path" ]]; then
        printf 'Required MinGW notice missing: %s\n' "$path" >&2
        exit 1
      fi
      cp "$path" "$package_stage/licenses/mingw/$(basename "$(dirname "$path")")-copyright"
    done
  fi
  {
    go version
    printf 'target: %s/amd64\nGOAMD64: v1\nCGO: GUI=1, CLI=0, engine=' "$package_platform"
    if [[ "$package_platform" == windows ]]; then printf '1\n'; else printf '0\n'; fi
    printf 'Go flags: -mod=readonly -buildvcs=false -trimpath -ldflags="-s -w"\n'
    printf 'Windows GUI adds: -H=windowsgui\n'
    printf 'SOURCE_DATE_EPOCH: %s\n' "$package_epoch"
    "$compiler" --version
    for binary in "$package_stage"/aircast*; do
      [[ -f "$binary" ]] || continue
      printf '\n%s\n' "$(basename "$binary")"
      (cd "$package_stage" && go version -m "$(basename "$binary")")
    done
  } > "$package_stage/BUILD-INFO.txt"
  if [[ "$package_platform" == windows ]]; then
    for binary in "$package_stage"/*.exe; do
      printf '%s\n' "$(basename "$binary")"
      objdump -p "$binary" | awk '/DLL Name:/ { print $3 }' | sort -f -u
    done > "$package_stage/PE-IMPORTS.txt"
  else
    ldd "$package_stage/aircast" | sed -E 's/ \(0x[[:xdigit:]]+\)//g' \
      > "$package_stage/LINUX-SHARED-LIBRARIES.txt"
  fi
}

package_archive() {
  local extension="$1" archive="$package_out/$package_name.$1"
  package_output_file "$archive"
  package_output_file "$archive.sha256"
  (
    cd "$package_stage"
    find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum > "$package_work/SHA256SUMS"
  )
  mv "$package_work/SHA256SUMS" "$package_stage/SHA256SUMS"
  find "$package_stage" -exec touch -h -d "@$package_epoch" {} +
  if [[ "$extension" == zip ]]; then
    (
      cd "$package_work"
      find "$package_name" -type f -print | sort | zip -X -q "$package_work/bundle.zip" -@
    )
    mv -fT -- "$package_work/bundle.zip" "$archive"
  else
    tar --sort=name --mtime="@$package_epoch" --owner=0 --group=0 --numeric-owner \
      -C "$package_work" -cf - "$package_name" | gzip -n > "$package_work/bundle.tar.gz"
    mv -fT -- "$package_work/bundle.tar.gz" "$archive"
  fi
  (cd "$package_out" && sha256sum "$(basename "$archive")") > "$package_work/archive.sha256"
  package_output_file "$archive.sha256"
  mv -fT -- "$package_work/archive.sha256" "$archive.sha256"
  printf '%s\n' "$archive" "$archive.sha256"
}
