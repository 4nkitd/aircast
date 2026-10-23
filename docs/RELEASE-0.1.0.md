# Aircast v0.1.0 — public beta

Aircast sends a Windows or Linux desktop to an AirPlay receiver over the local
network. No account, cloud relay, telemetry or remote-control listener.

## Downloads

| Platform | File | Notes |
| --- | --- | --- |
| Windows 10/11 x64 | `aircast-0.1.0-windows-amd64-setup.exe` | Per-user install, no admin rights, Start menu entry and uninstaller |
| Windows 10/11 x64 | `aircast-0.1.0-windows-amd64.zip` | Portable; keep `ffmpeg.exe` beside `aircast-engine.exe` |
| Linux X11 | `aircast-0.1.0-linux-amd64.tar.gz` | Run `install.sh` inside; installs under `~/.local` |

`SHA256SUMS` covers all three archives. `ffmpeg-9.0.2.tar.xz` is the upstream
source for the GPLv3 FFmpeg build shipped in the Windows packages.

## What works

- AirPlay screen sharing with PIN/password pairing, Start/Stop and reconnect.
- Receiver discovery through real mDNS, saved addresses and manual entry.
- Windows primary-monitor SDR capture through DXGI with a visible GDI fallback.
- Quality-first H.264, capped at 1080p30 on Windows.
- Linux X11 capture; the Wayland portal path exists but was not exercised.
- Optional system audio, off by default. Windows WASAPI stays experimental.

## Known limits

- No Google Cast sending, audio-only receivers, file playlists, virtual
  displays or multiple simultaneous receivers.
- No 4K, HDR or lossless-text claim.
- Windows on ARM, Windows 7/8 and headless service sessions are unsupported.
- Multicast filtering, VPNs, isolated Wi-Fi clients and routed subnets can block
  discovery; add the receiver by address instead.
- Windows system audio is not runtime-verified. GNOME needs a
  StatusNotifier/AppIndicator extension for the tray.

## License and source

Aircast is GPL-3.0. The full source for this release is the `v0.1.0` tag of
<https://github.com/4nkitd/aircast>.

`ffmpeg.exe` is an unmodified Gyan `9.0.2-essentials_build` static GPLv3 binary,
shipped as a separate executable. Its provenance is recorded in
`licenses/FFMPEG-WINDOWS.md`, and the upstream source is attached here.

The AirPlay sender derives from DoubleTake (LGPL-3.0). Its source and relink
instructions ship inside the packages (`licenses/doubletake`,
`licenses/SOURCE-AND-RELINK.md`) so the sender executable remains replaceable.

AirPlay, Apple TV, Google Cast and AirParrot are trademarks of their owners.
Aircast is not affiliated with Apple, Google or Squirrels.
