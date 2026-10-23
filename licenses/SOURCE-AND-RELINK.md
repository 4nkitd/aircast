# Source, replacement and license availability

This is a private evaluation bundle. Aircast's own release license has not
been chosen. Do not interpret the presence of dependency licenses as a license
grant for all Aircast code, or publish this bundle before the release review.

## DoubleTake sender

`aircast-engine` is a separate process built from the modified DoubleTake
source under `third_party/doubletake`. Upstream is
<https://github.com/omarroth/doubletake>, pinned at
`5e2f1cbe3e1070bcefec8e549c7baf884727bc00` before Aircast's changes.
The engine uses LGPL-3.0. Its LGPL and accompanying GPL text are in
`licenses/doubletake/`. Source files retain upstream copyright notices.

`source/aircast-source.tar.gz` contains the exact local source snapshot used
for the GUI, CLI and modified engine, including build scripts and module lock
files. It is a snapshot, not a claim that these changes are upstream. Go module
source ZIPs, metadata and go.mod files are in `source/go-proxy/`. Their notices
are in `licenses/go-modules/`. `BUILD-INFO.txt` records compiler versions and
Go build settings. The Go runtime license is in `licenses/go-runtime/`.

To rebuild the engine, extract the source snapshot on the approved Linux VM,
then run:

```sh
cd third_party/doubletake
GOTOOLCHAIN=local GOOS=windows GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=1 \
  CC=x86_64-w64-mingw32-gcc \
  go build -mod=readonly -buildvcs=false -trimpath -ldflags='-s -w' \
  -o aircast-engine.exe ./cmd/aircast-engine
```

Windows needs CGO for the native DXGI capture and WASAPI paths. A no-CGO
Windows helper can compile but cannot capture, so it is not a valid delivery.
For Linux use `GOOS=linux CGO_ENABLED=0` and omit `.exe` and `CC`. Do not enable
the optional nonfree `fdk_aac` build tag. To use the included module mirror,
set `GOPROXY=file:///absolute/path/to/extracted-bundle/source/go-proxy` before
building. Existing `go.sum` files still apply. An offline machine must already
have the needed Go toolchain and checksum-database records, or use `GOSUMDB=off`
for this offline mirror after checking the bundle's SHA256SUMS. That switch
does not disable checks against the supplied `go.sum` files.

Quit Aircast and replace only the adjacent `aircast-engine.exe` or
`aircast-engine` with a compatible modified build. The GUI communicates through
the JSON protocol documented in the source tree's `docs/ENGINE-PROTOCOL.md`.
There is no signature lock or installer restriction on replacing that helper.
Reverse engineering for debugging modifications to LGPL components is not
prohibited by this test bundle. The separate-process boundary means rebuilding
the GUI is not required to replace the engine. This is not a claim that a
statically linked LGPL library can be replaced without rebuilding its process.

## Windows FFmpeg

`ffmpeg.exe` is an unmodified, separately launched GPLv3 executable from Gyan.
It is not an LGPL-only build. `licenses/ffmpeg/` preserves its publisher README,
GPL text, relevant documentation, source revision, dependency version list and
checksum provenance. `licenses/FFMPEG-WINDOWS.md` explains the native dependency
and source-availability limits. The Go source snapshot is not FFmpeg's complete
corresponding source.

Upstream FFmpeg source is at <https://github.com/FFmpeg/FFmpeg/commit/946fcce07b>.
The publisher does not put every external library's source, patch set and
build recipe in the binary ZIP. Merely linking that upstream repository does
not fulfill a complete corresponding-source audit for a static GPL binary.
Before any external redistribution, obtain and preserve that complete source,
all dependency license texts, build scripts and any relevant installation
information, or replace FFmpeg with a build whose complete source is available.
This evaluation package does not claim that release gate is complete.

You can replace the adjacent FFmpeg executable for debugging, subject to its
license and the engine's required filters and command-line compatibility.
Never download or substitute a codec binary during an active cast.

## Linux native media dependencies

The Linux archive does not redistribute GStreamer, its plugins, system shared
libraries or GPU drivers. Install them through the target distribution and
retain their notices if repackaging them. A plugin's presence is not proof that
its device, portal or encoder works. See `docs/BUILDING.md` for package names
and runtime inspection commands.
