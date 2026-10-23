# Windows and TV manual test

This is the first local-machine casting test, not a Windows or Apple TV pass.
The build VM can cross-compile Windows executables and run synthetic Linux
fixtures. It cannot establish that Windows captures physical desktop pixels,
that a GPU initializes, or that a TV decodes and displays the stream.

The Windows backend currently shares the primary landscape SDR monitor only.
It uses native C/CGO DXGI capture with CPU readback and bundled FFmpeg software
x264 encoding. GDI is a compatibility fallback, not the native quality path.
No GStreamer installation or `DISPLAY` setting is required. HDR, unknown color
state and rotated outputs are rejected. The first ceiling is receiver-approved
1080p30 SDR H.264. Cursor sharing is required because DXGI drivers can embed the
pointer in acquired pixels. System audio is experimental and off by default.

## Record the test machine and receiver

Copy this checklist into a test report. Do not include pairing PINs, passwords,
reusable pairing identities or private desktop content.

```text
Bundle ZIP SHA256:
Aircast BUILD-INFO.txt toolchain:
Windows edition/version/build and x86-64 CPU:
CPU AVX/AVX2 support, if known:
GPU model(s), active display adapter and driver version:
Monitor physical resolution, refresh rate, orientation and scale percent:
HDR / Advanced Color / Night light / color profile:
Other connected monitors, adapters and physical placement:
Receiver maker, exact model, model year/hardware generation:
Receiver OS/firmware version:
TV picture mode, overscan/Just Scan and motion smoothing:
Computer and receiver network, Ethernet/Wi-Fi, VPN/isolation:
Test date, quality profile, requested and reported output resolution/fps:
Result: not run / pass / fail, with the failing step:
```

For an Apple TV box, record both the box model/tvOS version and attached TV.
For built-in AirPlay, record the TV model and firmware. One passing receiver
does not establish compatibility with every AirPlay model or firmware.

## Extract and inspect

1. Verify the archive hash against its adjacent `.sha256` file. In PowerShell,
   run `Get-FileHash .\aircast-windows-amd64-casting.zip -Algorithm SHA256`.
2. Extract the whole ZIP to a normal writable folder. Do not run inside the
   ZIP. Keep `aircast.exe`, `aircast-engine.exe`, `aircastctl.exe` and
   `ffmpeg.exe` together. Read the source/license notes before sharing it.
3. Start with an SDR landscape monitor and Windows 10/11 x86-64. Leave the
   existing resolution, refresh rate and scale unchanged. Do not enable HDR
   for the first test. Record any existing HDR/Advanced Color state before
   changing it manually.
4. Run `aircast.exe` without administrator privileges. Record SmartScreen or
   antivirus warnings. This is an unsigned private test build. Do not disable
   antivirus, SmartScreen globally or Windows Firewall to make it launch.
5. Confirm the tray/panel opens and Quit exits. A GUI launch is not a casting
   pass. If startup fails, capture the exact error and Windows Event Viewer
   application error, without secrets.

Optional component inventory from the extracted folder:

```powershell
.\ffmpeg.exe -version
.\ffmpeg.exe -hide_banner -filters | Select-String 'ddagrab|gfxcapture|hwdownload|zscale|lutrgb|scale_d3d11'
.\ffmpeg.exe -hide_banner -encoders | Select-String 'libx264|h264_nvenc|h264_qsv|h264_amf'
.\ffmpeg.exe -hide_banner -demuxers | Select-String 'rawvideo'
.\ffmpeg.exe -hide_banner -muxers | Select-String 'h264'
```

Save whether these commands actually ran. A listed hardware encoder still
needs a compatible GPU/driver and a successful encode/decode/capture test.
Do not infer hardware support from its name alone. The supplied bundle has no
ffprobe; do not copy a random executable from a search result to diagnose it.

## Network and firewall

1. Put the computer and receiver on a trusted LAN that allows peer traffic.
   Guest Wi-Fi client isolation, multicast filtering, VPNs and different
   subnets can block discovery or media. Do not change a corporate network's
   policy or turn a public network into Private merely to bypass controls.
2. On your own trusted home/test LAN, check Windows Settings > Network &
   internet > the active connection. Use the Private profile if appropriate.
3. If Windows asks for firewall permission, allow the named Aircast/helper
   only on Private networks. Leave Public unchecked. Inspect a helper-specific
   prompt rather than approving an unrelated executable.
4. Do not disable Windows Firewall globally or open all ports. If a custom
   rule is required, scope it to the exact extracted executable, Private
   profile and local subnet. Have the parent diagnose actual blocked traffic
   before adding broad UDP/TCP exceptions. Remove test-only rules after use.
5. Confirm the receiver appears. If it does not, try its current IP address
   manually. Record discovery versus manual reachability separately.

Diagnostics available without starting capture:

```powershell
.\aircastctl.exe discover -timeout 4s
.\aircastctl.exe probe 192.168.1.40:7000
```

The IP is an example. Use the receiver's current address and advertised port.
A successful probe is not pairing or playback. A discovered Google Cast or
audio-only endpoint is not proof that this AirPlay screen-casting build can
mirror to it.

## Pairing and actual pictures

1. Select the AirPlay screen receiver and the quality profile. Confirm the
   monitor the app will share before starting. Close private windows first.
2. Start sharing. If the TV shows a PIN, enter it only in Aircast's credential
   prompt. Record that the prompt appeared and whether pairing succeeded, not
   the PIN. Cancel once and confirm capture and connection attempts stop.
3. Repeat. Try one incorrect PIN, then the correct current PIN. It should
   report failure/retry clearly rather than claiming streaming or saving an
   incorrect credential. Do not brute-force the TV.
4. Confirm the TV displays the current desktop, not a static preview or black
   frame. Move a window and cursor. Verify motion on the TV and a readable
   state/diagnostic if the connection fails.
5. Record reported source physical pixels separately from encoded output
   pixels. H.264 SDR, normally up to the receiver-approved 1080p30 canvas, is
   the first compatibility target. A 4K desktop captured at 200% scale is
   still a 3840x2160 source, not a 1920x1080 source. Receiver limits may require
   a smaller coded picture. Neither a resolution label nor "streaming"
   status proves decoded receiver quality.

## Image quality is a gate

Use a non-sensitive test desktop or deterministic fixture. Display test images
at one image pixel per physical monitor pixel, with no viewer fit-to-window
scaling. Verify that mapping; an unverified browser zoom is not a pixel test.
Disable TV overscan with its normal Just Scan/Screen Fit option where available.
Record TV processing settings instead of changing them without noting it.

The bundle includes `quality-chart-1080p.png`, a deterministic 1920x1080 RGB
reference generated by `scripts/quality`. Open it at actual pixel size on the
sender. It is a source chart, not an example of a receiver's decoded output.

- Show black-on-white and white-on-black text at several sizes, including
  8/10/12/16/24-pixel glyphs. Include code punctuation, thin strokes and small
  spreadsheet numbers. Compare grayscale and colored/ClearType edges.
- Show 1/2/4-pixel grids, corner labels, rulers, circles and diagonals. All
  corners must remain visible. Circles must stay circular, with no hidden
  crop or stretch. Include an ultrawide or 16:10 source if available; expect
  aspect-preserving bars instead of a forced 16:9 stretch.
- Show a neutral gray ramp, near-black/near-white steps, saturated red/green/
  blue/cyan/magenta/yellow patches and a smooth gradient. Look for crushed
  blacks, clipped whites, wrong hue, washed-out range and banding. A TV photo
  can document symptoms but does not prove exact color conversion.
- Scroll code, drag a window, move thin lines and switch full-screen slides.
  Watch for smeared text, I-frame damage, stutter and latency that grows with
  time. A static sharp frame does not establish 30 fps. Use a frame counter
  if measuring distinct frames or glass-to-glass latency.
- Run the same scene for at least two minutes in quality, then balanced, at
  the same geometry/network/TV settings. Record clarity, motion, CPU, memory,
  network rate and fan/thermal behavior. Quality should not silently become
  a lower resolution/FPS while still displaying the quality label.

Current encoder settings are quality CRF 18, `faster`, 12 Mbit/s maximum and
150 ms VBV; balanced CRF 20, `veryfast`, 8 Mbit/s maximum and 100 ms VBV.
Those are configured budgets, not measured bitrate, latency or a quality pass.

H.264 4:2:0 discards colored-edge detail. Some one-pixel red/blue text blur can
remain even at high bitrate. Do not promise pixel-identical 4:4:4 or claim that
more bitrate repairs missing chroma. Unreadable ordinary grayscale text,
stretching, systematic DPI blur or wrong color/range still fails the build.

## DPI, monitor and capture boundaries

- Repeat at 100%, 150% and 200% Windows scaling where the machine supports
  them. Record physical source dimensions and whether text/edges change.
  Windows scale should not change automatically when sharing starts.
- With two monitors, include different DPI values and a monitor positioned
  left of or above primary. Verify the documented selected/default monitor,
  edge coverage and cursor placement. Do not mistake a combined, downscaled
  virtual desktop for one full-resolution monitor.
- Change primary or reconnect a dock only after stopping first, then restart
  and confirm the intended source. In a separate recovery test, unplug the
  selected monitor during casting. The app must stop or report/reselect
  explicitly, not silently expose another monitor or continue stale success.
- If portrait, HDR, an individual window or protected video is unsupported,
  require a clear limitation/error. Do not count a sideways, cropped or
  washed-out picture as support. Never elevate to capture the secure desktop
  or bypass protected-content restrictions.
- Lock Windows and unlock. Enter/exit a normal UAC secure-desktop prompt if
  safe to do so. Capture must not bypass the secure desktop. Record whether
  it stops, resumes correctly or needs an explicit restart.

## Stop, reconnect and soak

1. Stop sharing. Verify the TV leaves mirroring and no new desktop content
   arrives. In Task Manager check that an FFmpeg capture child does not stay
   running after stop. The engine must not keep capturing in the background.
2. Start/stop five times, including an immediate stop while connecting and
   while waiting for a PIN. Check for orphan helpers, leaked prompts and a
   panel that falsely remains streaming.
3. Close the panel while sharing. Verify the documented tray behavior, reopen
   it and stop. Quit must terminate casting and child capture processes.
4. Power off/disconnect the receiver briefly or interrupt its network, then
   restore it. Record timeout/error behavior. Reconnect deliberately; do not
   accept a permanently frozen TV frame with a success label.
5. Run a ten-minute mixed static/moving scene. Record CPU/memory/network,
   responsiveness, latency drift and receiver errors. Report the first failed
   step even if a later restart happens to work.

## System audio, only if supported and enabled

Windows loopback requires `AIRCAST_EXPERIMENTAL_WASAPI_LOOPBACK=1` in Aircast's
process environment before launch. This enables an experimental path, not a
runtime-verified capability.

Leave audio off for the first video-quality test. If the UI/engine explicitly
supports and enables system audio on this platform, play a non-sensitive
left/right tone and a visible clap/flash clip. Verify the correct system
output, stereo channels, mute/volume behavior, A/V drift and silence after
stop. It must not silently capture a microphone instead. Test a silent source
and an output-device change. If audio is unavailable or disabled, mark it
unsupported/not tested, not passed merely because video plays.

## Report

Send the filled machine/receiver record and step numbers that passed/failed.
Include error messages, reported source/output geometry and profile, and only
non-sensitive fixture screenshots/short TV clips. Do not send a full private
desktop recording or credential logs. Distinguish these results explicitly:

- Windows launch.
- Discovery and pairing.
- Physical-pixel capture and correct monitor.
- Actual decoded moving pictures on the named receiver.
- Text, color and aspect quality in each profile.
- Stop/reconnect/soak and, only if enabled, audio.

Until this checklist runs on the local machine and actual TV, those runtime
and receiver outcomes remain unverified.
