# Building and packaging

Compile on a Linux build machine with CGO and MinGW for the Windows targets.
Keep checkouts, downloads and outputs in a dedicated working directory rather
than a shared one. No hosting service is needed for builds or runtime casting.

## Toolchain

Use Go 1.25 or newer, Bash, GNU tar/coreutils, gzip, jq, curl, unzip, zip and
binutils. `GOTOOLCHAIN=local` prevents an implicit compiler upgrade. The GUI
uses CGO. The Windows sender also requires CGO and MinGW for native DXGI
capture and WASAPI audio; do not build its no-CGO stubs for delivery. The CLI
and Linux sender build without CGO. Go x86-64 code targets `GOAMD64=v1`
rather than assuming AVX support. Do not enable the optional `fdk_aac` build
tag; the nonfree FDK AAC dependency is not part of this bundle.

On Ubuntu 24.04, the build packages are:

```sh
sudo apt-get install build-essential pkg-config libgl1-mesa-dev xorg-dev \
  libxkbcommon-dev libwayland-dev gcc-mingw-w64-x86-64 \
  binutils-mingw-w64-x86-64 curl unzip zip jq
```

Copy the current source snapshot to `~/package-worker/source`, preserving the
nested module in `third_party/doubletake`. Both module roots need their own
`go.mod` and `go.sum`. Do not copy `.git`, `dist`, `.build` or local secrets.

```sh
cd ~/package-worker/source
go mod download
(cd third_party/doubletake && go mod download)
bash scripts/build.sh windows
bash scripts/build.sh linux
```

`bash scripts/build.sh` builds both targets. It refuses to create a
discovery-only build when the `cmd/aircast-engine` source is missing.
`WINDOWS_CC` defaults to `CC` if set, otherwise
`x86_64-w64-mingw32-gcc`. Use `LINUX_CC` for the native compiler, default `gcc`.
Windows cross-compilation does not require or prove a Windows runtime.

Outputs include:

```text
dist/aircast-windows-amd64.exe
dist/aircast-engine-windows-amd64.exe
dist/aircastctl-windows-amd64.exe
dist/aircast-linux-amd64
dist/aircast-engine-linux-amd64
dist/aircastctl-linux-amd64
dist/SHA256SUMS-windows
dist/SHA256SUMS-linux
```

These raw executables are not the delivery bundle. In particular the raw GUI
has a target-qualified filename while runtime helpers have adjacent plain
filenames in the package. Use the package scripts to assemble those names.

## Tests before packaging

Run from the copied checkout on the approved VM:

```sh
go test -race ./...
go vet ./...
(cd third_party/doubletake && go test -race ./...)
(cd third_party/doubletake && go vet ./...)
bash -n scripts/build.sh scripts/package-common.sh scripts/package-windows.sh scripts/package-linux.sh
shellcheck scripts/build.sh scripts/package-common.sh scripts/package-windows.sh scripts/package-linux.sh
```

Some sender integration tests need GStreamer plugins and a graphical session.
Record skipped tests and unmet dependencies. Do not count a skipped portal,
synthetic receiver or source inspection as Windows or Apple TV validation.

## Casting packages

```sh
bash scripts/package-windows.sh
bash scripts/package-linux.sh
```

Each package script rebuilds its target, so it cannot silently pick up an old
discovery-only executable. Both include the GUI, `aircastctl`, adjacent
`aircast-engine`, a source snapshot, locked Go module source archives, notices,
build information and internal SHA256SUMS. Windows adds the pinned FFmpeg
executable and its publisher materials. It does not include ffplay or ffprobe.

The offline Go proxy includes selected module sources plus historical manifests
visited during graph loading. Packaging checks both module roots with empty
module caches and a file-only proxy before archiving. These temporary caches use
`-modcacherw` so the normal build user can remove them. Output publication rejects
symlinks and non-regular destinations, then atomically replaces files.

Windows capture is native C/CGO DXGI of the primary SDR landscape monitor,
with a GDI compatibility fallback. CPU-readback BGRA goes to bundled FFmpeg's
rawvideo input, `libzimg`/`zscale`/`lutrgb` conversion and software `libx264`
H.264 output in streaming AVI packet envelopes. The bundle checks for AVI
muxing support too. Windows does not need GStreamer or a `DISPLAY` variable. HDR,
unknown color state and rotated outputs are rejected. The initial ceiling is
receiver-approved 1080p30 SDR H.264, not 4K or HDR support. WASAPI audio is
experimental and off by default; video and image quality are the first gate.

```text
dist/aircast-windows-amd64-casting.zip
dist/aircast-windows-amd64-casting.zip.sha256
dist/aircast-linux-amd64-casting.tar.gz
dist/aircast-linux-amd64-casting.tar.gz.sha256
```

`AIRCAST_DIST_DIR` changes the output directory. `GOMAXPROCS` defaults to 2 to
avoid competing with other build workers. `SOURCE_DATE_EPOCH` defaults to
`315532800`, 1980-01-01 UTC, so archive timestamps do not depend on build time.
Set it to a fixed value from 1980 onward for a specific delivery. The scripts
use `-trimpath -buildvcs=false -mod=readonly` and strip Go debug symbols.
They preserve source and dependency hashes; they do not promise byte-identical
binaries across different Go, MinGW, operating-system or system-library versions.

The FFmpeg ZIP caches under `.build/downloads` on the build VM. Every run
checks its hard-coded SHA256. There is no moving `latest` download or runtime
codec installer. See `licenses/FFMPEG-WINDOWS.md` for provenance, GPLv3 terms,
native filter inventory, CPU limits and the incomplete redistribution audit.

Copy completed archives back to the parent's local `dist` directory only after
the engine and capture workers pass their integration gates:

```sh
scp build-host:aircast/dist/aircast-windows-amd64-casting.zip dist/
scp build-host:aircast/dist/aircast-windows-amd64-casting.zip.sha256 dist/
```

Publish only after the engine and capture integration gates pass, and record
what actually ran. A cross-build is not a Windows runtime test.

## Linux runtime dependencies

The Linux tarball is not an AppImage and is not portable to arbitrary Linux
distributions. The GUI dynamically links the system C/OpenGL/X11 libraries.
`LINUX-SHARED-LIBRARIES.txt` records `ldd` on the build host, not guaranteed
library versions on the user's machine. Use a compatible x86-64 distribution
and install OpenGL drivers and a desktop session. A StatusNotifier tray host
is optional; GNOME may need an AppIndicator extension.

The Linux sender invokes host GStreamer tools and plugins. On Ubuntu 24.04,
start with:

```sh
sudo apt-get install gstreamer1.0-tools gstreamer1.0-plugins-base \
  gstreamer1.0-plugins-good gstreamer1.0-plugins-bad \
  gstreamer1.0-plugins-ugly gstreamer1.0-libav
```

Inspect the actual selected path rather than assuming every package installed
every encoder:

```sh
gst-inspect-1.0 ximagesrc
gst-inspect-1.0 videoconvert
gst-inspect-1.0 videoscale
gst-inspect-1.0 h264parse
gst-inspect-1.0 rtph264pay
gst-inspect-1.0 rtponviftimestamp
gst-inspect-1.0 rtpstreampay
gst-inspect-1.0 fdsink
gst-inspect-1.0 openh264enc
gst-inspect-1.0 x264enc
```

At least one encoder supported by the selected backend must exist and must
pass an actual encode test. `x264enc` is a GPL plugin. Its installation does
not make this archive a self-contained LGPL distribution.

Wayland additionally requires PipeWire, a functioning user D-Bus session,
`xdg-desktop-portal`, a compositor-specific screencast portal backend, and the
`pipewiresrc` plugin, commonly supplied by `gstreamer1.0-pipewire`. Permission
must come from the portal. Do not bypass it or label an X11 VM test a Wayland
test. Optional hardware encoders require compatible plugins, GPU drivers and
permissions. Audio is tested only when the engine explicitly supports and
enables system audio on that platform.

## Source and replacement

Read `licenses/SOURCE-AND-RELINK.md`. The separate LGPL sender is replaceable
without rebuilding the GUI. The package includes its modified source and a
local Go module mirror. The Windows FFmpeg static GPL executable has separate
source obligations. Aircast's own license and the full native dependency
source/notice audit remain gates before any external release.
