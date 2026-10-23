# AirPlay sender image fidelity

Research date: 2026-10-20. Reference: DoubleTake commit `5e2f1cbe3e1070bcefec8e549c7baf884727bc00`, inspected at `/private/var/folders/tz/ssfkzx5x60j8mlm9125n74xm0000gn/T/opencode/aircast-doubletake-reference`. No builds, receiver tests, or image-quality measurements were run for this research.

## Recommendation

Ship a good H.264 SDR path first. Target receiver-supported 1080p at 30 fps, 8-bit 4:2:0, with a starting video budget of 12 Mbit/s on a healthy LAN. Fit the source once without stretching, use correct color conversion, and preserve capture timestamps. These numbers are proposed Aircast defaults, not documented AirPlay limits or measured optima.

Do not promise 4K desktop mirroring, HDR, lossless video, or 4:4:4 text fidelity. Keep HEVC and above-1080p canvases experimental until the actual receiver, firmware, encoder, and displayed output pass the tests below. A 4K television or Apple TV 4K name does not establish a 4K mirroring path.

The most useful near-term quality improvement may be 1080p H.264 rather than HEVC. A current report against this exact pin says an Apple TV 4K advertises nominal 720p but accepts its advertised 1080p maximum over H.264. DoubleTake's automatic mode leaves that receiver at 720p. Treat a higher-than-nominal canvas as an opt-in, verified-per-receiver choice, not a universal rule. [R1]

## What the evidence establishes

Evidence labels used here:

- **Documented** means an Apple, Microsoft, or GStreamer public document says it. Its scope still matters.
- **Source fact** means the pinned implementation does it. This does not prove receiver interoperability.
- **Upstream report** means a maintainer or user describes a result. Aircast has not reproduced it.
- **Recommendation** means an engineering choice that still needs measurement.

| Question | Finding and evidence strength |
| --- | --- |
| Does Apple TV decode and output 4K60 video? | Documented for Apple TV 4K hardware. The 2022 model's specifications list SDR AVC/HEVC and HDR HEVC formats up to 2160p60. Those are general video/output specifications, not a screen-mirroring negotiation contract. [A1] |
| Does Apple's 4K AirPlay claim prove 4K desktop capture? | No. Apple's guide separates screen mirroring from video streaming and explicitly promises full 4K HDR for compatible videos sent from Photos. WWDC19's 4K HDR discussion focuses on video apps and HLS renditions. Neither cited document specifies a desktop-mirroring maximum, chroma format, or bitrate. [A2, A3] |
| Is 1080p30 an absolute protocol limit? | No defensible universal limit was found. H.264 at 720p/1080p is the conservative compatibility path. UxPlay documents HEVC mirroring with a 3840x2160 request and a 30 fps default, and separately documents advisory 60 fps requests. A current DoubleTake PR reports about 59 fps received by UxPlay, not an Apple TV. Old H.264-only descriptions are not a complete current protocol specification, but these results do not establish every Apple TV or smart TV can mirror 4K or 60 fps. [U1, R6] |
| Is HEVC mirroring real? | UxPlay implements `hvc1` parsing and associates feature 42 with multicodec screen support. Its historical issue #231 identified HEVC sent by a wired M1 Pro Mac when requesting more than 1080p. A DoubleTake PR reports physical AppleTV14,1 HEVC testing, but does not supply enough encoded/displayed-resolution evidence to certify 4K. These are upstream implementation/report evidence, not Aircast hardware verification. [U1, U2, U3, R2] |
| What does current Apple TV negotiation look like? | An October 2026 report on AppleTV6,2, tvOS 26.6, HDMI 1080p50, gives post-control-SETUP `widthPixels=1280`, `heightPixels=720`, maximum `1920x1080`, `maxFPS=60`. Its author reports H.264 at the maximum works. This is one configuration, not a model-wide guarantee. [R1] |
| What about smart TVs? | A TCL 55R617 Roku TV report shows a 1920x1080/30 H.264 session over Ethernet with the pinned 0.5.0 generation. Other TV reports show accepted setup without successful timing or presentation. Firmware and AirPlay implementation matter more than panel resolution or an AirPlay 2 badge. [R3, R4] |
| Does accepting SETUP prove quality or even a live picture? | No. Upstream reports include accepted PTP setup with a frozen picture, and live transmission that freezes while audio continues. Transport success is not presentation success. [R4, R5] |

For Apple TV HD/older Apple TVs, assume H.264 and the advertised canvas, usually a 720p/1080p class mode, until tested. For Apple TV 4K and licensed AirPlay TVs, use the same baseline and inspect their session-time capabilities. No public, authoritative per-model screen-mirroring matrix for resolution, fps, profile, bitrate, and chroma was found in the cited documents. A universal 4K promise and a universal 1080p30 ceiling would both overstate the evidence.

## Receiver negotiation and the pinned sender

### Read capabilities after creating the session

DoubleTake's `setupMirrorSession` starts with control-only SETUP and `combinedGetInfoWithControlSetup=true`. It consumes the returned `info` dictionary, or refreshes `GET /info` after control SETUP when usable display metadata was not returned. For a receiver that explicitly rejects control-first ordering, it refreshes after the media-first audio SETUP. Pre-session display data can be missing or provisional. Select capture/encode dimensions from that final snapshot, not just mDNS or the first plaintext `/info`. [D1]

`ReceiverInfo.DisplaySize()` prefers `displays[0].widthPixels/heightPixels`, falling back to `width/height`. `MirrorSize()` uses those nominal dimensions. Without displays, feature 7 plus feature 28 selects a provisional 1920x1080 default; feature 7 without 28 selects 1280x720. `MaxVideoSize()` prefers `widthPixelsMax/heightPixelsMax`, otherwise nominal. These fallback rules are source facts, not an Apple-published meaning for feature 28. [D2]

`selectVideo(auto, ...)` and explicit H.264 both use the nominal canvas. `AutomaticHEVCAvailable` is ignored by the final selection policy at this pin. Explicit HEVC requires feature 42 and uses the maximum canvas, aspect-fitted under 3840x2160. Therefore, this pin does not automatically promote a 720p nominal canvas to 1080p, or a 1080p nominal canvas to a 4K maximum. It also does not impose a universal 1080p H.264 ceiling when a receiver supplies a larger nominal canvas. [D3]

Important gaps when adapting this to Aircast:

- `DisplayInfo` does not parse `maxFPS`, `refreshRate`, `overscanned`, rotation, or a general video-profile/bitrate limit. Requested capture fps is not clamped to receiver `maxFPS` here. Display refresh rate, requested encoder fps, and actual decoded/presented fps are different measurements.
- `HDRInfo`, `HDRSupportedModes`, their `codecStrings`, and `receiverHDRCapability` have fields, but no selection code reads them at this pin. Feature 42 alone is the HEVC gate. Do not describe this as complete codec-profile or HDR-mode negotiation.
- A maximum is advertised capability evidence, not proof that the current mirroring route displays that raster without a further downscale. Conversely, nominal is not always the largest usable H.264 size. [R1]
- Validate dimensions together, bound hostile values, keep the source aspect ratio, normalize 4:2:0 dimensions to even values, and read the final encoded SPS. Do not combine independently invalid maxima into a new, invented aspect ratio.

### Relevant flags, without over-interpreting them

| Field/bit | What it is useful for | What it does not prove |
| --- | --- | --- |
| Feature 7, `Screen` | Screen-mirroring support. | Resolution, quality, or 60 fps. |
| Feature 28 | DoubleTake's provisional 1080p fallback selector. Public unofficial feature tables leave this bit unnamed. | A documented 4K or universal decoder limit. |
| Feature 42, `SupportsScreenMultiCodec` | Initial HEVC/multicodec screen eligibility. Both DoubleTake and UxPlay use it. | HEVC Main10, HDR, 4:4:4, 4K, or a particular bitrate by itself. |
| Feature 41, `SupportsPTP` | Timing-protocol eligibility. | Image quality or a usable clock identity in the SETUP reply. |
| Feature 59, audio stream connection setup | Initial audio descriptor shape. | Video format. One current Apple TV report accepts this shape but stays silent. [R1] |
| `supportedFormats.screenStream` | An **audio** format mask in this implementation. | A video codec, pixel format, or chroma capability. |
| `HDRInfo`/`HDRSupportedModes.codecStrings` | Potential profile/level/sample-entry constraints that Aircast should retain and inspect. | Successful displayed output without a test. |

The mDNS legacy feature string is low 32-bit word first, then high word. Preserve extended `fex`/`featuresEx` rather than truncating discovery data to 32 bits. Use receiver-advertised codec strings when available. Do not substitute a model-name guess, an AirPlay 2 badge, `SupportsAirPlayVideoV2`, or an audio bitmask for screen-codec negotiation. [D4, U4]

## H.264 and HEVC wire formats

The pinned implementation sends a 128-byte mirroring header followed by configuration or length-prefixed encoded NAL units. This is not an ordinary MP4 file transfer. [D5, U5]

| Detail | H.264 path at the pin | HEVC path at the pin |
| --- | --- | --- |
| Decoder configuration | Raw `AVCDecoderConfigurationRecord`, commonly called `avcC`, with SPS and PPS and DoubleTake's `02 00 00 00` trailer. It does **not** wrap this payload in an `avc1` visual sample-entry box. | Complete `hvc1` visual sample entry containing an `hvcC` decoder configuration, VPS, SPS, and PPS. |
| Codec packet | Type bytes `01 00`, option bytes `16 01`. | Type bytes `01 00`, option bytes `1e 01`. |
| Video payload | Four-byte big-endian NAL lengths, not Annex-B start codes. | The same length-prefixed organization. |
| Configuration source | Actual encoder SPS/PPS, including profile, level, and cropped raster dimensions. | Actual encoder parameter sets, including profile/tier/level, bit depth, chroma format, and cropped dimensions. |
| Configuration timing | Before initial decodable IDR, and when parameter sets change, at a keyframe. | Same principle, using VPS/SPS/PPS. |

Header payload length and network timestamp are little-endian. NAL lengths and BMFF box fields are big-endian. The source/destination rectangles in `sendCodecFrame` describe the actual encoded raster, not an arbitrary 4K TV size. Configuration and its associated picture must carry coherent presentation timestamps. Preserve complete access units, including multiple slices. [D5, D6]

For capability strings, `avc1` identifies AVC and `hvc1` identifies HEVC; the rest of the string carries profile/level constraints. Apple's HLS authoring examples distinguish HEVC Main `hvc1.1...` from Main10 `hvc1.2...` and recommend parameter sets in sample descriptions rather than `hev1`/in-band-only signaling. This is useful container terminology, not an instruction to replace mirroring configuration packets with an MP4 muxer or import HLS limits into mirroring. [A4]

There is a README/source mismatch worth catching. README says "HEVC Main10/high-resolution", but `selectGstHEVCEncoder()` at this pin deliberately selects 8-bit Main input: `NV12` for `nvh265enc`, `I420` for `x265enc`. Its tests explicitly check Main-profile input. The HEVC serializer can represent bit depths from the SPS; that does not make the current screen capture pipeline 10-bit or HDR. [D7]

Do not switch a live session from H.264 to HEVC without verified receiver support. UxPlay's receiver code rejects a codec change after initial selection. A bounded teardown/reconnect fallback is safer. [U3]

## Text, scaling, color, and bitrate

### The unavoidable 4:2:0 constraint

All selected DoubleTake encoders consume `NV12` or `I420` at this pin. These are 8-bit 4:2:0 layouts. Each chroma plane has half the horizontal and half the vertical sampling of luma. At 1920x1080, chroma has 960x540 samples. Changing H.264 to HEVC or increasing bitrate cannot restore discarded chroma samples. [D7, M1]

Thin saturated text, syntax highlighting, colored one-pixel lines, and RGB-subpixel-antialiased glyphs are harder than black-on-white text. A visually near-lossless encoder still follows a lossy RGB-to-4:2:0 conversion. No cited source establishes interoperable 4:4:4 or lossless screen mirroring to Apple TV/licensed AirPlay TVs. x264's ability to encode High 4:4:4, or a PC GPU's ability to encode it, is not receiver support. [G1]

Recommendations:

- Preserve the real physical capture raster, including Windows DPI scaling and Wayland fractional scaling. Portal logical dimensions are not necessarily delivered-buffer pixel dimensions. DoubleTake deliberately scales the negotiated stream rather than trusting the portal's logical size. [D8]
- For a text-focused presentation, prefer a source desktop/window rendered at the intended receiver content size over shrinking a whole 4K desktop to 1080p. Offer source/UI-scale choices; do not silently change the user's monitor mode. Upscaling a 720p source into a 4K encode does not add detail.
- Use one deliberate, aspect-preserving resample. Benchmark a high-quality scaler against the GPU scaler using text charts. Prefer scaling RGB/4:4:4 before the final chroma subsample where practical, rather than needlessly subsampling an oversized desktop first. This is a proposed quality improvement, not a measured claim about every backend.
- Set square pixels, clean black letterbox/pillarbox bars, correct orientation, and matching encoded/header dimensions. Avoid a second unnecessary resize or forced overscan. Apple's guide documents the AirPlay Display Underscan setting for cropped images and unwanted borders. [A2]
- Convert full-range desktop RGB to an explicitly chosen SDR YCbCr representation. A conservative default is BT.709 limited-range video, with converter output, SPS/VUI range/matrix/primaries/transfer, and any format-description color metadata agreeing. Test full-range only as a separate supported mode. Merely changing labels without converting pixel values causes washed-out blacks, clipped whites, or color shifts. [M1]
- On Windows HDR desktops, do not treat an 8-bit SDR capture as faithful HDR. Microsoft's capture guidance calls for float capture to avoid clipping and, when needed, explicit HDR-to-SDR tone mapping. Until the entire HDR source/conversion/encoder/receiver-mode path is implemented and tested, output honest SDR. Main10 alone is neither HDR nor 4:4:4. [M2, A5]

### DoubleTake's automatic bitrate is not a quality target

`recommendedBitrateKbps()` computes approximately `width * height * fps / 15000`, clamped to 1,800..12,000 kbit/s. H.264's automatic budget stays at or below a 1080p budget even when a larger canvas is supplied. HEVC budgets from its selected canvas. A positive explicit bitrate bypasses this automatic cap. [D9]

| Canvas/rate | Automatic video bitrate at the pin |
| --- | ---: |
| 1280x720, 30 fps | 1,843 kbit/s |
| 1920x1080, 30 fps | 4,147 kbit/s |
| 1920x1080, 60 fps | 8,294 kbit/s |
| HEVC 3840x2160, 30 fps | 12,000 kbit/s, capped from about 16,589 |
| HEVC 3840x2160, 60 fps | 12,000 kbit/s, capped from about 33,178 |

This formula does not measure source complexity, glyph preservation, actual network capacity, decoder load, or quality. Do not inherit 4.1 Mbit/s as "high quality" for desktop text, or 12 Mbit/s as an AirPlay maximum.

The pin favors latency and speed: CBR hardware paths, no B-frames, a two-second GOP, OpenH264 `usage-type=screen`, and x264/x265 `superfast` plus zero-latency tuning. GStreamer documents that x264 zero-latency tuning can reduce overall coding quality, and that presets trade speed for quality. Benchmark hardware encoders and software presets separately at the same actual bitrate. [D7, D9, G1]

One source-level unit mismatch must not be copied into Aircast. `vbvBufferKbit()` calculates a size in kbit, but `selectGstEncoderWithProbe()` passes it to x264's `vbv-buf-capacity`, which GStreamer defines in **milliseconds**. At 4,147 kbit/s and 30 fps it passes `276`, meaning 276 ms rather than the intended roughly two-frame, 67 ms buffer. This affects burst/latency expectations; its actual quality impact needs measurement. Keep encoder API units explicit in the C/CGO boundary. [D7, D9, G1]

## Proposed default and fallbacks

These are initial test settings, not claims of universal decoder acceptance.

| Mode | Initial choice | Admission rule |
| --- | --- | --- |
| Default quality | H.264, SDR, 8-bit 4:2:0, nominal canvas up to 1920x1080, 30 fps, 12 Mbit/s at 1080p; 6 Mbit/s at 720p. | Use post-SETUP dimensions. No B-frames, bounded queues, roughly two-second IDR interval. Verify profile/level from actual output. |
| Sharper H.264 canvas | Advertised maximum, capped at 1920x1080, same quality settings. | Explicit opt-in until actual presentation is verified for this receiver/firmware. Particularly useful for 720p nominal / 1080p maximum receivers. |
| Smooth motion | 1080p60, starting around 18..24 Mbit/s. | Receiver `maxFPS`, codec profile/level, local sustained encode throughput, and receiver presentation all pass. The HDMI refresh rate alone is insufficient. |
| HEVC at the same raster | Main 8-bit SDR, starting at 8..12 Mbit/s for 1080p30. | Feature 42, compatible advertised codec/profile where supplied, complete `hvc1/hvcC`, local encoder throughput, and hardware test. Compare quality, do not assume a fixed compression advantage. |
| Above 1080p / up to 4K | HEVC SDR at a receiver-advertised canvas, 30 fps first; test 15, 20, and 30 Mbit/s. | Experimental. Require suitable source detail, explicit receiver capability evidence, and a test proving displayed detail rather than just a 4K SPS or 4K HDMI mode. Do not assume the receiver accepts the higher bitrates. |
| Compatibility fallback | H.264 nominal canvas, 30 fps, then a receiver-supported 720p mode if needed. | Reconnect cleanly after codec/canvas rejection. Explain the selected resolution to the user. |

For HEVC sessions at 1080p or below, prefer a verified same-raster H.264 fallback rather than dropping spatial resolution immediately. An experimental 4K HEVC session should fall back to a verified H.264 mode, normally 1080p or nominal, not untested 4K H.264. For a desktop-quality mode, reduce 60 fps to 30 before sacrificing text resolution. Network pressure may still require a lower bitrate or a user-visible resolution reduction. Bound fallback attempts; a pairing failure or invalid clock is not evidence that image quality is too high.

Expose actual codec/profile, encoded dimensions, content rectangle, fps, bitrate, chroma/bit depth, and whether the receiver mode has been visually verified. Distinguish "advertised", "encoded", and "observed on receiver". Never show "4K quality" just because the device or requested canvas is 4K.

## Timestamps and latency are part of fidelity

DoubleTake preserves per-access-unit capture PTS across capture, scaling, encoding, and its RTP-over-stdout boundary. `frameTimeAt()` maps that original time to the negotiated network clock and adds the playout lead. It does not restamp old frames as newly captured. NTP video headers use a boot-relative seconds/fraction format; PTP adds the receiver timeline identity. Configuration and IDR timing must remain coherent. [D5, D10]

The pin's ordinary video/audio leads are 75/85 ms. Its detected wireless profile uses 100/170 ms. The low profile is 40/50 ms, but interface detection returns normal for ordinary wired interfaces, not low. These are sender scheduling leads, **not measured glass-to-glass latency**. Receiver decode, buffering, output, TV processing, and display scanout add delay. [D11]

HEVC capture calibration uses measured p95 source age plus a delivery margin of at least 67 ms, with a 500 ms automatic lead ceiling. A prepared HEVC path can raise video and audio leads together before committing media setup. The current automatic codec policy still selects H.264. Port the timing principles, not a synthetic-probe result that omits the production capture/scaler. [D1, D12]

For Aircast's C/CGO API, carry capture timestamps alongside complete encoded access units and map their clock domain explicitly. Windows Graphics Capture exposes QPC-based `SystemRelativeTime`. DoubleTake's non-Linux fallback is process-relative time, so it is not a drop-in Windows mapping for QPC or system uptime. Its Wi-Fi detection also relies on Linux `/sys/class/net`; a Windows port cannot assume that policy detects wireless routes. [M2, D11, D13]

If reusing the GStreamer stdout bridge, test large, high-bitrate keyframes. The pinned parser requires an ONVIF timestamp extension on every RTP fragment. An upstream PR reports GStreamer 1.20 adds it only to the first fragment of an access unit, so a real desktop IDR larger than the bridge's 60,000-byte MTU fails even when the synthetic test pattern works. This is a local capture-transport issue, not an AirPlay receiver bitrate limit. A native C/CGO access-unit boundary can avoid this bridge. [D10, R6]

Drop stale **raw** frames before encoding. Do not discard arbitrary encoded P-frames and continue their dependent GOP. If the network cannot keep up, lower rate, request a clean IDR, or restart. Keep queue age bounded and track capture age, encode time, socket blocking, decoded fps, presented fps, and A/V drift. A frozen receiver can keep accepting bytes. [D5, D8, R5]

## Verification matrix

Hardware verification remains open. Run this on approved Windows/Linux hosts and receivers, not by building on the Mac.

| Axis | Minimum cases |
| --- | --- |
| Receiver generation | AppleTV3,2 or Apple TV HD if available; Apple TV 4K first generation; Apple TV 4K second/third generation; at least one Samsung/LG licensed AirPlay TV; one Roku/TCL receiver. Record exact firmware and HDMI/output mode. |
| Capability shape | Missing pre-session displays; 720p nominal / 1080p maximum; 1080p nominal / 4K maximum; no feature 42; feature 42 with absent codec strings; Main-only vs Main10 strings; absent/inconsistent maxima and fps. |
| Sender | Windows 11 at 100%, 150%, and 200% DPI; Windows HDR on/off; Linux X11 and Wayland, including fractional scaling and rotated/portrait output. NVENC, Intel/AMD hardware paths, and software fallback. |
| Resolution/content geometry | 1080p source to 1080p; 4K source to 1080p; 16:10 and 3:2 sources to 16:9; portrait; ultrawide; odd source sizes; monitor/window resize while streaming. |
| Codec/rate | H.264 720p30 and 1080p30, then 1080p60; HEVC 1080p30 before testing 1440p/4K30; Main10 only when negotiated. Sweep H.264 8/12/18/24 Mbit/s and HEVC 8/12/15/20/30 Mbit/s within receiver limits. |
| Network/timing | Both ends wired; sender Wi-Fi with wired receiver; both on 5/6 GHz; weak/congested Wi-Fi; delay/loss; NTP and PTP; missing PTP timeline; 30-minute drift; idle desktop, resume, and reconnect. |

Use purpose-made sources, not only `videotestsrc`:

1. Black/white and saturated RGB text at 9, 12, 16, and 24 pixels; syntax-highlighted code; grayscale vs subpixel antialiasing where controllable. Include one-pixel luma and chroma grids, diagonal lines, circles, and marked content borders.
2. Scroll a dense page, move a window, show a cursor, run a moving timecode, and play 24/30/60 fps clips. Test static-to-motion and motion-to-static transitions. Check whether text sharpens again after motion stops.
3. Grayscale ramps and near-black/near-white patches; saturated color patches; HDR input tone-mapped to SDR. Look for lifted blacks, clipped detail, incorrect hue, banding, and uninitialized colored borders.

Validate three different stages:

- **Capture/conversion:** Save a consented source frame and the exact scaled, uncompressed 4:2:0 encoder input. Check capture dimensions, DPI, crop, stride, orientation, pixel aspect ratio, black borders, and color conversion.
- **Encoded/decoded:** Decode the actual outgoing elementary stream before encryption or a decrypted receiver recording. Check SPS/VUI, codec description, crop, chroma, bit depth, frame rate, and keyframes. Compare the decoded image against the scaled 4:2:0 input to isolate compression from capture/scaling/chroma loss. Use luma/chroma error and text-region comparisons; whole-frame SSIM alone can hide bad glyphs.
- **Presented on hardware:** Compare native-size text crops and the one-pixel resolution chart on the real TV. Use a suitable full-chroma HDMI capture or another controlled display measurement when possible; document capture-device scaling/subsampling. Prove that 4K input retains detail a 1080p intermediate cannot retain. A 3840x2160 SPS, the TV's 4K HDMI banner, or successful RTSP SETUP alone proves neither native 4K screen detail nor correct colors. Measure glass-to-glass latency with synchronized visual events/high-speed video, and A/V sync with a flash/click test.

Release gate: stable decoded and displayed image, correct geometry and colors, readable text accepted by the maintainer, no unexplained quality downgrade, no growing queue age or A/V drift, and a working fallback on each supported receiver class. Record results by receiver firmware and encoder rather than marking an entire brand "4K supported".

## Concrete DoubleTake files and functions

All paths below are relative to the pinned reference checkout. Source/test inspection is not test execution.

| File | Relevant functions or checks |
| --- | --- |
| `internal/airplay/client.go` | `ReceiverInfo`, `DisplayInfo`, `DisplayHDRInfo`, `DisplaySize`, `MirrorSize`, `MaxVideoSize`, `applyReceiverInfoUpdate`; session-time capability data and missing fps/profile enforcement. [D2] |
| `internal/airplay/video_codec.go` | `supportsVideoCodec`, `selectVideo`, `videoCanvas`, `maximumVideoCanvas`, `fitVideoSize`; automatic nominal H.264 versus forced maximum HEVC. [D3] |
| `internal/airplay/mirror.go` | `setupMirrorSession`, `prepareVideo`, `refreshSessionInfo`, `applyMeasuredCaptureLatency`; final capability refresh before encoder setup. `StreamFrames`, `spsDimensions`, `buildAVCCConfig`, `sendCodecFrame`, `sendFrame`, `frameTimeAt`, `mediaClock`; packet format, actual raster, and timing. [D1, D5] |
| `internal/airplay/mirror_hevc.go` | `parseHEVCSPS`, `buildHEVCSampleDescription`, `streamHEVCFrames`; actual `hvc1/hvcC`, bit-depth/chroma fields, parameter-set changes, and SEI preservation. [D6] |
| `internal/airplay/capture.go` | `receiverScaleStages`, `buildGstVideoPipeline`, `buildSystemWaylandVideoPipeline`, `buildVAWaylandVideoPipeline`, `buildVAPostprocPlainRawWaylandVideoPipeline`, `lowLatencyVideoQueueStage`; geometry, memory ownership, 4:2:0 conversion, and raw-frame dropping. `selectGstEncoderWithProbe`, `selectGstHEVCEncoder`, `captureBitrateKbps`, `recommendedBitrateKbps`, `vbvBufferKbit`, `MeasureVideoCaptureLatency`; quality/latency choices. [D7, D8, D9, D12] |
| `internal/airplay/capture_frame.go` | `VideoAccessUnit`, `ReadVideoAccessUnit`, `parseTimestampedRTPPacket`; PTS-carrying boundary to replace with a native C/CGO access-unit callback if needed. Do not mistake this local RTP transport for the AirPlay video wire format. [D10] |
| `internal/airplay/discovery.go`, `compatibility.go`, `latency.go`, `boot_clock_*.go` | Extended feature parsing, audio-versus-video distinction, PTP selection, interface hints, and platform clock domains. [D4, D11, D13] |
| Tests | `video_codec_test.go`, `mirror_setup_test.go`, `client_capabilities_test.go`, `capture_test.go`, `mirror_test.go`, `mirror_clock_test.go`, `receiver_server_test.go`. Useful named checks include `TestAutomaticVideoSelectionUsesNominalH264`, `TestSelectGstHEVCEncoderUsesMainProfileInput`, `TestCodecFrameUsesEncodedRasterForAllRects`, and `TestLiveHEVCCaptureLeadIsCommittedBeforeMediaSetup`. These are not substitutes for a physical receiver/image-quality test. |

## Sources

### Apple and platform documentation

- [A1] Apple TV 4K third-generation technical specifications: https://support.apple.com/en-us/111839
- [A2] Apple TV guide, separate mirroring and video-streaming workflows, Photos 4K HDR, underscan: https://support.apple.com/guide/tv/stream-audio-and-video-to-apple-tv-4k-atvbf2be9ef7/tvos
- [A3] WWDC19, Reaching the Big Screen with AirPlay 2, video/HLS scope: https://developer.apple.com/videos/play/wwdc2019/501/
- [A4] HLS authoring specification, sections 1.10 and codec-string examples. HLS-specific, not a mirroring specification: https://developer.apple.com/streaming/hls-authoring-specification-for-apple-devices.html
- [A5] HDR metadata and Main10 requirements for Apple media formats. Not proof of AirPlay mirroring HDR support: https://developer.apple.com/av-foundation/High-Dynamic-Range-Metadata-for-Apple-Devices.pdf
- [M1] Microsoft, YUV subsampling and full/studio-range conversion: https://learn.microsoft.com/en-us/windows/win32/medfound/recommended-8-bit-yuv-formats-for-video-rendering
- [M2] Microsoft, Windows Graphics Capture, HDR capture, QPC `SystemRelativeTime`, and `ContentSize`: https://learn.microsoft.com/en-us/windows/apps/develop/media-authoring-processing/screen-capture
- [G1] GStreamer x264 encoder, profiles, tuning, quality modes, and `vbv-buf-capacity` units: https://gstreamer.freedesktop.org/documentation/x264/index.html

### Public implementations and hardware reports

- [U1] UxPlay README, `-h265`, `-s`, `-fps`, and timestamp synchronization. These describe UxPlay/Apple-sender behavior, not a licensed-TV guarantee: https://github.com/FDH2/UxPlay/blob/master/README.md
- [U2] UxPlay issue #231, historical wired HEVC investigation: https://github.com/FDH2/UxPlay/issues/231
- [U3] UxPlay receiver packet/configuration handling: https://github.com/FDH2/UxPlay/blob/master/lib/raop_rtp_mirror.c
- [U4] Unofficial feature table. Note unnamed bit 28 and named bit 42: https://openairplay.github.io/airplay-spec/features.html
- [U5] Unofficial legacy H.264 screen-packet description. Useful for `avcC` and header history, incomplete for current HEVC/PTP implementations: https://openairplay.github.io/airplay-spec/screen_mirroring/stream_packets.html
- [R1] AppleTV6,2, tvOS 26.6, nominal 720p / maximum 1080p, reported working maximum-canvas H.264: https://github.com/omarroth/doubletake/issues/66
- [R2] Physical AppleTV14,1 HEVC/Wayland-scaling report; no sufficient proof of displayed 4K detail: https://github.com/omarroth/doubletake/pull/42
- [R3] TCL 55R617 Roku TV, reported 1080p30 H.264 Ethernet session: https://github.com/omarroth/doubletake/issues/70
- [R4] Missing PTP timeline and accepted-setup/frozen-picture hardware results: https://github.com/omarroth/doubletake/pull/61
- [R5] Wayland freeze despite ongoing transmission, proposed clock-provider change. Claimed diagnosis is not established receiver-internal behavior: https://github.com/omarroth/doubletake/pull/63
- [R6] Older GStreamer large-keyframe timestamp fragmentation and reported approximately 59 fps H.264 mirroring to UxPlay: https://github.com/omarroth/doubletake/pull/68

### Pinned DoubleTake source

- [D1] Session setup and capability refresh: https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/mirror.go#L462-L1045
- [D2] Receiver/display structures and nominal/maximum interpretation: https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/client.go#L190-L265
- [D3] Codec/canvas selection: https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/video_codec.go
- [D4] Discovery and extended features: https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/discovery.go
- [D5] H.264 streaming, configuration serialization, wire headers, and capture-time mapping: https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/mirror.go#L1362-L2148 and https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/mirror.go#L2591-L2697
- [D6] HEVC SPS/sample-description serialization and streaming: https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/mirror_hevc.go
- [D7] Encoder formats/presets, including actual 8-bit HEVC Main path: https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/capture.go#L1951-L2133
- [D8] Scaling, raw queue, Wayland physical/logical-size handling: https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/capture.go#L878-L1155
- [D9] Automatic bitrate, GOP, and VBV helper: https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/capture.go#L2291-L2358
- [D10] Timestamped encoded access units: https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/capture_frame.go
- [D11] Latency values and Linux interface detection: https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/latency.go
- [D12] Capture calibration: https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/capture.go#L574-L703
- [D13] Platform clock helpers: https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/boot_clock_linux.go and https://github.com/omarroth/doubletake/blob/5e2f1cbe3e1070bcefec8e549c7baf884727bc00/internal/airplay/boot_clock_other.go
