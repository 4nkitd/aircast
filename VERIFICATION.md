# Casting beta verification

The casting-enabled beta is published for public testing. Live transport,
native Linux GUI, post-review builds and package checks passed. No physical
receiver or Windows image-quality claim follows from Linux tests.

Build verification ran on two disposable Ubuntu 24.04 x86-64 VMs (2 vCPUs,
4 GiB RAM, 40 GiB disk), separate from existing machines.

## Completed evidence

- The initial discovery-only root module passed all unit, race, and Fyne UI
  interaction tests on Linux. Linux release and Windows CGO cross-builds passed.
  This is not evidence for the later casting integration or Windows runtime.
- Parent-owned receiver/config/discovery/diagnostic hardening passed race tests.
  Added regressions cover malformed binary plist lengths, XML nesting, rejected
  legacy OpenStep inputs, manual/hardware identity reconciliation, RAOP/video
  deduplication, DHCP changes, and stale/live endpoint selection.
- `FuzzParseModel` completed 153,921 executions in 9 seconds with no crash.
- Receiver/config hardening passed repeated race tests and read-only review.
- Three source-based quality research reports are in `docs/research`. Their
  proposed bitrates/presets and Windows commands are not measured results.
- The native capture component passed its complete nested module race/vet suite,
  actual FFmpeg encode/decode color and geometry tests, Windows CGO engine/test
  cross-compilation, and strict C warning checks. This does not verify DXGI,
  GDI, DPI/cursor behavior, sustained FPS, HDR handling, or output on Windows.
- The independent quality harness passed 17 tests and native/scaled 60-frame
  measurements. See `docs/QUALITY-TESTING.md` for retained results and limits.
  Parent visually inspected first-frame monochrome/colored text crops at native
  resolution. This corpus is not proof of Windows capture or TV image quality.
- Final root and nested sender race/vet suites passed, followed by Linux and
  Windows CGO builds. The sender suite included the new authentication fixes.
- All 57 acceptance-runner self-tests passed. Expected failures reject
  signal/panic exits; final receiver counters and reconnect epochs are checked.
- All 21 live sender/controller cases passed with real X11 chart capture and
  synthetic modern, Roku and Apple TV 3 receivers, using strict legacy
  decryption checks. PIN, combined password/Digest, wrong PIN, cancellation,
  Stop, EOF, receiver loss, reconnect and Close were exercised. No child needed
  SIGKILL. Reported crypto/malformed counters were zero. This validates H.264
  transport and AVCC framing, not picture decode or TV presentation.
- The live suite passed again after adding a rendered-source check. The private
  1920x1080 desktop matched the reference chart at RGB PSNR 42.523 dB before
  casting. An early black/partial FFplay startup image no longer qualifies.
- A test-only stdout tap recorded the actual Linux capture pipeline's framed
  RTP. The production parser extracted 67 complete H.264 access units, and
  FFmpeg decoded all 67 pictures without reported errors. Decoded metadata was
  1920x1080, yuv420p, BT.709 limited range. Parent inspected the decoded first
  picture. This was encoder output before AirPlay encryption, not TV output.
- Supervisor Unix PID identity cleanup was fixed and passed repeated race/vet
  and cross-compilation checks; its read-only re-review is clean.
- The post-review native Linux GUI rendered in isolated Xvfb/D-Bus/network
  sessions. PIN cancellation left zero video packets and removed its sender.
  Correct PIN started real desktop capture and validated H.264 transmission.
  Close hid the panel while frame counts increased; tray activation reopened
  the same window. Stop removed its sender, reconnect sent fresh validated
  frames, and confirmed Quit exited GUI/sender cleanly. Final synthetic receiver
  totals were 2,205 validated frames, zero crypto/malformed errors, two teardowns.
  The build VM initially lacked GStreamer; the UI reported an explicit capture
  failure. After installing the documented Linux runtime, the flow passed.
- Actual mDNS discovery and `/info` passed against the explicitly labelled
  synthetic fixture on isolated virtual Ethernet interfaces. This exposed DNS
  presentation escapes in names; the parser now decodes the instance label,
  with race-tested space, decimal, dot and backslash regressions.
- A remotely cross-built development CLI was run on the Mac for read-only LAN
  discovery. It exposed an interface-filter issue, now covered by tests. A
  subsequent attempt failed with an IPv4 multicast route error. No physical
  receiver was discovered or verified, and no Mac screen/audio was captured.

## Review findings addressed in the discovery foundation

- Bounded binary/XML information parsing rather than unsafe decoder autodetection.
- Stable hardware identities remain separate when DHCP reuses an endpoint.
- Current live observations take precedence over stale saved endpoint records.
- IPv4-unusable interfaces do not exhaust the interface limit.
- An IPv6 ingress zone is never guessed from the outgoing query interface.

The core read-only review is clean after the final freshness fix. Optional
WASAPI audio passed isolated VM tests and Windows cross-compilation, but actual
Windows capture/playback is unverified and audio remains default-off. Native
audio's separate read-only review found no concrete defects.
Capture, audio, supervision, UI and engine protocol review fixes passed focused
tests and fresh read-only review. Windows cursor exclusion is blocked explicitly.

Packaging review found missing historical Go module manifests and unsafe output
symlink handling. Fixes add cold-cache offline checks and atomic, guarded output
publication, with passing path-safety regressions. Temporary verification caches
use writable module directories. Both module roots passed cold-cache, file-only
proxy verification as the normal build user; cleanup completed. Code review is
clean, including the rendered-source metric and capture extraction tests.
Both final archives reproduced byte-for-byte, passed internal checksums, and
were retrieved and checksum-verified on the Mac. The packaged Windows engine
rebuilt byte-identically from its source using the offline module proxy.
Packaged GUI/engine binaries also matched the final tested cross-builds.

## Final artifacts

- `dist/aircast-windows-amd64-casting.zip`
  SHA256 `355c7b946b73e68ada8488a5c4488bd3e9222e9553fb4f586f95a19386fe4a8f`
- `dist/aircast-linux-amd64-casting.tar.gz`
  SHA256 `986de0fa958279dd97b2d4485cd05ee19548e232c9964b9bc353476727819823`

The original stale archives were replaced only after successful verification.
Local evidence is under `.build/evidence` and `.build/screenshots/final`.

## Required before Windows casting handoff

- [x] Final root and nested sender unit, race, and UI interaction tests.
- [x] Linux and Windows CGO builds including the actual sender/native helpers.
- [x] Real Linux GUI screenshots, Start/Stop, pairing, and tray interactions.
- [x] Actual mDNS discovery against an explicitly labelled synthetic receiver.
- [x] Actual encoded media sent to a loopback AirPlay receiver, not just `/info`.
- [x] Pairing, rejected PIN, cancellation, reconnect, and child-process cleanup.
- [x] Decode image fixtures and record text/color/geometry results and limits.
- [x] Read-only `/review`, fixes, and regression tests.
- [x] Bundle Windows GUI, sender, required FFmpeg, notices/source, and test guide.
- [x] Retrieve binaries, checksums, logs, and screenshots locally.

Actual Windows desktop/GPU behavior and physical TV presentation were not part
of the automated verification; the Windows build was launched and discovered a
receiver, but capture and playback still need a manual pass on real hardware. Record the exact receiver model/firmware and Windows GPU/DPI/HDR
settings. Virtual monitors, Chromecast sending, file playlists, surround sound,
and multi-receiver streams remain later roadmap items, not beta capabilities.
