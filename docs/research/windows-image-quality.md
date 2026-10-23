# Windows capture and image quality

Research date: 2026-10-20. Scope: Windows 10/11 x86-64 screen sending to an AirPlay receiver from Aircast's Go + C/CGO application.

## Recommendation

Use Desktop Duplication or Windows Graphics Capture, keep the source at physical pixel resolution, scale once, convert color explicitly, then encode H.264 8-bit 4:2:0. Prefer a tested encoder on the capture GPU. Keep a software encoder fallback without changing the capture API.

A bundled FFmpeg executable can do this. `ddagrab` is native DXGI capture, not GDI. There is no reason to ship `gdigrab` as the normal quality path just because FFmpeg is easier to bundle. Start with one SDR monitor and a receiver-approved canvas, normally up to 1080p30 for the first compatibility profile. Do not call that canvas the receiver's maximum, or claim 60 fps without measuring distinct moving frames.

The eventual efficient path is `D3D11 capture -> GPU scale/color conversion -> same-adapter hardware encoder -> timestamped access units`. GStreamer already exposes these pieces through its C API. A CPU readback and conversion path can preserve SDR image quality, but its CPU use and sustainable frame rate must pass Windows tests before release.

Do not ship two media engines in the first release. If Windows GStreamer packaging is practical, its D3D11 path fits the reference sender's timestamped pipeline best. If FFmpeg gets casting into users' hands sooner, use the explicit-conversion path below, report the CPU staging, and retain a path to GPU conversion. Neither choice needs a custom capture implementation on day one.

## Evidence and limits

- Microsoft, FFmpeg, NVIDIA and GStreamer documentation and source were inspected. FFmpeg flag examples target the publicly available `n8.1.2` source. GStreamer examples require 1.26 or newer and a pinned, inspected plugin build.
- The local DoubleTake reference was inspected at `5e2f1cbe3e1070bcefec8e549c7baf884727bc00`. Relevant source findings appear below.
- No Windows capture, GPU encoding, receiver playback, or benchmark was run for this research. No Mac build was run. Commands, bitrates, pass criteria and implementation order below are proposals, not verified working configurations.
- Linux builds can verify cross-compilation, codec/parser fixtures and synthetic encode/decode results. They cannot verify Windows D3D11 drivers, mixed-DPI capture, hardware encoders, or the test TV. The later Windows and receiver test is a release gate.

## Capture choices

| Path | Documented/source-established behavior | Aircast decision |
| --- | --- | --- |
| Native DXGI Desktop Duplication | Returns a GPU surface per output, dirty/move rectangles, cursor metadata and frame timing. Legacy `DuplicateOutput` returns BGRA8. `DuplicateOutput1` permits supported higher-color formats. The device must belong to the output's adapter. Rotation needs handling. [1][2] | Good monitor capture. Use the selected output, not an unbounded virtual desktop. Do not implement dirty-rectangle reconstruction unless needed; the full acquired image is available. |
| Native Windows Graphics Capture | Captures a monitor or window into D3D11 surfaces. WGC arrived in Windows 10 1803; desktop `CreateForMonitor` interop requires 1903. Frames expose `ContentSize` and QPC-based `SystemRelativeTime`. Microsoft documents FP16 capture for HDR to prevent clipping. [3][4] | Preferred route for individual windows and later HDR. Keep user-visible capture/stop controls. Retain the system border where available. WGC is not automatically higher fidelity than correctly captured SDR DXGI pixels. |
| FFmpeg `ddagrab` | A lavfi source producing D3D11 hardware frames. Software filters require `hwdownload`. `framerate` is a maximum, not a throughput guarantee; default `dup_frames=1` repeats idle images. `video_size` crops, it does not scale. [5][6] | Achievable first native-capture path with a bundled executable. Never set `video_size=1920x1080` to shrink a 4K screen. |
| FFmpeg `gfxcapture` | FFmpeg 8.1 added WGC capture. It accepts `hmonitor`/`hwnd`, `max_framerate`, output format and resize options. It also returns D3D11 frames. Requires C++17 and suitable Windows headers at build time. [7][8] | An available WGC alternative, not hypothetical future work. Probe the actual bundle. Do not assume every Windows FFmpeg build includes it. |
| GStreamer `d3d11screencapturesrc` | Supports `capture-api=dxgi` and `capture-api=wgc`, monitor/window handles, BGRA D3D11 memory and cursor control. Documented source caps are BGRA/sRGB, not an HDR contract. [9] | Most direct fit for a Go/C bridge and the existing GStreamer protocol reference. Keep D3D11 memory through conversion and encoding where supported. |
| GDI / FFmpeg `gdigrab` | `BitBlt` into a CPU DIB, then another frame copy in the inspected FFmpeg source. Supports desktop rectangles, negative desktop offsets and windows. Its DPI handling uses legacy device-cap ratios. [10][11] | Compatibility-only fallback after explicit notice. Higher CPU/memory traffic, mixed-DPI risk and no validated HDR path. No fixed universal FPS ceiling is established here. |

GDI is not inherently blurry for a static, unscaled SDR bitmap. It can capture the same pixels. The problem is that DPI virtualization, resizing, missed accelerated content, GPU readback and per-frame CPU work can make the complete path worse. Conversely, using D3D11 does not make a low-bitrate encoder sharp. Measure both capture pixels and decoded pixels.

### Details that affect implementation

- `ddagrab`'s `output_idx` indexes outputs on its D3D11 device's adapter. It is not a reliable mapping of Windows' displayed monitor number minus one on multi-GPU machines. The source explicitly calls that adapter's `EnumOutputs`. Select both adapter and output. [6]
- Native DXGI acquisition can time out on an unchanged desktop. That is not failure. Distinguish repeated idle pictures from a frozen moving source. On access loss, disconnect, resolution change or device removal, release and recreate capture. Do not continue displaying stale success status. [1][2]
- The inspected `ddagrab` source copies the desktop texture without a desktop rotation transform. Its rotation-specific code handles the cursor. Do not assume portrait is normalized merely because the pointer looks correct. Use WGC after validation, add a tested orientation transform before scaling, or explicitly reject rotated displays in the first DXGI build. [1][6]
- WGC frame-pool dimensions are not always valid content dimensions. Copy only `ContentSize`; recreate the pool on resize, otherwise clipped or undefined pixels may escape. Do not retain a checked-in frame surface after releasing its frame. [3]
- A window handle can be reused after the window closes. Validate identity, stop on closure, and never switch to a different window by silently matching its title.
- Capture runs in the interactive user session. Do not elevate to capture the secure desktop, defeat capture exclusions, or bypass protected content. A locked session, UAC secure desktop, DRM surface or minimized source is not proof of a working capture stream.

## Physical pixels, DPI and monitor rules

1. Set/verify Per-Monitor V2 DPI awareness before creating windows. Prefer a manifest and respect the GUI framework's initialization. A helper executable has its own DPI context; the Go parent's setting does not make every child DPI-aware. Restore any temporary thread context. Microsoft explicitly documents virtualized coordinates and bitmap stretching for unaware/system-aware callers. [12]
2. Enumerate monitors with physical bounds, rotation, adapter LUID, output identity and active color state. Resolve indices at session start and again after topology changes. Do not persist a DXGI ordinal or `HMONITOR` as permanent identity.
3. Use the actual acquired texture size as the capture size. A 3840x2160 display at 200% scale is still a 3840x2160 capture source, not 1920x1080. UI dimensions in device-independent units are not video dimensions.
4. Initially capture one selected monitor, with explicit selection or a clearly labelled primary-monitor default. Do not combine a 4K laptop and a 1080p external monitor into one wide image and shrink that to 1080p. Each output may have a different GPU, rotation, refresh rate, DPI and color state.
5. For an eventual desktop mosaic, preserve physical placement including negative coordinates, acquire each output on its owning adapter, and synchronize/composite before one final scale. That is outside the first release.
6. Keep the desktop's refresh rate and Windows scaling settings unchanged. Offer advice, not an automatic resolution/DPI change, if the receiver's smaller canvas makes tiny text unreadable.

### Scaling contract

Choose a coded canvas only after the receiver profile has accepted the codec, dimensions and rate. Preserve aspect ratio. Never stretch ultrawide or portrait content into 16:9, never crop edges silently, and never advertise a 4K stream made by enlarging 1080p.

For a square-pixel source `Sw x Sh` and an allowed canvas `Cw x Ch`, use `s = min(1, Cw/Sw, Ch/Sh)` for the default no-upscale policy. If shrinking, precompute even active dimensions close to `Sw*s` and `Sh*s`, not exceeding the canvas. Center them in black padding with even left/top offsets for 4:2:0. Example: 2560x1600 into 1920x1080 becomes 1728x1080 plus 96-pixel side bars. Test any rounding with corner markers.

For a source that already fits, preserve its pixels. Pad an odd source dimension to the next even value when the receiver permits it rather than discarding the last row/column. A fixed receiver canvas may require extra padding. If a receiver requires the whole canvas filled, make any upscaling explicit and report the active source resolution separately.

Scale RGB before the final 4:2:0 conversion. This avoids subsampling colored text and then resampling its already reduced chroma a second time. Prefer no scale; for shrinking, compare Lanczos or spline36 against a good GPU scaler on text, diagonals and moving detail. Lanczos can ring. Nearest-neighbor is useful for diagnostic pixel patterns, not general fractional desktop downscaling. Disable denoise, temporal smoothing, frame interpolation and sharpening by default.

The command examples use a 1920x1080 active image and canvas. Substitute precomputed active/canvas dimensions. For letterboxing, append, after conversion, for example `pad=1920:1080:96:0:color=black` to a 1728x1080 active image. Do not apply that particular pad to a full-width image. Set sample aspect ratio to `1:1`.

## Color, HDR and chroma

### Initial SDR contract

The proposed input contract is ordinary SDR sRGB RGB, full range. The proposed TV output contract is BT.709 primaries, BT.709 transfer, BT.709 matrix, limited-range YUV420, with matching bitstream VUI. Range conversion must happen once. Setting `-colorspace bt709` or `-color_range tv` alone changes signaling, not arbitrary input pixels.

The FFmpeg path below uses `zscale` to perform transfer, matrix and range conversion, rather than tagging sRGB values as BT.709. The GStreamer path uses source sRGB caps, `gamma-mode=remap`, and BT.709 output caps. Verify neutral ramps and color patches after decoding. If receiver testing establishes a different transfer convention, change the transform and tags together, not just the metadata.

Do not promise color-managed proofing. SDR capture, ICC profiles, Windows auto color management, night-light filters, receiver processing and panel calibration complicate the reference. Record active display settings. Compare to captured RGB and a defined sRGB fixture, not a photograph alone. Advanced Color includes WCG as well as HDR; hardware support and current active mode are different facts. [3][13]

### HDR is a separate feature

Microsoft warns that BGRA8 capture can clip/appear washed out with Windows HD Color enabled and recommends FP16 throughout capture, followed by appropriate tone mapping or HDR output. `DuplicateOutput1` can preserve higher-color fullscreen formats, but simply requesting 10-bit output does not implement HDR transport. [2][3]

For release one, detect active Advanced Color/HDR and either use a separately validated SDR conversion path or stop with a clear request to select an SDR source/disable HDR manually. Unknown color state is not a verified SDR state. Do not toggle the user's HDR setting automatically. Do not silently treat FP16 scRGB as PQ, and do not use a generic `tonemap` string with guessed peak/SDR-white values.

Later HDR work needs WGC FP16 or a validated DXGI higher-color path, the actual SDR white level, defined gamut/peak mapping, a 10-bit encoder input, correct transfer/color metadata, and an AirPlay HDR mode proven on that receiver. HEVC Main10 by itself satisfies none of the capture/negotiation requirements. The documented GStreamer BGRA-only screen-source caps are another reason not to claim HDR from that pipeline. [9][13]

### Text clarity has a 4:2:0 limit

NV12/I420 retain one luma sample per pixel but only one Cb and Cr sample per 2x2 area. A red/blue one-pixel glyph or a ClearType colored edge can blur even at lossless quantization. H.264 High profile is still usually 4:2:0. Higher bitrate cannot recover chroma samples that were discarded.

H.264 4:4:4/High 4:4:4 Predictive can preserve more desktop chroma locally, but is not an established AirPlay receiver capability here. HEVC Main/Main10 commonly remain 4:2:0 too. Do not enable 4:4:4, High10 or 10-bit AVC merely because the local encoder advertises them. A TV's HDMI 4:4:4 mode or its file-playback codec spec does not establish its AirPlay mirroring decoder's supported profiles.

Receiver-supported native resolution, one scale, enough bitrate and grayscale/high-contrast text are more useful than an unsupported codec. Include both ClearType and grayscale-antialiased fonts in tests. Never change the user's font-rendering settings without consent.

## Encoders and the latency/quality tradeoff

| Encoder | Practical role | Important limits |
| --- | --- | --- |
| NVIDIA NVENC | H.264 and, on capable GPUs, HEVC; D3D11 input is supported. Presets P1 to P7 trade throughput for quality. [14][15] | Capability and driver dependent. Prefer P5 with low-latency tuning initially, then measure. P7 is not automatically useful if it misses deadlines. |
| Intel Quick Sync / oneVPL | H.264 and hardware-dependent HEVC. FFmpeg `h264_qsv`/`hevc_qsv`; GStreamer QSV accepts D3D11 NV12 input. [16][17] | Probe the actual adapter, driver and rate-control mode. An Intel display does not prove that the installed runtime can encode the selected format. |
| AMD AMF | H.264/HEVC on supporting hardware. FFmpeg AMF and GStreamer `amfh264enc` support this family; GStreamer accepts D3D11 NV12. [18][19] | Explicitly bound async depth. Disable pre-analysis/lookahead and B frames for the initial latency profile. Some options vary by GPU/driver. |
| Media Foundation hardware MFT | Native Windows route if a later C backend needs OS encoder discovery. | A distinct implementation and test burden. Do not select a software MFT and label it hardware. Not needed to ship the first FFmpeg/GStreamer backend. |
| x264 | Useful quality reference and reliable software fallback. `veryfast` is a reasonable first realtime candidate. [20] | CPU and power cost. `ultrafast` spends more bits for comparable detail and is not the best-quality setting. Use only with an honest measured performance/quality label. |
| x265 | Useful offline HEVC comparisons. | Not the automatic realtime fallback on an arbitrary laptop. It can consume much more CPU and still miss the requested cadence. |

Capture -> CPU readback -> NV12 -> hardware encoder is still hardware encoding, but it is not zero-copy. A 3840x2160 BGRA60 readback alone moves about 1.99 GB/s before extra copies/conversion, versus about 249 MB/s for 1080p30. These are byte-count calculations, not measured bus throughput. Same-adapter GPU conversion avoids this recurring CPU traffic. Hybrid laptops need care: the display may be attached to the iGPU even when an NVIDIA GPU is present.

Use no B frames and no multi-frame lookahead initially. This sacrifices some compression efficiency but avoids reorder/lookahead delay. Use a two-second maximum GOP and request/send a fresh IDR with parameter sets at session start/recovery. Do not make every frame intra unless a separate measured use case requires it. Preserve adequate I-frame burst budget; a single-frame VBV can visibly damage a new slide or full-screen repaint.

### Proposed starting budgets, not receiver facts

| Accepted mode | First test settings | Policy |
| --- | --- | --- |
| H.264 1080p30 SDR | Software CRF 18 with 20 Mbit/s maximum and 4 Mbit VBV. Hardware VBR 12 Mbit/s target, 20 Mbit/s maximum, 4 Mbit VBV. GOP 60. | First compatibility-quality target. Negotiate a lower canvas if the receiver requires it. |
| H.264 1080p60 SDR | Hardware VBR 20 Mbit/s target, 30 Mbit/s maximum, 6 Mbit VBV. GOP 120. | Enable only after 60 fps receiver acceptance, moving-frame cadence and latency tests. 1080p60 needs a sufficient H.264 level, commonly 4.2, not the 4.1 example below. |
| HEVC SDR | Begin experiments at the same canvas/rate as proven AVC, then compare at equal bitrate and equal quality. | Diagnostic opt-in until transport/decoder compatibility is proved. Do not assume a fixed percentage bitrate saving for desktop text. |

These are LAN trial budgets. Apply any lower receiver/transport cap. High bitrate can increase Wi-Fi queueing or exceed a receiver's decoder envelope. Show a warning or offer an explicit lower mode when the link cannot sustain the selected quality. Do not silently switch to 720p or 15 fps under a label saying "best quality".

### FFmpeg GPU-path traps found in source

1. The documented shortest example, `ddagrab -> h264_nvenc`, passes packed RGB to NVENC. FFmpeg 8.1.2's wrapper sets BT.470BG matrix signaling and limited-range flags for packed RGB, rather than simply honoring a requested BT.709 matrix. Do not claim controlled BT.709 conversion from that example. Feed explicitly converted NV12 for this release. [15]
2. FFmpeg's NVENC `-cq` target-quality mode explicitly clears average bitrate and VBV buffer size while retaining maximum bitrate. Therefore `-cq 18 -b:v 12M -bufsize 4M` is not the bounded-VBV configuration its flags appear to promise. Use ordinary VBR below for the latency profile. Test `-cq 18` as a separate capped-quality experiment, without claiming the 4 Mbit VBV still applies. [15]
3. FFmpeg 8.1.2 also has `scale_d3d11=width=1920:height=1080:format=nv12`. In the inspected implementation, the video-processor path does not explicitly set stream/output color spaces and copies input frame properties to the converted output. Treat its RGB-to-NV12 color handling as a review/test risk, not as proof of a wrong picture. Do not replace the explicit transform below with that shorter GPU graph and claim color equivalence without decoded-patch validation. GStreamer's explicit colorimetry/gamma conversion is the better-defined candidate here. [27]

## Exact candidate commands

All commands below are unexecuted Windows test candidates. Use the pinned executable by absolute path from Go, pass arguments directly rather than through `cmd.exe`, bound stderr, and drain media/stderr concurrently. Do not accept arbitrary command fragments or filter strings from a receiver. Examples are single-line commands to avoid shell-continuation differences.

### FFmpeg native capture with a software encoder

Assumptions: adapter 0/output 0, unrotated 16:9 SDR source at least 1920x1080, approved 1920x1080 active/coded canvas, H.264 High level 4.1 accepted by this receiver. The exact color conversion requires a bundle built with `libzimg`.

```text
ffmpeg.exe -hide_banner -nostdin -loglevel warning -init_hw_device d3d11va=cap:0 -filter_hw_device cap -filter_complex "ddagrab=output_idx=0:framerate=30:draw_mouse=1:dup_frames=1:output_fmt=bgra,hwdownload,format=bgra,format=gbrp,zscale=w=1920:h=1080:filter=lanczos:primariesin=bt709:transferin=iec61966-2-1:matrixin=gbr:rangein=full:primaries=bt709:transfer=bt709:matrix=bt709:range=limited:chromal=left:dither=ordered,format=yuv420p,setsar=1[v]" -map "[v]" -an -c:v libx264 -preset veryfast -tune zerolatency -crf 18 -maxrate 20M -bufsize 4M -g 60 -bf 0 -profile:v high -level:v 4.1 -x264-params "scenecut=0:open-gop=0:repeat-headers=1:aud=1" -color_range tv -colorspace bt709 -color_primaries bt709 -color_trc bt709 -chroma_sample_location left -fps_mode passthrough -flush_packets 1 -f h264 pipe:1
```

This is CPU color conversion and software encoding with native GPU capture, not a 60 fps promise. Change the explicit dimensions according to the scaling contract, including no-upscale padding. Omit neither range/transfer conversion nor source identity checks. Changing `-profile:v`/`-level:v` must follow the receiver's accepted mode, not a local encoder preference.

Do not add `-r 60`, `fps=60`, or `-re` to "fix" a slow capture. They cannot invent distinct frames and may add repeated pictures or buffering. Source capture is already paced. `-fps_mode passthrough` avoids an extra output CFR duplication layer; protocol timestamps still need handling.

### FFmpeg hardware substitutions

Keep the same capture and color transform, but replace the final `format=yuv420p` with `format=yuv420p,format=nv12`. The latter is a plane-layout conversion, not a second chroma downsample. Remove the x264-specific encoder flags and select one encoder block below. Keep output color flags, `-fps_mode passthrough` and the output sink. All blocks are H.264 1080p30 trials.

```text
-c:v h264_nvenc -preset p5 -tune ll -rc vbr -b:v 12M -maxrate 20M -bufsize 4M -g 60 -bf 0 -rc-lookahead 0 -zerolatency 1 -delay 0 -aud 1 -profile:v high -level:v 4.1
```

```text
-c:v h264_qsv -preset medium -b:v 12M -maxrate 20M -bufsize 4M -g 60 -bf 0 -look_ahead 0 -async_depth 1 -idr_interval 0 -aud 1 -profile:v high -level:v 4.1
```

```text
-c:v h264_amf -usage lowlatency -quality quality -rc vbr_peak -b:v 12M -maxrate 20M -bufsize 4M -g 60 -bf 0 -preanalysis 0 -async_depth 1 -aud 1 -profile:v high -level:v 4.1
```

Probe each block independently. QSV may require an explicitly initialized oneVPL/D3D11 child device for the selected Intel adapter; do not confuse its ordinal with an NVIDIA CUDA ordinal. AMF may not accept all options on old drivers. Inspect the encoded stream for actual profile, level, B frames, IDR spacing and VUI. Extract/cache parameter sets and deliver them on each recovery even if a particular encoder does not repeat them. Do not blindly pass x264's `repeat-headers` option to another encoder.

For a quality-only QSV experiment, use `-global_quality 18 -look_ahead 0` without treating that ICQ mode as a capped-VBV equivalent. A bounded VBR comparison is easier to interpret across vendors. CRF, CQ, ICQ and QP numbers are not equal quality scales.

For a later, receiver-approved SDR HEVC NVENC trial, replace the NVIDIA block with this block and use an HEVC-aware transport/parser. Do not just rename the output file or keep AVC configuration records.

```text
-c:v hevc_nvenc -preset p5 -tune ll -rc vbr -b:v 12M -maxrate 20M -bufsize 4M -g 60 -bf 0 -rc-lookahead 0 -zerolatency 1 -delay 0 -aud 1 -profile:v main
```

Use `-f hevc` for a raw diagnostic file/pipe. `hevc_qsv` and `hevc_amf` are other hardware candidates, but they need their own inspected option set and receiver test. Keep software x265 out of automatic fallback.

### WGC and GDI alternatives

With an FFmpeg 8.1.2 build containing WGC, this source can replace the `ddagrab=...` portion before `hwdownload`:

```text
gfxcapture=hmonitor=123456:max_framerate=30:capture_cursor=1:display_border=1:output_fmt=bgra
```

`123456` is a placeholder for a currently validated `HMONITOR`, not a literal usable monitor ID. The option is `max_framerate`, not `framerate`. Do not force `width`/`height` at this capture stage when scaling later. If using WGC's built-in resize instead, require `resize_mode=scale_aspect` and test `scale_mode=bicubic`; never accept its default crop behavior as aspect-preserving scaling. Idle/event-driven timing and border availability need Windows verification. [7][8]

Only if DXGI/WGC cannot work, an explicitly labelled compatibility source for a known physical 1920x1080 monitor at `0,0` is:

```text
-f gdigrab -framerate 30 -draw_mouse 1 -offset_x 0 -offset_y 0 -video_size 1920x1080 -i desktop
```

Use a normal `-vf` with the SDR transform from `format=gbrp` onward, not `hwdownload`. Replace coordinates/dimensions using the selected monitor's verified physical bounds. A left/up monitor needs negative offsets. Do not default to `-i desktop` without a rectangle on a multi-monitor system. Mixed-DPI verification is mandatory, because the inspected GDI source does not establish a per-monitor correctness guarantee. [10][11]

### GPU-native GStreamer candidate

This is the preferred efficient comparison to the FFmpeg CPU-staging path. It requires GStreamer 1.26+ plugins with these properties, a selected same-adapter NVIDIA encoder and a 16:9 source at least as large as the approved canvas. `monitor-index=0` is a test placeholder; production should use the validated `monitor-handle`.

```text
gst-launch-1.0.exe -q d3d11screencapturesrc capture-api=dxgi monitor-index=0 show-cursor=true ! "video/x-raw(memory:D3D11Memory),format=BGRA,framerate=30/1,colorimetry=sRGB" ! queue max-size-buffers=1 max-size-bytes=0 max-size-time=0 leaky=downstream ! d3d11convert add-borders=true method=anisotropic gamma-mode=remap ! "video/x-raw(memory:D3D11Memory),format=NV12,width=1920,height=1080,pixel-aspect-ratio=1/1,colorimetry=bt709,chroma-site=mpeg2" ! nvd3d11h264enc preset=p5 tune=low-latency rc-mode=vbr bitrate=12000 max-bitrate=20000 vbv-buffer-size=4000 gop-size=60 bframes=0 rc-lookahead=0 zerolatency=true repeat-sequence-header=true aud=true ! "video/x-h264,profile=high" ! h264parse config-interval=-1 ! "video/x-h264,stream-format=byte-stream,alignment=au" ! filesink location=trial.h264 sync=false
```

The source, `d3d11convert`, NVENC, Intel QSV and AMD AMF elements document compatible D3D11 memory caps. That establishes a possible GPU-memory pipeline, not measured zero-copy operation or proven adapter selection. [9][17][19][21][22]

`method=anisotropic` is a documented D3D11 sampling choice, not Lanczos. Compare its downscaling against the CPU Lanczos reference before calling it equal in quality. `add-borders=true` preserves aspect but can upscale a smaller source into a requested large canvas; the application must still enforce the no-upscale policy. For that case, preserve a smaller negotiated canvas or explicitly composite at the intended active size. Do not let caps negotiation override the geometry contract.

For WGC, use `capture-api=wgc` and a validated monitor/window handle. The `adapter` property applies to WGC; do not assume it selects a DXGI capture device. Keep BGRA/sRGB input assumptions only for the validated SDR case. [9]

The GPU input can instead feed these documented H.264 candidate blocks:

```text
qsvh264enc rate-control=vbr bitrate=12000 max-bitrate=20000 gop-size=60 idr-interval=0 b-frames=0
```

```text
amfh264enc usage=low-latency preset=quality rate-control=vbr bitrate=12000 max-bitrate=20000 gop-size=60 b-frames=0 pre-analysis=false
```

Retain the parser/output caps, probe actual encoder latency and query available properties. Do not assume NVIDIA property names such as `bframes` or `rc-mode` apply to QSV/AMF. In native integration, configure the selected device/context and query adapter LUID where exposed; multi-GPU registration names can differ.

A CPU GStreamer fallback should download RGB, perform any resizing before 4:2:0, convert with explicit colorimetry, then use, as a first test:

```text
x264enc speed-preset=veryfast tune=zerolatency pass=qual quantizer=18 bitrate=20000 vbv-buf-capacity=200 key-int-max=60 bframes=0 rc-lookahead=0 sliced-threads=true byte-stream=true aud=true option-string=scenecut=0:open-gop=0
```

`vbv-buf-capacity` is milliseconds, whereas NVENC's `vbv-buffer-size` is kbits and FFmpeg's `-bufsize 4M` is bits. Do not substitute one number for another. GStreamer documents `pass=qual` as constant quality and the `bitrate` property as its maximum. Validate the actual encoder output, not just property acceptance. [20][22]

## Timestamps, queues and process boundaries

The raw `-f h264 pipe:1` and `filesink` examples above are codec/pixel diagnostic outputs. They are not a complete AirPlay transport. Annex-B start codes and AUDs give NAL/access-unit framing, not capture timestamps. Do not label pipe-read time as capture time.

For a C/GStreamer integration, pull encoded AU-aligned samples from an `appsink`, retain their PTS and pipeline-clock mapping, and copy encoded bytes before releasing the sample. Use a one-frame leaky raw queue before the encoder. After encoding, do not drop arbitrary P frames. Bound the transport backlog, fail/recover at an IDR when necessary, and refresh codec configuration.

The inspected DoubleTake already sends `rtph264pay -> rtponviftimestamp -> rtpstreampay -> fdsink`. Its reader preserves AU boundaries and timestamps through RFC4571 framing. Reuse its timestamp contract only if the Windows runtime/clock mapping is verified; `gst-launch` and Unix pipe assumptions need porting. A C appsink bridge can avoid that subprocess boundary entirely. [23]

If choosing FFmpeg as a subprocess, use a timestamp-preserving container plus an existing reviewed demuxer, or a narrow native helper that returns encoded packet bytes, PTS/timebase, keyframe status and codec configuration. For a local NUT packet pipe the sink flags are:

```text
-flush_packets 1 -f nut -write_index 0 pipe:1
```

Demux those packets before AirPlay framing. This does not make NUT an AirPlay payload. Do not add `-syncpoints none` casually; FFmpeg 8.1.2 treats the extra NUT syncpoint modes as experimental. [24]

Relative packet PTS still needs a defined source-clock anchor. Stock `ddagrab` timestamps acquisition/poll time using its relative clock; it does not export the original `LastPresentTime` as its frame PTS. Preserve those PTS rather than reconstructing `frameNumber/fps`. Do not claim exact presentation-time capture or exact A/V sync from process-start time or pipe-arrival time. WGC's documented QPC timestamp or a verified GStreamer/native clock mapping is the stronger long-term contract. [3][6]

Never grow queues to hide a slow encoder. Record source timestamp, AU-ready timestamp and enqueue/send age. An idle duplicate is valid; a duplicated counter while the source moves is not. A zero-latency encoder flag does not remove receiver buffering, network queueing or a blocked stdout reader.

## Safe hardware probing and fallback

1. Use only a pinned bundle from the application directory, not an executable found on `PATH` or downloaded during casting. Record version, build configuration and executable hash. Keep a redistributable manifest and matching source/license material.
2. Enumerate compile-time filters/encoders, then classify them as present, not working. For FFmpeg, inspect `-filters`/`-encoders` and the selected filter/encoder's option listing. For GStreamer, inspect factories and properties. Presence alone does not prove a usable driver session.
3. After the user chooses a source and starts casting, preflight locally before committing a receiver stream. Test a short synthetic encode on candidate hardware, decode its output, and validate its bitstream. Then run the actual capture -> conversion -> encode path at the selected size/rate. Use an overall timeout and first-AU deadline, terminate hung children, and cap output sizes. Screen probes require the same visible capture consent as normal capture.
4. Prefer an encoder on the capture adapter. Try another GPU only through a deliberate measured CPU/cross-adapter transfer path. Do not infer compatibility from vendor names. A synthetic encode on a detached GPU is not proof that a D3D11 capture texture can reach it.
5. Require decoded frames, expected dimensions/chroma/profile, current moving source content, sufficient cadence and a healthy bounded queue. Exiting zero or producing an SPS alone is not a pass. A short successful preflight also does not replace a sustained soak.
6. If a hardware encoder cannot initialize, retain DXGI/WGC and try the software block at the same geometry and rate. If it cannot sustain that mode, offer a clearly named lower mode or stop. `gdigrab` is an independent capture fallback, not an automatic consequence of hardware encoder failure.
7. Treat access denial, lock and protected content separately from unsupported hardware. Never retry with privilege escalation. On device loss, stop the old stream, re-enumerate and reconfigure once within a bounded recovery policy. Avoid infinite encoder restart loops.
8. Codec switches, dimensions and profile changes require matching receiver setup and fresh parameter sets/IDR. Do not change H.264 to HEVC mid-session merely because it encodes locally.

Cache successful hardware information only against adapter identity, driver version, executable hash and tested mode. Reprobe after those change or after device loss. No sensitive desktop frames should be saved during routine preflight. Diagnostic clips require explicit opt-in and local retention controls.

For example, inventory the bundled executable before trying its native path:

```text
ffmpeg.exe -hide_banner -filters
ffmpeg.exe -hide_banner -encoders
ffmpeg.exe -hide_banner -h filter=ddagrab
ffmpeg.exe -hide_banner -h encoder=h264_nvenc
```

Then run a synthetic NVENC initialization/packet test into a fresh, bounded temporary directory. This bypasses screen capture and color conversion deliberately, so a pass proves only this encoder configuration, not capture, quality or realtime throughput:

```text
ffmpeg.exe -hide_banner -nostdin -v error -f lavfi -i "testsrc2=size=1920x1080:rate=30,format=nv12" -frames:v 90 -an -c:v h264_nvenc -preset p5 -tune ll -rc vbr -b:v 12M -maxrate 20M -bufsize 4M -g 60 -bf 0 -rc-lookahead 0 -zerolatency 1 -delay 0 -aud 1 -profile:v high -level:v 4.1 -f nut probe-nvenc.nut
```

```text
ffmpeg.exe -hide_banner -nostdin -v error -xerror -i probe-nvenc.nut -an -f null -
```

```text
ffprobe.exe -v error -count_frames -select_streams v:0 -show_entries stream=nb_read_frames,width,height,profile,level -of json probe-nvenc.nut
```

Require 90 decoded frames and the selected geometry/profile before proceeding to the real capture test. Apply a parent-enforced timeout even though the input is finite. Delete only that application-owned probe directory after extracting non-image diagnostics. Do not use arbitrary user paths or overwrite an existing file.

Example truthful session status: `DXGI monitor 2, captured 3840x2160, active 1920x1080, H.264 High 4:2:0 SDR, NVIDIA encoder, 30 requested / 29.9 distinct moving fps`. Do not describe the example as observed data.

## What the DoubleTake reference does and does not establish

The inspected reference is Linux-oriented. It is useful protocol and framing evidence, not Windows capture validation. [23]

- `video_codec.go` keeps automatic selection on H.264 at the receiver's nominal `MirrorSize`. HEVC is an explicit override. Feature bit 42 is a source-level gate, not a successful HEVC decode test. Its higher-resolution path uses receiver maxima only after that explicit codec choice.
- `receiverScaleStages` chooses exact even receiver canvas dimensions and `videoscale add-borders=true`. Preserve its aspect/even-dimension intent, but add a no-upscale policy and choose a deliberate scaler. Its ordinary pipeline converts to 4:2:0 before `videoscale`; Windows can improve this order by scaling RGB before conversion.
- The default encoder selector mainly checks element availability. Aircast needs actual preflight encode/decode and capture tests before a hardware-working label.
- At 1920x1080x30 its automatic bitrate formula produces about 4147 kbit/s. That is a compatibility budget, not evidence of best desktop-text quality. Aircast should compare higher tested budgets rather than inherit it as the quality ceiling.
- `vbvBufferKbit` calculates kbits, but the x264 path passes that number to `vbv-buf-capacity`, documented in milliseconds. For 4147 kbit/s at 30 fps, roughly 276 kbits intended as two frames becomes 276 ms through that property. Audit units rather than copying the number. This is a source/documentation finding, not a measured receiver bug.
- `capture_frame.go` and `appendGstVideoEncoding` preserve timestamps and AU framing. Preserve that property when replacing the Linux media producer.

## Achievable first release

1. Ship real casting, not another discovery-only executable. Limit the first Windows quality claim to one chosen unrotated SDR monitor, H.264 8-bit 4:2:0 and the receiver-approved nominal canvas. Show input and output dimensions separately. Reject unsupported HDR/rotation instead of sending wrong pixels.
2. Pick one engine. Prefer the D3D11 GStreamer C pipeline if its Windows deployment is ready. Otherwise bundle pinned FFmpeg with `ddagrab`, `hwdownload`, `zscale`, x264, and only the hardware encoders that the bundle can probe safely. Keep the explicit color path even if it costs more CPU initially.
3. Implement bounded cancellation, source identity/DPI checks, timestamped AU delivery and a visible stop action. Keep raw frame queues to one pending frame. Do not implement all-monitor stitching, virtual displays, HDR or automatic HEVC yet.
4. Cross-build and run codec/geometry/framing fixtures only on a disposable Linux VM. Record those as synthetic/Linux results. Packaging a Windows binary is not a Windows runtime pass.
5. Give the maintainer the casting build for Windows/TV testing with a short test checklist and measured diagnostics. Require the normal native path to pass moving 1080p30 and text/color tests. A GDI-only, low-FPS or blurry result is an experimental build, not the quality release.
6. After the first verified mode, add same-adapter GPU conversion and 60 fps only where they pass. Then investigate WGC windows/rotation, receiver-tested higher resolution/HEVC, and finally HDR. No codec or resolution label should get ahead of decoded receiver evidence.

Distribution must account for the actual FFmpeg/x264 build license. Enabling libx264 normally makes FFmpeg a GPL build; putting it in a separate executable does not erase that executable's distribution obligations. Preserve exact source, configuration, notices and hashes and follow the project's licensing review. Avoid `--enable-nonfree` bundles. [25]

## Validation fixtures and measurements

### Fixtures

Generate deterministic reference assets on the build machine. The later Windows test must show them at an actual one-image-pixel-to-one-display-pixel size, without browser/image-viewer scaling. Use a small DPI-aware fixture viewer or verified native-size presentation; a screenshot of a scaled browser is not a pixel reference.

| Fixture | What it reveals |
| --- | --- |
| 1920x1080 and 3840x2160 RGB PNGs with corner labels, rulers, 1/2/4-pixel grids, circles and diagonal lines | Cropping, wrong monitor, aspect stretch, rounding, rotation, fractional scaling and ringing. |
| Light/dark editor, spreadsheet and browser-like text at 8/10/12/16/24 px, grayscale and ClearType examples, red/blue/cyan glyphs | Compression damage versus unavoidable chroma loss. Retain separate text ROIs for scoring. |
| Full-range neutral ramp, near-black/near-white steps, primary/secondary patches, skin-tone patches and smooth gradients | Full/limited-range errors, BT.601/709 mismatch, transfer error, clipping and banding. |
| Deterministic 30/60 fps clip with frame ID/counter, moving thin lines, scrolling code, dragging windows and full-screen slide cuts | Distinct-frame cadence, stutter, I-frame quality, backlog and source age. A static desktop cannot certify 60 fps. |
| Two monitors at 100%/150%/200%, one left/up of primary; switch primary and reconnect a dock | DPI virtualization, negative coordinates, monitor identity and adapter selection. Include a portrait monitor when rotation support is claimed. |
| HDR desktop/video fixture with known SDR white and highlights | Negative release-one gate. Later tests must check tone mapping, not merely that ten bits were encoded. |

### Separate the errors

Keep three comparison points: captured RGB, ideal resized/color-converted 4:2:0 reference, and decoded encoded video. Compare capture to the native fixture to find capture/DPI mistakes. Compare decoded YUV to the ideal YUV reference to isolate encoding loss. Compare the complete reconstructed RGB result to the original to expose resampling/chroma/color losses that an encoder-only score excludes.

An RGB -> 4:2:0 -> RGB round trip cannot be pixel-identical on colored one-pixel patterns. Do not fail the capture API for that expected loss or use a whole-frame average to hide unreadable small text.

### Measurements and proposed gates

- Parse/decode the entire trial with no decoder errors. Check actual SPS/VUI/profile/level, coded and displayed dimensions, SAR, pixel format, color tags and keyframe spacing. Metadata checks do not prove the pixel conversion is correct.
- On the unscaled SDR fixture with cursor outside the ROI, demand exact corner/edge coverage, correct orientation and no geometric resampling. Compare raw capture pixels to the fixture with a recorded tolerance for any documented Windows color processing.
- Score aligned Y, U and V planes with PSNR and SSIM. Report averages, worst frame and text-ROI results. As initial thresholds to calibrate, try text-ROI luma SSIM >= 0.98 and luma PSNR >= 40 dB on the static fixture against the ideal same-size 4:2:0 reference. These numbers are proposals, not accepted AirPlay quality standards.
- Add OCR character error rate on a known text corpus and inspect 200-400% nearest-neighbor crops. OCR success alone is too weak; colored fringes or hard-to-read punctuation can remain. Compare code punctuation, thin strokes and small spreadsheet numbers manually.
- Measure decoded patch means and ramp endpoints. Compare against the defined transform; inspect near-black/white step separation and clipping. Use a color-managed RGB reconstruction for Delta E comparisons if adding a color metric. Do not compute Delta E directly on gamma-coded YUV samples.
- For a moving fixture, require at least 29 distinct frames/s for a 30 fps claim and 58 for a 60 fps claim over a 60-second trial, with no sustained freeze or growing source age. Record dropped/missed IDs, repeat count and p95/p99 inter-frame gaps. These are proposed release gates, not measurements already achieved.
- Separately record capture-to-AU-ready p50/p95/p99 and maximum queue age. Initial local targets are p95 <= 100 ms at 30 fps and <= 50 ms at 60 fps, without latency growing during a ten-minute soak. Do not equate VBV duration with an inevitable fixed delay or encoder flags with measured delay.
- Measure CPU, memory, GPU video-encode/copy/3D utilization, power/thermal throttling and bitrate peaks during the same fixtures. Compare plugged-in and battery policy where applicable. Averages can hide slide-cut stalls.
- Measure glass-to-glass with a visible frame counter on source and TV using a high-frame-rate camera. Report receiver model/firmware, source hardware/driver, network, mode and TV processing settings. Sender decode validation is necessary but does not prove receiver image quality. Receiver/network buffering must be reported separately from local encode time.

VMAF can supplement motion/video comparisons, but it is not the release criterion for desktop text. Record the exact model/version if used. Likewise, do not accept "4K", a bitrate number, a clean log, or a successful encoder process as a quality measurement.

### Inspection commands

Use a timestamped local diagnostic capture only with explicit opt-in. These are examples for a captured NUT file, not commands run during this research.

```text
ffprobe.exe -v error -select_streams v:0 -show_entries stream=codec_name,profile,level,width,height,pix_fmt,sample_aspect_ratio,color_range,color_space,color_transfer,color_primaries,chroma_location,r_frame_rate,avg_frame_rate -of json capture.nut
```

```text
ffmpeg.exe -hide_banner -v error -xerror -i capture.nut -an -f null -
```

For offline fixtures with already aligned frames, identical dimensions and matching 4:2:0 color interpretation:

```text
ffmpeg -i decoded.mkv -i reference420.mkv -lavfi "[0:v]setpts=PTS-STARTPTS[d];[1:v]setpts=PTS-STARTPTS[r];[d][r]ssim=stats_file=ssim.log" -an -f null -
```

```text
ffmpeg -i decoded.mkv -i reference420.mkv -lavfi "[0:v]setpts=PTS-STARTPTS[d];[1:v]setpts=PTS-STARTPTS[r];[d][r]psnr=stats_file=psnr.log:stats_version=2" -an -f null -
```

Align by embedded frame ID before scoring live captures. Resetting PTS alone does not repair dropped frames, and adding an `fps` filter to hide those drops invalidates cadence measurement. FFmpeg documents the PSNR/SSIM inputs and matching-format requirements. [26]

## Sources

1. Microsoft, Desktop Duplication API, including rotation and cursor handling: https://learn.microsoft.com/en-us/windows/win32/direct3ddxgi/desktop-dup-api
2. Microsoft, `IDXGIOutput5::DuplicateOutput1`, adapter requirements and higher-color formats: https://learn.microsoft.com/en-us/windows/win32/api/dxgi1_5/nf-dxgi1_5-idxgioutput5-duplicateoutput1
3. Microsoft, screen capture, HDR/FP16 warning, content size, surface lifetime and QPC timestamp: https://learn.microsoft.com/en-us/windows/apps/develop/media-authoring-processing/screen-capture
4. Microsoft, WGC OS history and desktop monitor interop: https://learn.microsoft.com/en-us/uwp/api/windows.graphics.capture.graphicscapturesession and https://learn.microsoft.com/en-us/windows/win32/api/windows.graphics.capture.interop/nf-windows-graphics-capture-interop-igraphicscaptureiteminterop-createformonitor
5. FFmpeg, `ddagrab` documentation: https://ffmpeg.org/ffmpeg-filters.html#ddagrab
6. FFmpeg 8.1.2, exact DXGI source, output enumeration, PTS and pixel copy: https://github.com/FFmpeg/FFmpeg/blob/n8.1.2/libavfilter/vsrc_ddagrab.c . Device selection: https://github.com/FFmpeg/FFmpeg/blob/n8.1.2/doc/ffmpeg.texi
7. FFmpeg, WGC documentation and release history: https://ffmpeg.org/ffmpeg-filters.html#gfxcapture and https://github.com/FFmpeg/FFmpeg/blob/n8.1.2/Changelog
8. FFmpeg 8.1.2, WGC exact options, implementation and build dependencies: https://github.com/FFmpeg/FFmpeg/blob/n8.1.2/libavfilter/vsrc_gfxcapture.c , https://github.com/FFmpeg/FFmpeg/blob/n8.1.2/libavfilter/vsrc_gfxcapture_winrt.cpp and https://github.com/FFmpeg/FFmpeg/blob/n8.1.2/configure
9. GStreamer, D3D11 capture APIs, properties and BGRA/sRGB caps: https://gstreamer.freedesktop.org/documentation/d3d11/d3d11screencapturesrc.html
10. FFmpeg, GDI input and negative offsets: https://ffmpeg.org/ffmpeg-devices.html#gdigrab
11. FFmpeg 8.1.2, GDI BitBlt/copy and DPI calculations: https://github.com/FFmpeg/FFmpeg/blob/n8.1.2/libavdevice/gdigrab.c
12. Microsoft, DPI awareness, virtualization and PMv2 testing: https://learn.microsoft.com/en-us/windows/win32/hidpi/high-dpi-desktop-application-development-on-windows
13. Microsoft, Advanced Color/HDR/WCG state, SDR white and conversion considerations: https://learn.microsoft.com/en-us/windows/win32/direct3darticles/high-dynamic-range
14. NVIDIA, NVENC capabilities, D3D11 input, rate control, presets and threading: https://docs.nvidia.com/video-technologies/video-codec-sdk/13.0/nvenc-video-encoder-api-prog-guide/index.html
15. FFmpeg 8.1.2, NVENC options and implementation, including CQ/VBV handling and packed-RGB VUI: https://github.com/FFmpeg/FFmpeg/blob/n8.1.2/libavcodec/nvenc_h264.c , https://github.com/FFmpeg/FFmpeg/blob/n8.1.2/libavcodec/nvenc_hevc.c and https://github.com/FFmpeg/FFmpeg/blob/n8.1.2/libavcodec/nvenc.c
16. FFmpeg 8.1.2, QSV options/rate-control decisions: https://github.com/FFmpeg/FFmpeg/blob/n8.1.2/libavcodec/qsvenc_h264.c , https://github.com/FFmpeg/FFmpeg/blob/n8.1.2/libavcodec/qsvenc.h and https://github.com/FFmpeg/FFmpeg/blob/n8.1.2/libavcodec/qsvenc.c
17. GStreamer, Intel QSV input and properties: https://gstreamer.freedesktop.org/documentation/qsv/qsvh264enc.html
18. FFmpeg 8.1.2, AMF exact H.264 options: https://github.com/FFmpeg/FFmpeg/blob/n8.1.2/libavcodec/amfenc_h264.c
19. GStreamer, AMD AMF input and properties: https://gstreamer.freedesktop.org/documentation/amfcodec/amfh264enc.html
20. GStreamer, x264 quality modes, latency warning and VBV units: https://gstreamer.freedesktop.org/documentation/x264/index.html
21. GStreamer, D3D11 conversion and inherited sampling methods: https://gstreamer.freedesktop.org/documentation/d3d11/d3d11convert.html and https://gstreamer.freedesktop.org/documentation/d3d11/GstD3D11BaseConvert.html . Gamma conversion: https://gstreamer.freedesktop.org/documentation/video/videoconverter.html
22. GStreamer, D3D11 NVENC input and encoder options: https://gstreamer.freedesktop.org/documentation/nvcodec/nvd3d11h264enc.html
23. DoubleTake pinned reference: https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/capture.go , https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/capture_frame.go and https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/video_codec.go
24. FFmpeg 8.1.2, NUT muxer options and experimental syncpoint modes: https://github.com/FFmpeg/FFmpeg/blob/n8.1.2/libavformat/nutenc.c
25. FFmpeg licensing and libx264 implications: https://ffmpeg.org/legal.html
26. FFmpeg, zscale, SSIM and PSNR documentation: https://ffmpeg.org/ffmpeg-filters.html#zscale-1 , https://ffmpeg.org/ffmpeg-filters.html#ssim and https://ffmpeg.org/ffmpeg-filters.html#psnr-1 . Exact zscale options: https://github.com/FFmpeg/FFmpeg/blob/n8.1.2/libavfilter/vf_zscale.c
27. FFmpeg 8.1.2, D3D11 video-processor scaler implementation: https://github.com/FFmpeg/FFmpeg/blob/n8.1.2/libavfilter/vf_scale_d3d11.c
