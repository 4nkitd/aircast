# Encoding and image-quality verification

Research date: 2026-10-20. This is an implementation proposal, not a test report.
No Mac compilation, encoder benchmark, Windows capture test, or physical AirPlay
receiver test was performed for this research.

Inspected Aircast at `/path/to/aircast` and the pinned
DoubleTake checkout at
`/private/var/folders/tz/ssfkzx5x60j8mlm9125n74xm0000gn/T/opencode/aircast-doubletake-reference`.
The latter is revision `5e2f1cbe3e1070bcefec8e549c7baf884727bc00`.

## Recommended starting policy

Ship an honest SDR H.264 path first. Default to the quality profile below, retain
the receiver's proven canvas, preserve aspect ratio, and convert RGB to 4:2:0
only after resizing. Use capped CRF rather than a small fixed bitrate. Prove the
decoded pixels before calling the picture good. HEVC is an optional,
receiver-validated hardware path, not a consequence of the TV having a 4K panel.

The numerical profiles and quality gates below are proposed starting values.
They have not been measured against Aircast. A bandwidth cap can prevent an
encoder from reaching its requested CRF or target quality.

## What the inspected code actually establishes

Aircast currently has no capture or encoder implementation. Its
`internal/diagnostics/probe.go:67-76` reads only the model from `/info`.
`internal/receiver/device.go:47-56` does not retain video capabilities.
`cmd/test-receiver/main.go:31-43` serves an information plist, not a video
decoder. Existing discovery tests therefore establish no image-quality result.
This matches `README.md:74-87`.

Do not import these DoubleTake defaults unchanged:

| Finding in the pinned sender | Consequence and required change |
| --- | --- |
| `capture.go:2345-2357` computes `vbvBufferKbit`; `:2053` assigns it to `vbv-buf-capacity`. | Definite unit mismatch. GStreamer's property is milliseconds, not kbit. At 4,147 kbit/s and 30 fps the value 276 becomes 276 ms, approximately 1,144 kbit of VBV, not the intended two frames or 276 kbit. At 12,000 kbit/s and 60 fps, 400 becomes 400 ms and 4,800 kbit. Rename the value by its real unit and test the encoder's effective parameters. [1][2] |
| `capture.go:1982-1983,2052-2056` requests a 25% larger `vbv-maxrate` in `option-string`. | The GStreamer 1.24 implementation applies rate-control properties after parsing that string and sets maximum bitrate back to `bitrate`. The requested overshoot cannot be assumed effective. Test the shipped plugin version, not the command string. [2] |
| `capture.go:2319-2331` budgets roughly 0.0667 bit/pixel/frame, with 1.8 to 12 Mbit/s automatic limits. | 720p30 gets 1,843 kbit/s and 1080p30 gets 4,147 kbit/s. These are network-saving heuristics, not measured text-quality targets. `capture_test.go:344-386` locks those low budgets into tests. Explicit `Bitrate` bypasses the automatic cap. |
| `capture.go:2300-2310` keeps automatic H.264 bitrate at a 1080p budget even with a larger canvas. | A larger encoded canvas must not silently inherit a 4.147 Mbit/s budget. Derive the budget from the actual selected encoding mode, not the word "4K" in an advertisement. Ordinary upstream H.264 selection currently uses nominal dimensions, so this is also a defensive requirement for future larger modes. |
| `capture.go:2048-2060` uses ABR `pass=0`, `superfast`, zero B-frames and sliced threads. | Low latency is deliberate, but the preset and rate control sacrifice coding efficiency. `superfast` uses much simpler motion analysis than `faster`; `zerolatency` also disables lookahead and macroblock-tree rate control. Retain the latency constraints, spend more bits, and benchmark `faster` versus `veryfast`. [1][3] |
| `capture.go:959-963` converts to I420/NV12 before resizing; `:1093-1102` stages through another 4:2:0 layout before resizing. | Colored text loses chroma detail before spatial filtering. I420 to NV12 alone is a layout change, not automatically another lossy conversion, but the initial RGB to 4:2:0 loss is real. Stage owned RGB or 4:4:4, resize once, then subsample once. Preserve the upstream protection against retaining PipeWire-owned buffers. |
| `capture.go:932-945` sets even canvas dimensions and `add-borders=true`, but no scaler method; no explicit output colorimetry or chroma site appears in these pipelines. | Aspect-preserving borders are a good existing safeguard. GStreamer's default spatial scaler is bilinear and its default chroma resampler is linear. Neither color correctness nor fine-text filtering has been established. Select filters and signal metadata explicitly. Hardware postprocessing needs its own pixel comparison. [4][5] |
| `capture.go:2159-2173` uses a moving ball and time overlay for synthetic video. | Useful for liveness and timing, weak for thin strokes, chroma, gradients, gamma, and hard scene changes. It can also negotiate a YUV source, bypassing the desktop RGB conversion being evaluated. Add native RGB fixtures. |
| `capture_test.go:483-735` checks pipeline tokens and even caps; `:577-622` checks HEVC parameter sets and sample-description dimensions. | These are structural tests, not decoded-image tests. Keep them, but do not treat them as perceptual verification. Add independent decoding, known-pixel checks, and capture-to-decoder tests. |
| `receiver_media.go:570-625,666-705` counts packets/header dimensions and optionally decrypts and validates AVC NAL framing. | It does not decode pictures. The reported width/height comes from the sender's header, not a decoded surface. A syntactically plausible NAL with wrong colors or unusable image content can pass these checks. |

Preserve the pinned sender's useful bounds: one latest raw frame before encoding,
timestamped complete access units, and separate receiver queues. Its
`capture_broadcast.go:10-32,170-175` distinguishes single-target backpressure
from bounded fan-out and does not drop dependent encoded pictures casually.

## Pixel, color, and text policy

### Keep the sampling loss in the right place

H.264 High and ordinary HEVC Main use 4:2:0 in the proposed interoperability
path. Each chroma plane has half the luma width and height. Red/blue fine text,
isoluminant color boundaries, and RGB/BGR subpixel-antialiased glyphs can look
worse than black/white text even at very high bitrate. CRF 0 cannot recover
information discarded before encoding.

NV12 and I420 have the same 4:2:0 sampling and 8-bit precision; NV12 is not a
picture-quality upgrade. Main10 improves quantization precision, not chroma
resolution. An encoder accepting RGB, Y444, 4:2:2, or Main10 does not prove the
AirPlay receiver accepts the resulting profile. No inspected source establishes
universal AirPlay 4:4:4 support. Do not make 4:4:4 a default or use a proprietary
two-stream chroma reconstruction scheme as if it were ordinary AirPlay.

Use this order:

1. Acquire actual source pixels, pixel format, row strides, crop, orientation,
   timestamp, and color description. Do not substitute DPI-scaled window bounds
   or portal logical coordinates for pixel dimensions.
2. Copy or import into owned storage when downstream may retain a frame. Keep
   RGB or 4:4:4 precision. Layout conversion must not accidentally subsample.
3. Normalize source color and, where necessary, tone map in adequate precision.
   Resize the RGB/4:4:4 content once. Add the final canvas borders.
4. Convert once to BT.709 limited-range I420 or NV12 with explicit chroma siting.
   Encode without a second resize or automatic color guess.

Quality starts with Lanczos spatial filtering and Lanczos chroma resampling.
Balanced starts with cubic filtering and cubic chroma resampling. At 1:1, bypass
spatial filtering. Inspect high-contrast strokes for ringing; a sharp filter is
not automatically better. If Lanczos creates visible halos, adopt the measured
cubic result rather than adding sharpening. GStreamer exposes `method=lanczos`
or `method=mitchell`, and `chroma-resampler=lanczos` or `cubic`. For the balanced
spatial choice, use `method=mitchell`. Equivalent FFmpeg test filters must pin
their algorithm and coefficients rather than relying on defaults. [4]

Do not change the user's host font smoothing settings. Include both grayscale
and subpixel-antialiased text in tests. A future application-rendered source may
choose grayscale antialiasing, but monitor capture cannot repair the OS's
subpixel assumptions without additional image processing and loss.

### Make the conversion and metadata agree

For the first supported output, fix all resolutions to this contract:

| Property | SDR output |
| --- | --- |
| Primaries and white point | BT.709, D65 |
| Transfer | BT.709 after an explicitly defined source-to-output transform |
| Matrix | BT.709, never an SD-resolution guess of BT.601 |
| Range | Limited, H.264/HEVC `video_full_range_flag=0` |
| Sampling | Progressive 4:2:0, 8-bit |
| Chroma site | MPEG-2/left, matching H.264 chroma location type 0 |
| Sample aspect ratio | 1:1 |

Treat captured SDR RGB as full range only where the capture API's contract
supports that interpretation. sRGB and BT.709 share primaries, but not the same
transfer curve. A BT.709 matrix is not a gamma conversion. If the input is sRGB,
define and apply the sRGB-to-output transfer mapping; if the capture surface is
already color-managed, do not apply the monitor ICC transform a second time.
The physical receiver's rendering still needs grey-ramp validation. Record any
receiver-specific transfer behavior rather than silently changing the global
conversion. [5][6]

For normalized nonlinear BT.709 R', G', B' in 0 to 1, the conversion oracle is:

```text
Y' = 0.2126 R' + 0.7152 G' + 0.0722 B'
Cb = (B' - Y') / 1.8556
Cr = (R' - Y') / 1.5748
Y8  = round(16  + 219 Y')
Cb8 = round(128 + 224 Cb)
Cr8 = round(128 + 224 Cr)
```

Flat black must produce approximately `(16,128,128)`, white `(235,128,128)`,
red `(63,102,240)`, green `(173,42,26)`, and blue `(32,240,118)` before encoding.
Allow one code of rounding difference. Derive intermediate grey values from the
chosen transfer transform; do not assume source sRGB code 128 remains BT.709
code 128. Use large uniform patches so chroma filtering does not affect the
sampled interiors. [5][6]

Limited range does not mean lifted black on a correct decoder. Full-range data
labelled limited crushes levels; limited-range data interpreted as full lifts
black and dulls white. Converting twice also compresses contrast. Set conversion
parameters, encoder VUI, and any container metadata consistently. Metadata-only
flags such as `-color_range tv` or `setparams` do not perform that conversion.

With GStreamer, set truthful input colorimetry and request output
`video/x-raw,format=I420,colorimetry=bt709,chroma-site=mpeg2,pixel-aspect-ratio=1/1`.
Use NV12 for the hardware equivalent. Enable actual transfer conversion when
needed with `gamma-mode=remap`; the default is `none`. Enable primaries
conversion when needed rather than just changing the caps. Inspect the
negotiated `GstVideoInfo` and emitted SPS VUI. `x264enc` derives VUI from that
information, so `option-string` is not a substitute for correct raw caps.
Full-range YUV remains a per-receiver experiment, not the quality default. [2][4]

## Aspect ratio, dimensions, and monitor changes

Select a supported encoded canvas first, separately from the content rectangle.
For source pixel dimensions `Sw,Sh` and validated canvas `Cw,Ch`:

```text
Cw = 2 * floor(Cw / 2); Ch = 2 * floor(Ch / 2)
s = min(1, Cw / Sw, Ch / Sh)
Fw = 2 * floor(Sw * s / 2); Fh = 2 * floor(Sh * s / 2)
left = 2 * floor((Cw - Fw) / 4)
top  = 2 * floor((Ch - Fh) / 4)
right = Cw - Fw - left; bottom = Ch - Fh - top
```

Reject zero/invalid results. Calculate using wide integers or checked arithmetic
and sufficient precision. The no-upscale rule avoids manufacturing detail;
prefer a smaller accepted canvas where available. If a receiver requires its
fixed nominal canvas, pad it. Any later decision to enlarge smaller content for
presentation must be an explicit, single aspect-preserving scale, never a claim
of extra native resolution.

Even content dimensions and even left/top offsets avoid a chroma-phase shift at
the padding boundary. Opposite borders can differ by two pixels. Flooring both
dimensions introduces less than two pixels of fitting error per dimension; test
against that bound, not mathematically exact ratios for every odd input.
Never stretch or crop desktop edges to fill a 16:9 output. Do not force dimensions
to multiples of 16; the encoder can pad macroblocks and signal the display crop.

For example, 3024x1964 into 1280x720 fits as 1108x720 with 86-pixel side borders.
1080x1920 into 1920x1080 fits as 606x1080, with left/right borders 656/658.
Use actual negotiated pixel dimensions for fractional-DPI and Wayland capture.
The pinned sender already correctly avoids using portal logical size for this
purpose at `capture.go:1144-1147`.

On resize, rotation, monitor change, DPI change, or HDR-mode change, recompute the
plan. Never read stale strides or encode undefined capture-surface margins.
Flush the old codec configuration when required and start the new one with an
IDR and matching parameter sets. Windows documents that only `ContentSize` is
valid when a captured item is smaller than its allocated surface. [7]

## Exact initial profiles

These are encoder settings, not additional product modes or a reason to build a
large configuration framework. Keep two named profiles and record their resolved
settings in diagnostics. Both default to 30 fps, 8-bit BT.709 limited 4:2:0.
Never select 60 fps by doubling frame rate while keeping the 30-fps bit budget.

### Software H.264

| Setting | Quality, default | Balanced |
| --- | --- | --- |
| Canvas | Largest proven H.264 nominal canvas, initially at most 1920x1080 | Same geometry policy; do not start by sacrificing text resolution |
| x264 preset | `faster` | `veryfast` |
| CRF | 18 | 20 |
| Maximum video rate at 1280x720 or below | 8,000 kbit/s | 4,000 kbit/s |
| Maximum video rate above 720p, up to 1920x1080 | 16,000 kbit/s | 8,000 kbit/s |
| VBV capacity | 150 ms, 2,400 kbit at 16 Mbit/s | 100 ms, 800 kbit at 8 Mbit/s |
| GOP | Maximum 2 seconds, 60 frames at 30 fps | Same |
| B-frames, lookahead | 0, 0 | Same |
| Thread cap per encoder | `min(6, max(1, logicalCPUs-1))` | `min(4, max(1, logicalCPUs-1))` |
| Receiver-constrained AVC profile/level | High level 4.0 for validated 1080p30; High 3.1 for validated 720p30 | Same |

Use CABAC and normal deblocking. Do not tune for PSNR/SSIM merely to improve the
test score. Retain `tune=zerolatency` and sliced threading initially; their
efficiency cost is accepted to keep encoding delay bounded. Do not choose
`ultrafast` or `superfast` silently when CPU is busy. If a receiver needs Main or
Baseline, select that as a recorded compatibility restriction and rerun quality
tests. Neither preset names nor one old Apple-origin High 4.0 sample establish
universal receiver acceptance. [1][3][8]

For already converted 1080p30 YUV, the GStreamer quality encoder stage is:

```text
x264enc speed-preset=faster tune=zerolatency pass=qual quantizer=18
  bitrate=16000 vbv-buf-capacity=150 key-int-max=60
  bframes=0 rc-lookahead=0 sync-lookahead=0 sliced-threads=true threads=T
  byte-stream=true aud=true
  option-string=min-keyint=1:scenecut=40:open-gop=0:repeat-headers=1
! video/x-h264,profile=high,level=(string)4
! h264parse config-interval=-1
! video/x-h264,stream-format=byte-stream,alignment=au
```

`T` is the resolved thread count, not a literal pipeline argument. For balanced,
replace the preset, quantizer, bitrate, and capacity using the table. In
`pass=qual`, GStreamer's `quantizer` maps to CRF and `bitrate` is the VBV maximum.
Do not confuse `pass=quant`, constant QP, with CRF. Verify the effective CRF,
maximum bitrate and buffer size after initialization. [1][2]

Equivalent FFmpeg encoder-only arguments for the quality 1080p30 experiment are:

```text
-c:v libx264 -preset faster -tune zerolatency -crf 18
-maxrate 16M -bufsize 2400k -pix_fmt yuv420p -profile:v high -level:v 4.0
-g 60 -bf 0 -threads T
-x264-params min-keyint=1:scenecut=40:open-gop=0:repeat-headers=1:aud=1:rc-lookahead=0:sync-lookahead=0
-color_range tv -colorspace bt709 -color_primaries bt709 -color_trc bt709
```

Those flags assume conversion has already happened. They are not a complete
desktop capture command, and were not executed. FFmpeg uses bits for `bufsize`;
GStreamer uses milliseconds for the x264 capacity property. VBV is a rate-control
model, not a promise that a network queue or end-to-end latency is 150 ms. [9]

Send codec configuration and force a fresh IDR after the receiver becomes media
ready, on a new receiver joining a shared encoder, and after a supported
reconfiguration. Do not wait up to two seconds for the periodic IDR. Keep scene
cut IDRs available. Do not replace IDRs with gradual intra refresh before testing
receiver behavior. A frame-count GOP can last longer than two seconds when raw
frames are dropped; also request an IDR when elapsed capture PTS reaches the
two-second limit. Test random access at startup, after a cut, and under drops.

### Optional hardware HEVC

First establish a real encode/decode path on the GPU and exact receiver model.
Feature 42 and a maximum canvas are necessary evidence in the inspected
implementations, not sufficient proof of Main10, HDR, arbitrary profile/level,
or 4K mirroring. The pinned `video_codec.go:60-81` deliberately keeps automatic
mode on H.264. Its actual screen HEVC selection at `capture.go:2091-2115` is
8-bit Main, despite the README's opening Main10 wording. [10]

For an explicitly enabled, validated SDR 3840x2160@30 path, start with:

| Setting | Quality | Balanced |
| --- | --- | --- |
| Codec | HEVC Main, NV12, 8-bit | Same |
| Initial tier/level requirement | Main tier, level 5.1, explicitly accepted by receiver | Main tier, level 5, explicitly accepted by receiver |
| NVENC preset and tuning | P4, low latency | P3, low latency |
| Rate control | VBR target quality 20 | VBR target quality 23 |
| Average / maximum video rate | 20 / 32 Mbit/s | 12 / 20 Mbit/s |
| VBV | 4,800 kbit | 2,000 kbit |
| Frame structure | GOP 60, no B-frames, no lookahead, no reordering | Same |
| Other initial settings | Multi-pass disabled; repeated VPS/SPS/PPS; AUD enabled | Same |

For current GStreamer `nvh265enc`, these map to `preset=p4`,
`tune=low-latency`, `rc-mode=vbr`, `const-quality=20`, `bitrate=20000`,
`max-bitrate=32000`, `vbv-buffer-size=4800`, `gop-size=60`,
`rc-lookahead=0`, `zerolatency=true`, `multi-pass=disabled`,
`repeat-sequence-header=true`, and `aud=true`, with downstream `profile=main`.
Query property availability and effective no-B-frame operation on the selected
device. Older GPUs/plugins can omit the B-frame property entirely. Do not blindly
apply an unsupported `bframes=0` and break startup. [11][12]

Set and inspect the real profile/tier/level, not just `profile=main`. At 4K30,
the 32 Mbit/s quality budget exceeds HEVC level 5 Main tier's 25 Mbit/s VCL
ceiling; level 5.1 Main tier provides 40 Mbit/s. Do not assume High tier instead.
If only level 5 Main tier is proven, use a maximum of 24 Mbit/s and a 3,600-kbit
VBV as the receiver-constrained quality candidate, then rerun the gates.
Balanced's 20 Mbit/s candidate fits that level. Account for NAL/VCL overhead in
the actual level check. Never rewrite the signalled level to conceal a
non-conforming stream. [19]

NVENC target quality is not x264 CRF; equal numbers are not equal fidelity.
These settings are candidates for measurement, not a promise that HEVC beats
software AVC. Apply the same decoded-quality gates. Intel QSV, AMD AMF and VA
implementations need their own supported-property mapping and measurements.
Do not put software x265 4K in automatic fallback. Fall back to the proven
H.264 canvas with the reason recorded, or fail an explicit HEVC-only request.

### CPU, network, and queue limits

- Measure capture/conversion/encode age and encode duration separately. Require
  encode p95 below 80% of the frame interval and p99 below one interval during a
  60-second motion/text stress clip. At 30 fps those bounds are 26.7 and 33.3 ms.
  Target total sender CPU below 70% of machine capacity for quality and 50% for
  balanced over 30 seconds. Count native C threads and GPU copies; `GOMAXPROCS`
  does not cap codec threads. These are admission targets, not OS-enforced quotas.
- Keep one pending owned raw frame, newest wins, before encoding. Drop stale raw
  frames, not arbitrary encoded P pictures. Preserve original capture PTS and
  report drops separately from intentionally duplicated idle frames. Use fixed
  capture pools and a measured encoder in-flight limit; start with at most three
  capture surfaces and four hardware encode slots where the driver permits.
  Count stride, format, and alignment when budgeting native memory.
- Per receiver, cap queued complete encoded AUs at 250 ms of nominal frame
  duration and 2 MiB for these H.264 modes or 4 MiB for the 4K HEVC mode,
  whichever is reached first. At 30 fps a strict duration bound allows seven
  queued AUs. Track oldest capture age too; tiny/static frames must not evade a
  latency bound. Admission must include the incoming AU. An oversized AU fails
  that receiver's admission rather than bypassing the byte bound.
- A single-target handoff can use one pending AU with backpressure into the
  leaky raw queue. A shared encoder must not let one target block its peers.
  Disconnect/recover a lagging target at a fresh IDR instead of deleting
  reference pictures. Bound writes, initially at 500 ms, and interrupt them on
  cancellation. Do not replay seconds of stale desktop after a stall.
- Provision tested application goodput of at least
  `1.5 * sum(videoMaxRate + audioRate)` for simultaneous unicast receivers.
  Wi-Fi PHY/link speed is not application goodput. One quality 1080p stream
  therefore needs roughly 25 Mbit/s of usable headroom; 4K HEVC needs roughly
  50 Mbit/s. Do not saturate the LAN merely to perform an automatic speed test.
  Measure encoded rolling rates, actual socket writes, retransmission/stall
  symptoms, and AU sizes during the approved stream.
- Check the burst envelope, not just average bitrate. Capped CRF/VBV allows
  short bursts; a useful initial coded-video check is `bits(t) <= R*t + B`, with
  explicit AU-boundary/accounting tolerance. Add protocol/audio overhead for
  network provisioning. Larger VBV is not a replacement for measured headroom.
- If CPU admission fails, try an already qualified hardware encoder, then
  `faster` to `veryfast` at the same CRF and dimensions. For text-first quality,
  reduce 30 to 24 to 20 fps before lowering resolution. Recompute the two-second
  GOP. If the network cap still prevents readable output, report the constrained
  quality and offer the balanced mode or a lower proven canvas. No silent CRF
  increase, unlimited buffering, or claim that the requested quality was reached.

Apply CPU and memory accounting across simultaneous encoder groups, not once
per group as if each owns the whole machine.

## HDR and wide-gamut monitors

The first output should remain SDR even if the source monitor is HDR-capable.
Detect the current source mode, not just EDID support or an encoder's Main10
capability. Never label SDR capture PQ/HLG, invent mastering metadata, or infer
HDR from HEVC. [7][13]

On Windows HDR/Advanced Color, use FP16 capture and processing where required.
Microsoft specifically warns that an 8-bit BGRA pipeline can overclip or wash
out HDR capture. FP16 scRGB is linear, uses BT.709 primaries, and can contain
negative and greater-than-one components. It is not PQ and cannot be fed to an
ordinary 8-bit RGB-to-YUV converter as if it were sRGB. Read the relevant SDR
white level, preserve SDR UI white, apply a documented tone/gamut mapping to SDR,
then encode BT.709. A fixed per-session mapping avoids brightness pumping while
windows move. Store its parameters in the test manifest. [7][13]

On Wayland, read negotiated PipeWire pixel format, range, matrix, transfer,
primaries and chroma site. PipeWire exposes these fields, but their existence
does not prove a particular compositor supplies truthful HDR metadata. If the
portal delivers a known SDR image, do not tone map it again. If an HDR source's
transfer/precision is unknown, reject that path or ask for SDR capture rather
than silently guessing. X11 capture also needs explicit color assumptions. [14]

Test HDR on/off, SDR and HDR monitors side by side, moving a window between them,
SDR UI over HDR video, 100/203/1000-nit patches, near-black detail and saturated
wide-gamut patches. Assert the chosen SDR output has no PQ/HLG or HDR mastering
SEI left over. True HDR transport is a separate approval gate requiring source
metadata, tone-mapping policy, supported 10-bit receiver mode, and displayed
evidence. It is not needed to enable quality SDR mirroring.

## Validation that can falsify a quality claim

### Keep five evidence levels separate

1. Advertised: discovery or `/info` says a mode might be supported.
2. Encoded: local SPS/VPS, VUI, frame types and dimensions match the plan.
3. Received: a receiver reconstructed and decrypted the expected access units.
4. Decoded: an actual decoder emitted the expected pictures with correct pixels.
5. Displayed: the physical receiver/TV showed the expected picture and motion.

A successful RTSP response, bytes written, header dimensions, encoder discovery,
or local FFmpeg decode does not reach level 5. Apple's general video-format
specification is not an AirPlay mirroring-mode contract. [15]

Use an independently implemented protocol receiver such as pinned UxPlay in a
disposable Linux VM, with decrypted stream dumping and a decode sink. Its
`-vdmp` records compressed video, and `-h265` explicitly enables screen
multi-codec support. Save the exact version and decoder selection. A UxPlay pass
is useful interoperability evidence, not proof of Apple TV or vendor-TV behavior.
GStreamer's `avdec_h264` and FFmpeg can share libavcodec; they are not two
independent decoder engines. Add actual receiver/hardware decoding later. [10]

### Reference images and sequences

Create deterministic RGB fixtures with recorded font files, renderer versions,
dimensions, color descriptions and hashes. Do not use JPEG or screenshots that
were already subsampled as the reference. Include:

- Black/white and grey text at 8, 10, 12, 16 and 24 physical pixels, both polarities,
  monospace/code, serif and UI fonts. Include grayscale and RGB/BGR subpixel AA.
  Repeat native capture at 100%, 125%, 150% and 200% desktop scaling.
- Red, blue, cyan and magenta text; thin syntax highlighting on dark grey;
  nearly equal-luma color pairs; one-pixel colored lines and checkerboards.
  Shift patterns by one pixel horizontally and vertically to reveal chroma phase.
  Include two- and four-pixel versions that a correct 4:2:0 path can reproduce.
- One-pixel monochrome grids, alternating lines, diagonal strokes, circles,
  a one-pixel outer border, numbered corners and pixel rulers. Exercise 16:9,
  16:10, ultrawide, portrait, odd dimensions, and 3024x1964 fractional-DPI capture.
- Full RGB and grey ramps, flat primaries/secondaries, near-black and near-white
  steps. Also inject known limited-range YUV patches around Y=16 and Y=235 to
  isolate decoding/range errors from RGB conversion. Examine patch interiors.
- Scrolling code at fixed speeds, spreadsheet panning, cursor motion, window
  dragging, hard cuts between a dense document and photo, smooth gradients,
  natural motion, and deterministic noise as a bandwidth stress test.
- A machine-readable per-frame ID and capture timestamp in a reserved region.
  Exclude the marker from quality ROIs but use it to detect freezing, duplicates,
  missing frames, reordering and hidden temporal misalignment.

Build two reference branches. The full-RGB branch measures total loss from
capture, resize and subsampling. An independently generated, correctly filtered
BT.709 4:2:0 branch at the same canvas measures codec loss. Decode that ideal
4:2:0 branch back to RGB as a visual ceiling for colored text. Do not demand
lossless one-pixel chroma or excuse avoidable monochrome blur as "just 4:2:0".
Do not generate every golden pixel with the same conversion routine under test.

### Measurements and proposed gates

The thresholds here are engineering acceptance proposals, not ITU requirements,
not calibrated human-vision guarantees, and not results already achieved. Freeze
the corpus and metric implementation before tuning. Reconsider a failed
threshold using retained images and maintainer review, not by quietly lowering it.

| Gate | Required evidence |
| --- | --- |
| G0, geometry | Decoded active rectangle, SAR, rotation, even dimensions and offsets match the computed plan. All four edge markers survive; no stretch/crop. Fitting error stays below two pixels per dimension. Native 1:1 content has no extra resize. |
| G1, conversion | Independent pre-encode flat-patch oracle within one 8-bit code. Encoded flat-patch interior means within two codes for quality and three for balanced against the ideal 4:2:0 reference. No swapped channels, 601/709 switch, double range compression, clipped grey-ramp sections or false HDR tags. |
| G2, actual decode | Decode the receiver-side reconstructed/decrypted stream, not just the sender file. Zero decoder errors/concealment on a clean link; expected SPS/VPS dimensions, bit depth, chroma, VUI and frame IDs; no B-frames. Compare ordered canonical VCL NAL hashes before encryption and after receiver decryption; compare codec configuration separately. |
| G3, static/scrolling desktop | Against the ideal 4:2:0 reference, each clip's mean Y-PSNR at least 40 dB quality / 38 dB balanced; mean Y-SSIM at least 0.990 / 0.985. Each fixed text ROI must also meet that SSIM floor. Per-clip fifth-percentile Y-PSNR at least 36 / 34 dB prevents the mean hiding hard cuts and IDR damage. Measure U/V separately, with mean PSNR at least 38 / 36 dB per plane. |
| G3, natural motion | Initial mean Y-PSNR floor 36 dB quality / 34 dB balanced and mean Y-SSIM 0.980 / 0.970 against matched 4:2:0. Inspect worse frames and color ROIs. Noise is a rate/stall stress case, not a promise of visually lossless output under the cap. |
| G3, visual veto | Inspect lossless decoded crops at 100% pixel mapping and a documented normal viewing distance. No newly unreadable 10-16-pixel monochrome text relative to the fitted reference, persistent halos, colored fringe beyond the ideal 4:2:0 comparison, missing strokes, gradient pumping or range errors. A metric pass cannot overrule a visible regression. |
| G4, timing/resources | During a clean 60-second active-motion clip, at least 99% of configured frames reach the decode sink; no unreported freeze over 100 ms. Meet CPU/encode/queue bounds above. Target p95 capture-to-local-AU age at most 100 ms and physical capture-to-display at most 250 ms, p99 at most 400 ms. Report separately when a receiver's negotiated presentation lead prevents this. |
| G4, stress/recovery | Run 30 minutes with dense scrolling and cuts. Inject receiver stalls and bandwidth below the cap; queues and native pools remain bounded, cancellation works, healthy peers continue, and a recovered receiver resumes at a fresh random-access point without reference corruption. Retain actual high-water marks, not only configured limits. |
| G5, real receiver | the maintainer approves text, chroma, aspect, color and motion patterns on each claimed receiver model/firmware. Repeat connection and a mode change; retain selected settings, on-screen evidence, network conditions and limitations. A software fixture cannot close this gate. |

Record per-frame values and per-scene results, not just one aggregate score.
Report the first IDR, hard-cut frame and recovery frames separately. At native
resolution, compare text ROI edges and optionally OCR character error rate against
the same font/rendered reference. OCR is a useful regression alarm, not a reason
to add a production OCR dependency or to ignore visible glyph damage.

SSIM/PSNR inputs must have matching dimensions, frame IDs, pixel format, bit
depth, matrix and range. Align frames from the recorded IDs before measurement,
and report unmatched frames as failures/drops. Do not automatically retime or
repeat missing frames until a metric looks good. Exclude padding and ID markers
from the main score while separately testing borders. SSIM and PSNR are not
interchangeable with perceived legibility. [16][17]

Canonical transport comparisons strip Annex-B start codes or AVCC length fields
before hashing NAL payloads. Group them by access unit and account explicitly
for configuration/SEI that the protocol sends separately. Hashing a sender's raw
Annex-B file against an AVCC/encrypted packet dump is not a valid equality test.

VMAF is optional evidence for the natural-motion subset. Pin the model file and
hash, library version, pixel precision and viewing condition. Current VMAF v1
models distinguish 1080p at 3H and 4K at 1.5H/3H and recommend 10-bit measurement
precision even for 8-bit SDR encodes. Do not compare scores from different
models or resolutions as if they shared one scale. Neither the old luma-focused
model nor a newer chroma-aware one replaces fine-text ROIs and visual inspection.
Do not install or run a heavy VMAF build on the Mac for this research. [18]

### Practical commands for the future VM test job

These are unexecuted examples. For an AVC elementary stream reconstructed at the
receiver, inspect the stream and require a full decode:

```sh
ffprobe -v error -select_streams v:0 -show_entries stream=codec_name,profile,level,width,height,coded_width,coded_height,pix_fmt,has_b_frames,color_range,color_space,color_transfer,color_primaries,chroma_location,sample_aspect_ratio -of json received.h264
ffmpeg -v warning -xerror -err_detect explode -i received.h264 -map 0:v:0 -fps_mode passthrough -f framemd5 decoded.framemd5
ffmpeg -v debug -i received.h264 -map 0:v:0 -c:v copy -bsf:v trace_headers -f null - 2>headers.log
```

Use `received.hevc` for the HEVC job. Frame hashes prove neither correct content
nor color by themselves; inspect the resulting frames against the fixture and
manifest. Elementary-stream probes can infer cadence without original capture
PTS, so preserve receiver packet timestamps separately for timing evidence.

After asserting identical frame counts and aligning IDs into lossless
`decoded-aligned.mkv` and `reference-aligned.mkv`, both with identical 4:2:0
format/range, measure:

```sh
ffmpeg -i decoded-aligned.mkv -i reference-aligned.mkv -lavfi "[0:v]settb=AVTB,setpts=PTS-STARTPTS[d];[1:v]settb=AVTB,setpts=PTS-STARTPTS[r];[d][r]ssim=stats_file=ssim.log:shortest=1:repeatlast=0" -f null -
ffmpeg -i decoded-aligned.mkv -i reference-aligned.mkv -lavfi "[0:v]settb=AVTB,setpts=PTS-STARTPTS[d];[1:v]settb=AVTB,setpts=PTS-STARTPTS[r];[d][r]psnr=stats_file=psnr.log:shortest=1:repeatlast=0" -f null -
```

`shortest=1` must not hide missing input: the separate count/ID assertion is
mandatory. Repeat on fixed text/color ROIs with matching crops. Keep source RGB,
independent fitted RGB and ideal YUV references, receiver dumps, lossless decoded
crops, SPS/VUI dump, per-frame metrics, capture/encode/send/decode timestamps,
CPU/native-memory/queue samples, and a machine-readable settings manifest. Use
synthetic content by default; desktop dumps can expose private information.

## Implementation order and release stop conditions

1. Add a small resolved encoding plan with named units for canvas, active rect,
   FPS, color description, CRF/target quality, maximum kbit/s, VBV milliseconds
   and codec-specific buffer units. Test arithmetic, odd sizes, chroma offsets,
   metadata and backend parameter mapping.
2. Implement the SDR RGB-to-H.264 path and actual independent decoded fixture
   tests on the approved Linux VM. Make decoder/plugin absence fail the required
   CI job rather than silently skip it. Keep hardware-specific jobs separately
   labelled unavailable when no device exists.
3. Capture native Windows and Linux desktops with the same fixture patterns.
   Close G0-G4 separately for Windows, X11, Wayland, and each qualified hardware
   conversion/encoder backend. A synthetic appsrc encode does not prove capture.
4. Ask the maintainer to run the physical receiver matrix and close G5. Only then promote
   an optional hardware HEVC/4K mode for that receiver class. Any implementation
   change still needs the project's normal review and regression workflow.

Stop a quality claim for wrong range/matrix, stretched or cropped content,
unreadable fitted text, decoder errors, stale-frame accumulation, unbounded
native memory, or a mode justified only by advertised capabilities. Document a
receiver-limited 720p/1080p result honestly. Do not publish a "4K quality" result
from a larger header, an upscaled source, an encoder probe, or an unviewed dump.

## Sources

All web sources accessed 2026-10-20. Documentation may describe a newer plugin
than the eventual packaged runtime; verify the exact shipped versions.

1. [GStreamer x264enc](https://gstreamer.freedesktop.org/documentation/x264/index.html). `pass`, quantizer/quality modes, preset/profile restrictions, latency, sliced threading, and VBV capacity in milliseconds.
2. [GStreamer 1.24 x264enc implementation](https://github.com/GStreamer/gstreamer/blob/1.24/subprojects/gst-plugins-ugly/ext/x264/gstx264enc.c). Option/property precedence; `GST_X264_ENC_PASS_QUAL` maps to CRF; `i_vbv_buffer_size = i_vbv_max_bitrate * vbv_buf_capacity / 1000`; VUI derives from input colorimetry and chroma site.
3. [x264 preset/tune implementation](https://github.com/mirror/x264/blob/master/common/base.c). `superfast`, `veryfast`, `faster`, `zerolatency`, and metric-oriented tuning behavior. Inspect `param_apply_preset` and `param_apply_tune`.
4. [GStreamer videoconvertscale](https://gstreamer.freedesktop.org/documentation/videoconvertscale/videoconvertscale.html) and [1.24 implementation](https://github.com/GStreamer/gstreamer/blob/1.24/subprojects/gst-plugins-base/gst/videoconvertscale/gstvideoconvertscale.c). Default bilinear scale, linear chroma resampling, borders, transfer/primaries conversion controls and supported filter names.
5. [GStreamer video color API](https://gstreamer.freedesktop.org/documentation/video/video-color.html) and [video converter API](https://gstreamer.freedesktop.org/documentation/video/videoconverter.html). Range, matrix, primaries, transfer functions, gamma remapping and chroma processing are separate operations.
6. [Linux media color-space definitions](https://www.kernel.org/doc/html/latest/userspace-api/media/v4l/colorspaces-details.html). BT.709 matrix coefficients, limited quantization and the different sRGB/BT.709 transfer functions. [ITU-R BT.709-6](https://www.itu.int/rec/R-REC-BT.709-6-201506-I/en) is the normative parameter reference.
7. [Microsoft screen capture](https://learn.microsoft.com/en-us/windows/apps/develop/media-authoring-processing/screen-capture). FP16 advice for HDR, capture-frame ownership, timestamps, `ContentSize`, and recreation on size/device changes.
8. [Unofficial AirPlay stream packets](https://openairplay.github.io/airplay-spec/screen_mirroring/stream_packets.html). Observed AVC configuration sample with High profile, level 4.0; not an Apple receiver support guarantee.
9. [FFmpeg codec options and libx264](https://ffmpeg.org/ffmpeg-codecs.html#libx264_002c-libx264rgb). CRF/preset mappings, bitrate units, buffer size in bits, color metadata and chroma location.
10. [UxPlay current README](https://github.com/FDH2/UxPlay/blob/master/README.txt). Independently implemented receiver, actual decoder selection, `-h265`/feature 42, compressed stream dumping, and hardware color-decoding caveats. Protocol evidence, not Aircast hardware testing.
11. [GStreamer nvh265enc](https://gstreamer.freedesktop.org/documentation/nvcodec/nvh265enc.html). Supported profiles, VBR quality, rate units, VBV in kbits, presets, tuning, repeated sequence headers and property version differences.
12. [NVIDIA NVENC SDK 13 programming guide](https://docs.nvidia.com/video-technologies/video-codec-sdk/13.0/nvenc-video-encoder-api-prog-guide/index.html). Query actual capabilities; P1-P7 tradeoffs; VBR target-quality limits; no-reordering operation; force-IDR and parameter-set requests; input/output resource bounds.
13. [Microsoft Advanced Color and HDR](https://learn.microsoft.com/en-us/windows/win32/direct3darticles/high-dynamic-range). scRGB/FP16 composition, SDR reference white, HDR/WCG capture, gamut/tone mapping, and monitor-mode changes.
14. [PipeWire spa_video_info_raw](https://docs.pipewire.org/structspa__video__info__raw.html). Negotiated size, PAR, chroma site, range, matrix, transfer and primaries fields.
15. [Apple TV 4K technical specifications](https://www.apple.com/apple-tv-4k/specs/). General playback/video formats only; these must not be substituted for observed AirPlay mirroring-mode support on the test device.
16. [FFmpeg SSIM](https://ffmpeg.org/ffmpeg-filters.html#ssim), [PSNR](https://ffmpeg.org/ffmpeg-filters.html#psnr-1), [scale](https://ffmpeg.org/ffmpeg-filters.html#scale-1), [zscale](https://ffmpeg.org/ffmpeg-filters.html#zscale-1) and [trace_headers](https://ffmpeg.org/ffmpeg-bitstream-filters.html#trace_005fheaders). Matched-format/frame requirements, per-frame metrics and explicit scaling/color controls.
17. [Wang et al., SSIM reference and demonstrations](https://ece.uwaterloo.ca/~z70wang/research/ssim/). Pixel error and perceived structure can disagree; scale and viewing conditions matter.
18. [Netflix VMAF v1 models](https://github.com/Netflix/vmaf/blob/master/resource/doc/models_v1.md) and [FAQ](https://github.com/Netflix/vmaf/blob/master/resource/doc/faq.md). Model/viewing-condition dependence, measurement precision and cross-resolution comparison limits.
19. [FFmpeg HEVC profile/level constraint tables](https://github.com/FFmpeg/FFmpeg/blob/master/libavcodec/h265_profile_level.c) and [ITU-T H.265](https://www.itu.int/rec/T-REC-H.265). Picture-size, sample-rate, CPB and VCL/NAL bitrate constraints depend on profile, tier and level, not resolution alone.

Pinned upstream findings can be checked against
[capture.go](https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/capture.go),
[capture_test.go](https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/capture_test.go),
[video_codec.go](https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/video_codec.go),
[receiver_media.go](https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/receiver_media.go)
and [capture_broadcast.go](https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/capture_broadcast.go).
