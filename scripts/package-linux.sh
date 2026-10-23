#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

bash scripts/build.sh linux
for tool in jq tar gzip sha256sum ldd; do
  command -v "$tool" >/dev/null
done
source scripts/package-common.sh
package_init linux
cp "$package_out/aircast-linux-amd64" "$package_stage/aircast"
cp "$package_out/aircastctl-linux-amd64" "$package_stage/aircastctl"
cp "$package_out/aircast-engine-linux-amd64" "$package_stage/aircast-engine"
package_sources
cat > "$package_stage/README.md" <<'EOF'
# Aircast Linux casting test bundle

This archive is not a self-contained portable Linux application. It contains
the GUI, CLI, sender engine, notices and modified source. The host must supply
the shared libraries in `LINUX-SHARED-LIBRARIES.txt`, OpenGL drivers, GStreamer
and the capture/encoder plugins described in `docs/BUILDING.md`.

Keep `aircast-engine` beside `aircast`, install the runtime dependencies, then
run `./aircast` from a graphical desktop session. Start with one SDR monitor
and a receiver-supported H.264 mode. A synthetic Xvfb test does not validate a
physical desktop, a GPU or Apple TV output.

The engine is LGPLv3. Optional host GStreamer plugins may be GPL or have other
licenses. They are not included in this archive. Read `SOURCE-AND-RELINK.md`.
Aircast's own release license is not yet chosen. Do not publish this private
test bundle as a licensed release.
EOF
package_metadata
package_archive tar.gz
