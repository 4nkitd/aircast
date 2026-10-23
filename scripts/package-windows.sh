#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

bash scripts/build.sh windows
for tool in curl unzip zip jq tar gzip sha256sum objdump; do
  command -v "$tool" >/dev/null
done
source scripts/package-common.sh
package_init windows

ffmpeg_version=9.0.2
ffmpeg_name="ffmpeg-$ffmpeg_version-essentials_build"
ffmpeg_archive="$ffmpeg_name.zip"
ffmpeg_url="https://github.com/GyanD/codexffmpeg/releases/download/$ffmpeg_version/$ffmpeg_archive"
ffmpeg_hash=60f467265b1e312373dbcd92200c2618a74850f98d3d078e94296bb3fa2047ba
ffmpeg_exe_hash=3256173f3f8bffd7df12227c68adf68025edb1832273a9530688a7bb1ed8edec
cache="$PWD/.build/downloads"
mkdir -p "$cache"
if [[ ! -f "$cache/$ffmpeg_archive" ]]; then
  curl --fail --location --retry 3 --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --output "$package_work/$ffmpeg_archive" "$ffmpeg_url"
  (cd "$package_work" && printf '%s  %s\n' "$ffmpeg_hash" "$ffmpeg_archive" | sha256sum --check --status)
  mv "$package_work/$ffmpeg_archive" "$cache/$ffmpeg_archive"
fi
(cd "$cache" && printf '%s  %s\n' "$ffmpeg_hash" "$ffmpeg_archive" | sha256sum --check --status)

cp "$package_out/aircast-windows-amd64.exe" "$package_stage/aircast.exe"
cp "$package_out/aircastctl-windows-amd64.exe" "$package_stage/aircastctl.exe"
cp "$package_out/aircast-engine-windows-amd64.exe" "$package_stage/aircast-engine.exe"
unzip -p "$cache/$ffmpeg_archive" "$ffmpeg_name/bin/ffmpeg.exe" > "$package_stage/ffmpeg.exe"
(cd "$package_stage" && printf '%s  ffmpeg.exe\n' "$ffmpeg_exe_hash" | sha256sum --check --status)
package_sources
mkdir -p "$package_stage/licenses/ffmpeg"
unzip -p "$cache/$ffmpeg_archive" "$ffmpeg_name/LICENSE" > "$package_stage/licenses/ffmpeg/LICENSE"
unzip -p "$cache/$ffmpeg_archive" "$ffmpeg_name/README.txt" > "$package_stage/licenses/ffmpeg/README.txt"
unzip -p "$cache/$ffmpeg_archive" "$ffmpeg_name/doc/ffmpeg-filters.html" \
  > "$package_stage/licenses/ffmpeg/ffmpeg-filters.html"
unzip -p "$cache/$ffmpeg_archive" "$ffmpeg_name/doc/ffmpeg-codecs.html" \
  > "$package_stage/licenses/ffmpeg/ffmpeg-codecs.html"
ffmpeg_require_component() {
  local section="$1" component="$2"
  if ! awk -v section="$section" '
    /^Enabled / { active = ($0 == "Enabled " section ":"); next }
    active { print }
  ' "$package_stage/licenses/ffmpeg/README.txt" | grep -w "$component" >/dev/null; then
    printf 'Pinned FFmpeg publisher manifest lacks %s in %s.\n' "$component" "$section" >&2
    exit 1
  fi
}
for component in ddagrab gfxcapture hwdownload zscale lutrgb scale_d3d11; do
  ffmpeg_require_component filters "$component"
done
for component in libx264 h264_nvenc h264_qsv h264_amf; do
  ffmpeg_require_component encoders "$component"
done
ffmpeg_require_component demuxers rawvideo
ffmpeg_require_component muxers h264
ffmpeg_require_component muxers avi
cat > "$package_stage/licenses/ffmpeg/PROVENANCE.txt" <<EOF
Publisher: Gyan Doshi, https://www.gyan.dev/ffmpeg/builds/
Release: $ffmpeg_version essentials, static Windows x86-64, GPLv3
Download: $ffmpeg_url
Publisher checksum: https://www.gyan.dev/ffmpeg/builds/packages/$ffmpeg_archive.sha256
Publisher SHA256 retrieved 2026-10-20: $ffmpeg_hash
Downloaded ZIP SHA256 matched that publisher value on the approved Linux VM.
This is a same-publisher HTTPS checksum comparison, not an independent signature or reproducible build audit.
Extracted ffmpeg.exe SHA256 first calculated 2026-10-20: $ffmpeg_exe_hash
The executable hash is a locally calculated pin, not a separately published publisher hash.
Source revision: https://github.com/FFmpeg/FFmpeg/commit/946fcce07b
Components listed in publisher README: ddagrab, gfxcapture, hwdownload,
zscale, lutrgb, scale_d3d11, libx264, h264_nvenc, h264_qsv, h264_amf,
rawvideo input, H.264 output and streaming AVI packet envelopes.
This manifest inspection does not establish that a Windows filter or GPU works.
Windows runtime, capture, receiver playback, AVX-free operation: NOT TESTED.
EOF
cat > "$package_stage/README.md" <<'EOF'
# Aircast Windows casting test bundle

Extract the whole ZIP, then run `aircast.exe` in this folder. Keep
`aircast-engine.exe` and `ffmpeg.exe` beside it. No separate FFmpeg installation
is required. Do not run directly inside the ZIP.

Use a Windows 10/11 x86-64 machine with an interactive SDR desktop and OpenGL
drivers. See `docs/WINDOWS-TESTING.md` for receiver, PIN, firewall and quality
checks. The Windows backend captures the primary landscape SDR monitor using
native C/DXGI with CPU readback, then software x264 encoding in FFmpeg. It can
use a GDI compatibility fallback. It does not require GStreamer or DISPLAY.
Start with the quality profile, up to receiver-approved 1080p30 SDR H.264.
System audio is experimental and off by default; test video quality first.

This is a private casting test build, not a verified Apple TV compatibility
release. Cross-compilation is not Windows runtime validation. A sender's
"streaming" status does not prove that a TV decoded or displayed its pictures.

The bundled FFmpeg is GPLv3. The modified DoubleTake engine is LGPLv3.
Read `SOURCE-AND-RELINK.md` and `licenses/FFMPEG-WINDOWS.md`. Aircast's own
release license and the complete FFmpeg dependency source audit are release
gates. Do not publish or forward this private test bundle as a licensed release.
EOF
package_metadata
package_archive zip
