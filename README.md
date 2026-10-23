# Aircast

Send your Windows or Linux desktop to an AirPlay receiver over your local
network. A Go + C/CGO tray application with native capture, an AirPlay sender,
and no account, cloud relay or telemetry.

**v0.1.0 public beta.** Windows 10/11 x64 and Linux X11. Independent project,
GPL-3.0.

- Website and downloads: <https://4nkitd.github.io/aircast>
- Releases: <https://github.com/4nkitd/aircast/releases>
- Roadmap and quality gates: [ROADMAP.md](ROADMAP.md)
- What was actually tested: [VERIFICATION.md](VERIFICATION.md)

## Install

### Windows 10/11 x64

Run `aircast-0.1.0-windows-amd64-setup.exe`. It installs for the current user
under `%LOCALAPPDATA%\Programs\Aircast`, needs no administrator rights, adds a
Start menu entry and an uninstaller, and includes the FFmpeg build the engine
uses. A portable zip is published beside the installer; keep `ffmpeg.exe` next
to `aircast-engine.exe`.

### Linux X11

```sh
tar -xzf aircast-0.1.0-linux-amd64.tar.gz
cd aircast-0.1.0-linux-amd64
./install.sh
```

`install.sh` copies the binaries to `~/.local/opt/aircast`, links `aircast` and
`aircastctl` into `~/.local/bin`, and adds a desktop entry. Override the prefix
with `PREFIX=...`. Runtime needs the GStreamer plugins listed in
[docs/BUILDING.md](docs/BUILDING.md). Only X11 capture was exercised; the
Wayland portal path is untested. GNOME needs a StatusNotifier/AppIndicator
extension for the tray.

Verify archives against the release's `SHA256SUMS`.

## Beta capabilities

- AirPlay screen sharing with PIN/password pairing, Start/Stop and reconnect.
- Native Windows primary-monitor SDR capture through DXGI, with a visible GDI
  compatibility notice when fallback is needed. Cursor sharing is required.
- Receiver-approved H.264, capped at 1080p30 on Windows, with quality-first and
  balanced profiles. No 4K, HDR or lossless-text claim.
- Linux X11 desktop capture and a Wayland portal path. Only X11 was exercised.
- Optional system audio, off by default. Windows WASAPI remains experimental
  and runtime-unverified.

Google Cast sending, audio-only receivers, virtual displays, file playlists and
multiple simultaneous receivers remain roadmap work, not beta capabilities.

## Receiver discovery

- Finds AirPlay, AirPlay audio, and Google Cast receivers using actual mDNS.
- Refreshes every 25 seconds and expires observations after 70 seconds.
- Combines AirPlay video and audio advertisements when their device IDs match.
- Adds receivers by IP address or hostname, with optional port.
- Saves receiver addresses locally and keeps saved devices when they disappear.
- Checks AirPlay `/info`, including binary/XML property lists and auth responses.
- Checks TCP reachability for Cast and audio endpoints without claiming pairing.
- Shows a compact desktop panel and a tray menu where a tray host is present.

Checking a receiver does not initiate pairing, start a TV app, or capture a
screen. No account, telemetry, cloud service, or remote-control listener exists.
Saved addresses are not "recent successful connections".

## Run

Extract the bundle and keep the engine beside the panel. Start with audio off,
the quality profile, and a non-sensitive SDR desktop. Follow
[docs/WINDOWS-TESTING.md](docs/WINDOWS-TESTING.md) for a Windows test pass.

Close hides the panel only when a tray host was detected at startup. Use the
tray's Show Aircast action to reopen it, and Quit Aircast to exit. Without a
Linux StatusNotifier host, closing quits rather than leaving an invisible app.

Diagnostics also work without a graphical desktop:

```sh
aircastctl discover -timeout 4s
aircastctl probe 192.168.1.40:7000
aircastctl probe -kind cast 192.168.1.41:8009
```

Auto-discovery sends mDNS queries over IPv4 on up to eight active multicast
interfaces. Global IPv6 records are supported. Unscoped link-local AAAA-only
announcements are not assigned a guessed network interface; add those manually
with an explicit zone. IPv6-only discovery is not supported yet. Multicast
filtering, VPNs, isolated Wi-Fi clients, and routed subnets can prevent
discovery. Manual entry works if the computer can route to the receiver.
`.local` hostname resolution depends on the operating system's resolver.

## Build and test

Development requires Go 1.25 or newer, CGO and a C toolchain. On Ubuntu 24.04:

```sh
sudo apt-get install build-essential pkg-config libgl1-mesa-dev xorg-dev \
  libxkbcommon-dev libwayland-dev gcc-mingw-w64-x86-64
go mod download
go test -race ./...
go vet ./...
bash scripts/build.sh
```

The panel uses Fyne's C/CGO OpenGL integration, not a web wrapper. Casting uses
the separately built LGPL sender in `third_party/doubletake`; keep its source
available and the executable replaceable when redistributing. See
[docs/BUILDING.md](docs/BUILDING.md) for sender tests, release packaging and
the complete Windows/Linux build commands.

For isolated GUI verification, run the synthetic mDNS/HTTP fixture in a
disposable Linux VM, then open Aircast in the same VM:

```sh
go run ./cmd/test-receiver
```

The fixture advertises `Aircast test receiver` and binds its information server
to loopback only. It does not emulate pairing, decoding, or streaming. Never
count a fixture pass as Apple TV hardware compatibility.

Saved addresses live in `aircast/receivers.json` under the OS user
configuration directory. Invalid existing configuration disables saving rather
than replacing the file. No PIN, private key, or other credential is stored.

## Release packaging

`scripts/package-release.sh` turns the verified full bundles in `dist/` into the
runtime-only release assets and the NSIS installer, then writes `SHA256SUMS`:

```sh
bash scripts/package-release.sh
```

It requires `makensis` (NSIS) and the full bundles produced by
`scripts/package-windows.sh` and `scripts/package-linux.sh`.

## Verification

See [VERIFICATION.md](VERIFICATION.md) for what actually ran. A Windows
cross-build is not a Windows runtime test. An X11 VM test is not a Wayland
capture test. Synthetic receiver transport tests do not verify a physical TV.

## License

Aircast is licensed under the GNU General Public License, version 3. See
[LICENSE](LICENSE).

Third-party components keep their own licenses. `ffmpeg.exe` is an unmodified
Gyan GPLv3 build, shipped as a separate executable; [licenses/FFMPEG-WINDOWS.md](licenses/FFMPEG-WINDOWS.md)
records provenance and the source offer. The AirPlay sender derives from
DoubleTake (LGPL-3.0) and remains separately replaceable; source and build
instructions ship in `third_party/doubletake` and
[licenses/SOURCE-AND-RELINK.md](licenses/SOURCE-AND-RELINK.md). See
[THIRD-PARTY.md](THIRD-PARTY.md) for the full list.

AirPlay, Apple TV, Google Cast and AirParrot are trademarks of their owners.
Aircast is not affiliated with Apple, Google or Squirrels.
