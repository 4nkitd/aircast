#!/usr/bin/env bash
# Build the public release archives from the verified full bundles.
set -euo pipefail

version=${VERSION:-0.1.0}
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
win_src="$root/dist/aircast-windows-amd64-casting.zip"
linux_src="$root/dist/aircast-linux-amd64-casting.tar.gz"
work="$root/.build/release"
win_name="aircast-$version-windows-amd64"
linux_name="aircast-$version-linux-amd64"

for input in "$win_src" "$linux_src" "$root/site/assets/icon-512.png"; do
  [ -f "$input" ] || { echo "Missing $input" >&2; exit 1; }
done

rm -rf "$work"
mkdir -p "$work/windows" "$work/linux" "$root/installer/payload"

echo "== unpacking runtime files"
unzip -q "$win_src" -x '*/source/*' -d "$work/windows"
mv "$work/windows/aircast-windows-amd64-casting" "$work/windows/$win_name"
cp "$root/LICENSE" "$work/windows/$win_name/LICENSE"

tar -xzf "$linux_src" -C "$work/linux" --exclude '*/source' --exclude '*/source/*'
mv "$work/linux/aircast-linux-amd64-casting" "$work/linux/$linux_name"
cp "$root/LICENSE" "$work/linux/$linux_name/LICENSE"
cp "$root/installer/install.sh" "$work/linux/$linux_name/install.sh"
chmod 755 "$work/linux/$linux_name/install.sh"

# 256 px icon for the Linux desktop entry.
sips -z 256 256 "$root/site/assets/icon-512.png" \
  --out "$work/linux/$linux_name/aircast-icon.png" >/dev/null

echo "== windows portable zip"
(cd "$work/windows" && zip -qr9 "$root/dist/$win_name.zip" "$win_name")

echo "== linux tarball"
tar -czf "$root/dist/$linux_name.tar.gz" -C "$work/linux" "$linux_name"

echo "== NSIS payload"
cp -R "$work/windows/$win_name/." "$root/installer/payload/"

echo "== windows setup"
(cd "$root/installer" && makensis -V2 aircast.nsi)
rm -rf "$root/installer/payload"

echo "== checksums"
(cd "$root/dist" && shasum -a 256 \
  "$win_name.zip" \
  "$linux_name.tar.gz" \
  "aircast-$version-windows-amd64-setup.exe" > SHA256SUMS)

cat "$root/dist/SHA256SUMS"
