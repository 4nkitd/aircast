# Decoded image-quality tests

## Live capture cross-check

The final Linux loopback run also captured the full reference chart from a real
private Xvfb desktop. A test-only stdout tap recorded the production GStreamer
RTP output. The production parser extracted 67 complete H.264 access units, and
FFmpeg decoded all 67 pictures without reported errors. Metadata was 1920x1080,
yuv420p, BT.709 limited range. The decoded first picture was visually inspected.
This checks actual Linux encoder output before encryption. It does not verify
Windows capture, a receiver's decoded picture or physical TV quality.

## Independent fixture measurements

`scripts/quality` is an independent, standard-library Go fixture generator and
FFmpeg measurement job. It does not import the capture, encoder, UI, or AirPlay
engine. Its nested `go.mod` adds no application dependency.

A successful run means the synthetic files encoded and decoded correctly, with
valid geometry, color signaling, frame IDs, timestamps, and complete metrics.
It does **not** mean Windows/Linux screen capture, AirPlay delivery, a hardware
decoder, or the picture on a physical TV has passed.

## Run on the approved VM

The approved machine is `aircast-engine.exe.xyz`; this worker uses
`/home/exedev/quality-worker`. Do not delete the shared VM. FFmpeg 6.1.1 with
libx264 and Go 1.27.2 were already installed, so this job needed no apt changes.

Upload only this directory from the Mac. Compilation and video work belong on
the VM, not the Mac:

```sh
tar -C scripts/quality -cf - . | ssh aircast-engine.exe.xyz 'mkdir -p ~/quality-worker && tar -C ~/quality-worker -xf -'
ssh aircast-engine.exe.xyz
cd ~/quality-worker
GOMAXPROCS=2 AIRCAST_QUALITY_INTEGRATION=1 go test -count=1 -v ./...
GOMAXPROCS=2 go build -o quality-measure .
GOMAXPROCS=2 ./quality-measure -out native-1080p
GOMAXPROCS=2 ./quality-measure -out scaled-720p -target-width 1280 -target-height 720
```

Alternatively, `sh run.sh -out /absolute/new/output` runs unit tests and the
measurement job with `GOMAXPROCS=2`. The integration environment variable enables
additional FFmpeg failure-injection tests. When enabled, missing FFmpeg is a
failure, not a skip. The normal measurement job always requires FFmpeg/ffprobe.

Output must be a new or empty directory. A rerun needs a different path; the job
will not overwrite an earlier baseline. FFmpeg codec threads are capped at two,
including each metric input decoder; filter threads are capped at one.

Default input is 1920×1080, 60 frames, 30 fps. Targets must be even, preserve the
source aspect ratio exactly, never upscale, and fit at most 1920×1080. A larger
RGB source up to 3840×2160 can be downscaled into that limit. An above-1080p H.264
target is rejected. This is not a universal receiver limit or a 4K mode.

This first job rejects mismatched aspect ratios rather than inventing a stretch
or crop. Production letterboxing, odd dimensions, portrait sources and monitor
changes need separate capture/geometry tests.

## Fixtures and the color contract

Every source frame is a lossless RGB PNG with its SHA-256 in `results.json`.
The embedded 5×7 bitmap font needs no host fonts or font-renderer dependency.
Fixtures contain:

- One-pixel black text and red, blue and green text on white.
- One-pixel vertical/horizontal lines, checkerboards and RGB stripes.
- Grey/R/G/B ramps, near-black and near-white steps, and flat color patches.
- Concentric circles, crosshairs and a one-pixel outer border.
- A moving red box, a printed frame number and a 16-bit black/white frame ID.

The RGB codes deliberately represent full-range, nonlinear BT.709 R'/G'/B'
with BT.709 primaries. This is a defined test input, not an assumption that an OS
screenshot already has that transfer curve. sRGB-to-BT.709 transfer conversion,
ICC handling, HDR tone mapping, real UI fonts, font smoothing and ClearType are
not covered. The wider corpus proposed in
[`research/encoding-verification.md`](research/encoding-verification.md) remains
necessary before capture-quality sign-off.

Resize RGB once with pinned Lanczos filtering, then convert to limited-range
BT.709 4:2:0 with explicit left chroma siting. Never resize an already-subsampled
desktop first. The job verifies pre-encode flat-patch interiors using an
independent Go equation, not FFmpeg's conversion function:

```text
Y' = 0.2126 R' + 0.7152 G' + 0.0722 B'
Y8  = round(16 + 219 Y')
Cb8 = round(128 + 224 (B' - Y') / 1.8556)
Cr8 = round(128 + 224 (R' - Y') / 1.5748)
```

The oracle permits one 8-bit rounding code. That is a conversion accuracy check,
not a perceptual quality threshold. Both conversion pixels and matching VUI
metadata matter; setting `-colorspace bt709` alone does not convert RGB.

Each decoded frame also checks the eight flat patch interiors against their
expected nearest RGB cube corner. The midpoint between 0 and 255 classifies each
channel, so a red/blue swap or gross color corruption fails. This semantic color
identity guard is not a color-fidelity tolerance. Exact conversion accuracy comes
from the independent pre-encode oracle; small decoded color errors remain in the
reported metrics and human review.

## Exact candidate profiles

These are trials, not extra user-facing modes. At 1080p30:

| Candidate | x264 preset | CRF | Maximum rate | VBV size | VBV capacity |
| --- | --- | --- | --- | --- | --- |
| `quality-12m` | `faster` | 18 | 12 Mbit/s | 1,800 kbit | 150 ms |
| `quality-16m` | `faster` | 18 | 16 Mbit/s | 2,400 kbit | 150 ms |
| `balanced-8m` | `veryfast` | 20 | 8 Mbit/s | 800 kbit | 100 ms |

All use software H.264 High level 4.0, 8-bit `yuv420p`, square pixels,
`tune=zerolatency`, zero B-frames and lookahead, normal deblocking/CABAC, and a
two-second maximum GOP. Scene cuts may create earlier IDRs. No PSNR/SSIM-specific
tuning is used. Rate caps are held fixed in the 720p scaling experiment to avoid
confounding a changed resolution with a changed rate policy. This is not the
proposed shipping 720p rate table.

The exact encoder block for the 12 Mbit/s candidate is:

```sh
-c:v libx264 -preset faster -tune zerolatency -crf 18 \
-maxrate 12M -bufsize 1800k -profile:v high -level:v 4.0 \
-pix_fmt yuv420p -bf 0 -g 60 -threads 2 \
-x264-params min-keyint=1:scenecut=40:open-gop=0:repeat-headers=1:aud=1:rc-lookahead=0:sync-lookahead=0 \
-chroma_sample_location left -color_range tv -colorspace bt709 \
-color_primaries bt709 -color_trc bt709
```

These arguments consume the **already converted** 4:2:0 reference. The full
conversion/encode/decode commands and their output are retained in the report.
The job also checks x264's effective CRF, maximum kbit/s, VBV kbit, GOP,
B-frames and codec thread count from the actual initialization output.

FFmpeg `bufsize` uses bits. GStreamer `vbv-buf-capacity` uses milliseconds:
`buffer_kbit = maxrate_kbit_per_second * capacity_ms / 1000`. Do not put `1800`
into a milliseconds property and call it the same 150 ms buffer.

MP4 keeps packet PTS/DTS for the fixture experiment. The job saves packet dumps,
requires a starting keyframe, monotonic PTS equal to DTS, and one packet per
verified fixture frame. Raw Annex-B does not preserve capture PTS. These fixture
timestamps are synthetic and do not prove live capture timing or A/V sync.
Hardware NVENC CQ/ICQ and VBV mappings are outside this job. In particular, do
not copy these x264 CRF flags into NVENC and assume its CQ mode retains VBV.

## What each comparison measures

| Row | Reference versus candidate | Interpretation |
| --- | --- | --- |
| `scale` | Source RGB versus target RGB enlarged back to source size | Spatial detail lost by downscaling, before chroma or encoding. Native 1:1 must be exactly lossless. Enlargement is for measurement only, not an encoded-resolution claim. |
| `chroma-range` | Target RGB versus a lossless FFV1 4:2:0 round trip | BT.709 limited-range quantization and chroma resampling. No H.264 artifacts. This is the current conversion control, not a universal optimal filter. |
| `*-codec` | Lossless 4:2:0 versus decoded H.264 | Additional codec loss after identical conversion. |
| `*-total` | Target RGB versus decoded H.264 | Chroma, range and codec loss together. The separate scale loss is not included. |

Go reports mean absolute error, root-mean-square error and maximum absolute error
in 8-bit RGB codes, both over all channels and over weighted BT.709 nonlinear
luma. It records per-frame errors and fixed black-text, colored-text, line,
gradient, aspect, color-range and motion ROIs.

FFmpeg reports per-frame PSNR/SSIM for the full frame and both text ROIs.
Codec rows retain native limited-range Y/U/V with per-plane results. Other rows
use planar full-range RGB. Their input is the **same decoded RGB bytes** measured
by Go, and FFmpeg PSNR must match `20 log10(255 / RGB_RMSE)` within printed numeric
precision. Do not compare an RGB score with a Y-only score or add stage PSNRs.

Every decoded frame ID and frame count is checked before scoring. Only then does
the metric filter put frame index `N` on the common synthetic `N/fps` timeline.
This prevents rounded Matroska timestamps and finer MP4 timestamps from making
FFmpeg compare neighboring moving frames. It does not repair or hide a missing,
duplicated, reordered or frozen frame; those fail before metrics. Original
encoded packet PTS/DTS remain in the artifacts. Full-frame metrics include the
border and ID marker; the fixed text ROIs exclude them.

One-pixel saturated color cannot survive ordinary 4:2:0 unchanged. Keep the
lossless 4:2:0 comparison beside every H.264 colored-text crop. More bitrate cannot
restore chroma samples discarded before encoding. Conversely, a bad black/white
text result must not be dismissed as chroma loss.

## Artifacts and review

Open `index.html` locally or serve the directory on localhost. The artifact
directory contains:

- `results.json`, exact profiles, fixture hashes, all command arguments, stream
  metadata, packet statistics, per-frame RGB/luma errors and per-ROI errors.
- Every generated source PNG, source/target lossless RGB video, lossless 4:2:0
  control, and all candidate MP4 files.
- Native reference/decoded PNGs for the first, middle and last frames. Fixed text,
  line and circle crops have absolute RGB difference PNGs amplified four times.
- Per-frame FFmpeg metric logs and complete command output, including failures.

Full-frame HTML previews are resized and explicitly labeled. Review crop PNGs at
1:1 mapping. Their difference amplification is for finding damage, not the
actual displayed appearance. Temporary raw RGB files are removed after metrics;
lossless source files and review PNGs remain.

Check color fringes against the 4:2:0 control, missing monochrome strokes,
gradient banding, clipped black/white steps, Lanczos halos, and round circles.
Review the first IDR separately. A readable synthetic bitmap font does not prove
that native OS text or subpixel-antialiased glyphs will be readable on a TV.

## Failure and regression policy

The process returns nonzero for a missing executable, nonzero command, decode
error/concealment treated as an error by FFmpeg, missing/truncated/extra frames,
black frames, wrong decoded ID or color identity, changed dimensions/aspect/cadence,
invalid color metadata/profile, failed color oracle, missing/invalid metrics, inconsistent RGB
PSNR, or incomplete per-frame metric logs. Commands use `-xerror` and
`-err_detect explode`. A failed command produces `status: failed`, an error and
the command log in JSON/HTML. Invalid configuration or an occupied output path
fails before creating a report rather than overwriting someone else's files.

There is no fabricated "TV quality passed" threshold. The numerical gates in
the research document are proposals, not accepted pass criteria for this job.

For regression review:

1. Freeze the fixture hashes, dimensions, frame count, color assumptions,
   FFmpeg/x264 version and profile settings with the retained source snapshot.
2. Compare like domains and the same fixed ROIs. Inspect per-frame logs and the
   first IDR, not just means. A repeatable increase in error or reduction in
   PSNR/SSIM is a regression requiring review, not an automatic baseline update.
3. Changes to the corpus, converter, encoder version or profile need a separately
   named baseline. Repeat the unchanged configuration to establish reproducibility
   before treating small differences as code regressions.
4. Keep a human visual veto. Do not lower a score floor to bless newly missing
   strokes, chroma beyond the control, halos or geometry errors. The maintainer must accept
   the baseline and any deliberate quality/performance tradeoff.
5. Close real capture, recovered receiver-stream decode and physical TV checks
   separately, by receiver model/firmware. This test cannot close those gates.

The small moving-box fixture is not sustained natural motion, dense scrolling,
noise, a network test, a CPU-admission benchmark or a long soak. Maximum rates are
encoder caps, not observed throughput or a demonstrated network requirement.

## Measured baseline, 2026-10-20

The retained `v3` runs completed all 17 top-level tests, `go vet`, and both 60-frame
measurements on the approved VM with `GOMAXPROCS=2`, FFmpeg 6.1.1-3ubuntu5,
libx264 core 164 and Go 1.27.2. All eight independent pre-encode color patches
matched the exact rounded oracle values at both target sizes. All candidates
decoded 60 ordered frame IDs with matching dimensions, square pixels, BT.709
limited-range metadata and monotonic packet PTS/DTS. Invalid-command, actual
black-frame, decoded red/blue swap and changed-SAR failure tests also passed.
Decoded color identities were checked on every compared frame, and the native
1:1 RGB branch was required to remain exactly lossless.

The native and scaled experiments use the same source PNGs. The first source
frame SHA-256 is
`8b982781643700eaf143f48fefdd0d66fcb0cd2d1da6180e03d00cc4f69dbf07`.
All 60 hashes, effective encoder logs and exact per-frame data are retained.

### Native 1920×1080

The following scores compare H.264 with the matched lossless 4:2:0 control.
PSNR/SSIM columns below are **Y only**, not the plane-weighted summary:

| Candidate | Mean video payload, Mbit/s | Y-PSNR, dB | Y-SSIM | Black-text Y-PSNR | Colored-text Y-PSNR |
| --- | --- | --- | --- | --- | --- |
| `quality-12m` | 0.509736 | 52.461744 | 0.999901 | 47.502477 | 48.268918 |
| `quality-16m` | 0.502684 | 53.112462 | 0.999900 | 47.600683 | 48.539115 |
| `balanced-8m` | 0.553948 | 49.128661 | 0.999734 | 43.880945 | 44.286599 |

For the whole frame, additional decoded-RGB codec MAE was 0.165725, 0.184675
and 0.277797 code values respectively. A higher native-Y PSNR does not guarantee
a lower decoded-RGB MAE or a better-looking glyph. Keep those domains separate.

The separate lossless 4:2:0 conversion already incurred RGB MAE 2.142699,
RGB RMSE 15.880156, RGB PSNR 24.113709 dB and RGB SSIM 0.973169. Black-text
conversion was exactly lossless in this black/white fixture. The deliberately
one-pixel saturated text/stripes explain why full-RGB scores can look poor while
codec-only scores are high. This is not a decoder failure or permission to ignore
colored-text review.

### 1920×1080 downscaled to 1280×720

Before chroma or H.264, resizing and enlarging the RGB reference back for
comparison produced RGB MAE 11.772523, RMSE 36.425606, PSNR 16.902668 dB and
SSIM 0.891710. Black-text scale-only RGB SSIM was 0.900075. This chart intentionally
places detail above the smaller canvas's sampling limit. Encoding the smaller
canvas at a higher bitrate cannot recover it.

| Candidate | Mean video payload, Mbit/s | Codec-only Y-PSNR, dB | Codec-only Y-SSIM |
| --- | --- | --- | --- |
| `quality-12m` | 0.274608 | 53.462845 | 0.999530 |
| `quality-16m` | 0.274608 | 53.462845 | 0.999530 |
| `balanced-8m` | 0.249848 | 51.712043 | 0.999376 |

Both quality caps produced the same decoded pixels in this 720p trial. This is
evidence about the retained chart, not proof that a 16 Mbit/s cap is useless for
busy desktop content. The full-frame low average rates show that this two-second
fixture does not exercise sustained 12/16 Mbit/s operation. Different presets can
also spend more bytes while producing lower scores; the balanced preset did so
in the native trial. Repeat capped, multithreaded encodes when calibrating small
regression differences rather than assuming bitstream identity. For example,
unchanged balanced encoder settings measured native codec Y-PSNR 49.424574 dB in
`v2` and 49.128661 dB in `v3`; the quality candidates were unchanged. Do not turn
this short, multithreaded VBV startup variation into a fabricated regression
tolerance or an unsupported claim of a code-quality change.

### Recommendation and retained files

Start receiver-approved 1080p30 SDR at quality CRF 18/`faster`, maximum 12 Mbit/s,
1,800 kbit VBV, and the exact common settings above. Keep CRF 20/`veryfast`,
8 Mbit/s maximum and 800 kbit VBV as the balanced candidate, without lowering the
canvas first. Test the 16 Mbit/s/2,400 kbit variant when receiver/network headroom
and denser motion/cut content justify it. These measurements do not establish a
universal 12-versus-16 Mbit/s winner, real-time CPU admission, receiver acceptance
or physical-TV quality. No source or test result justifies a 4K claim.

The corrected archive is
`aircast-engine.exe.xyz:/home/exedev/quality-worker/quality-artifacts-v3.tar.gz`.
Its folders are `native-1080p-v3` and `scaled-720p-v3`, each with `index.html` and
`results.json`. It also contains the harness source snapshot, profiles, lossless
source/control video, PNGs, command receipts, packet dumps and `tests-v3.log`.
The retrieved local copy is under
`/private/var/folders/tz/ssfkzx5x60j8mlm9125n74xm0000gn/T/opencode/aircast-quality.V9juGY/`.
Do not use the earlier unsuffixed reports as a baseline: their FFmpeg full-frame
motion metrics used timestamp alignment before the verified-index fix. They were
retained rather than silently overwritten.

The HTML report was loaded in an isolated browser session and all 336 linked PNG
URLs returned successfully. Representative decoded and control crops were also
inspected directly. This verifies report navigation and retained image content,
not physical rendering on a receiver. The scoped `/review` workflow was performed
in this worker without nested agents; formatting and shell syntax checks passed.
