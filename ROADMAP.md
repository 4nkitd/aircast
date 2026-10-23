# Aircast

An independently branded, Go + C/CGO tray application for Windows and Linux.
The target is AirParrot's Windows feature set, not a receiver application.

Each milestone ends with tests, code review and artifacts. Do not start the
next milestone without sign-off.

Milestones 1 and 2 were combined. A discovery-only Windows build is not a
deliverable: add AirPlay casting, then test on a local Windows machine. Image
quality is a release criterion. Builds and tests run on disposable Linux VMs.

## 1. Tray and receiver discovery

- [ ] Styled desktop panel and system tray. Closing the panel keeps the app running.
- [ ] Real AirPlay, AirPlay audio, and Google Cast discovery.
- [ ] Manual receiver entry, saved receivers, expiration of discovery results.
- [ ] Explicit receiver diagnostics without starting playback or pairing.
- [ ] Linux runtime/UI tests and a Windows cross-build.
- [x] Initial unit, race, and UI tests plus Linux and Windows CGO builds.
- [ ] Final integrated review and verification report.

This milestone does not send a screen, audio, or media. A reachable receiver
must never be labelled as a streaming connection.

## 2. AirPlay screen and system audio

- [ ] Evaluate and pin the LGPL-3.0 DoubleTake sender as reusable protocol code.
- [ ] Document license obligations before incorporating or distributing it.
- [ ] C/CGO capture and encoding integration. Prefer GStreamer over new codecs.
- [ ] Linux X11 and Wayland portal capture. Respect the system consent picker.
- [ ] Windows capture and WASAPI loopback through platform-specific native code.
- [ ] PIN pairing, reconnect, and OS credential-store integration.
- [ ] Start/stop, bounded queues, cancellation, error reporting, and cleanup.
- [ ] Hardware test against the maintainer's exact Apple TV/AirPlay TV model and firmware.

The protocol candidate is Linux-specific today. Its Windows capture, process,
clock, and audio assumptions need porting, not just a GOOS setting.

## 3. AirPlay source and playback controls

- [ ] Individual windows, monitor selection, cursor settings, and quality controls.
- [ ] Pause/resume feeds and audio-only output.
- [ ] Media files, playlists, playback controls, format negotiation/transcoding.
- [ ] 5.1 passthrough only when the source, transport, and receiver support it.
- [ ] Multiple simultaneous receivers with bounded shared capture/encoding.
- [ ] Recent successful streaming connections, distinct from saved receivers.

## Image-quality gate for the first casting build

- [x] Research actual AirPlay codec/canvas limits, Windows capture, and encoding.
- [ ] Quality-first H.264 SDR preset with explicit color conversion and correct
  encoder buffer units. Do not inherit the low automatic text bitrate unchanged.
- [ ] Physical-pixel capture, one aspect-preserving resize, and bounded queues.
- [ ] Native Windows DXGI/WGC capture or an explicitly labelled compatibility path.
- [ ] Decoded text, color, grayscale, edge, and geometry fixtures with saved results.
- [ ] Show the actual negotiated/encoded canvas, not the TV's panel resolution.
- [ ] the maintainer's Windows/physical receiver test. Cross-compilation cannot satisfy it.

Start with receiver-supported H.264 SDR, normally up to 1080p30. Higher bitrate
does not reverse 4:2:0 text/chroma loss. HEVC, HDR, 4K, and 60 distinct frames per
second need separate capability checks and receiver evidence. Research proposals
are in `docs/research/`; they are not benchmark or hardware-test results.

## 4. Google Cast

- [ ] Authenticated Cast control and media playback.
- [ ] Screen, window, and system-audio streaming through a suitable receiver path.
- [ ] Real Chromecast/Google TV testing. Discovery is not streaming support.
- [ ] Mixed AirPlay/Cast sessions and per-receiver failures.

## 5. Extended desktop

- [ ] Windows indirect-display driver, installer, signing, and uninstall recovery.
- [ ] Linux virtual outputs with an explicit compositor support matrix.
- [ ] Mirror/extend switching without changing the physical monitor's refresh rate.
- [ ] Remove virtual outputs and restore desktop state after errors and exit.

Wayland does not have one universal virtual-monitor API. Windows driver
distribution also needs signing arrangements. Neither is a tray-library feature.

## 6. Remote control and distribution

- [ ] Our own paired phone remote, preview, playlists, keyboard/mouse consent.
- [ ] Our own direct-address/pairing-code connection flow.
- [ ] Evaluate Bluetooth discovery against actual receiver behavior.
- [ ] Windows installer and Linux packages with native dependencies documented.
- [ ] Autostart opt-in, deployment options, accessibility, and soak tests.

Squirrels' patented Quick Connect service and AirParrot Remote iOS application
are not assumed interoperable. Use independently implemented equivalents.
Protected media is not a DRM-bypass target.

## Target systems

- Windows 10/11 x86-64 initially. Windows ARM needs a separate build/test decision.
- Linux x86-64 initially, X11 and Wayland. Test Ubuntu and a KDE/Wayland system.
- GNOME needs a StatusNotifier/AppIndicator extension for tray icons. The normal
  application window must remain usable when a tray host is unavailable.
- Apple TV/AirPlay TV is the first hardware target. Exact model is still needed.

## Reference

Feature baseline checked on 2026-10-20:

- https://www.airsquirrels.com/airparrot/features/win
- https://www.airsquirrels.com/airparrot/specs
- https://www.airsquirrels.com/airparrot/remote
- Protocol candidate: https://github.com/omarroth/doubletake
  at `5e2f1cbe3e1070bcefec8e549c7baf884727bc00`.

Build and test on disposable Linux VMs, separate from any machine you do not
own.
