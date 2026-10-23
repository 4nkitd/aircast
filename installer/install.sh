#!/bin/sh
# Install Aircast for the current user under ~/.local (override with PREFIX).
set -eu

src=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
prefix=${PREFIX:-"$HOME/.local"}
opt="$prefix/opt/aircast"
bin="$prefix/bin"
applications="$prefix/share/applications"
icons="$prefix/share/icons/hicolor/256x256/apps"

for file in aircast aircast-engine aircastctl; do
  if [ ! -f "$src/$file" ]; then
    echo "Missing $file beside install.sh" >&2
    exit 1
  fi
done

mkdir -p "$opt" "$bin" "$applications" "$icons"
cp -f "$src/aircast" "$src/aircast-engine" "$src/aircastctl" "$opt/"
chmod 755 "$opt/aircast" "$opt/aircast-engine" "$opt/aircastctl"

ln -sfn "$opt/aircast" "$bin/aircast"
ln -sfn "$opt/aircastctl" "$bin/aircastctl"

if [ -f "$src/aircast-icon.png" ]; then
  cp -f "$src/aircast-icon.png" "$icons/aircast.png"
fi

cat >"$applications/aircast.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=Aircast
Comment=Share your screen to an AirPlay receiver
Exec=$opt/aircast
Icon=aircast
Terminal=false
Categories=Network;AudioVideo;
EOF

echo "Installed Aircast to $opt"
echo "Run it with: $bin/aircast"

if ! command -v gst-launch-1.0 >/dev/null 2>&1; then
  echo
  echo "GStreamer is not on PATH. AirPlay casting needs gst-launch-1.0 and the"
  echo "base, good, bad, ugly and libav plugin sets. See docs/BUILDING.md."
fi

case ":$PATH:" in
  *":$bin:"*) ;;
  *) echo "Add $bin to PATH to run aircast by name." ;;
esac
