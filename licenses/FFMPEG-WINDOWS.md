# Windows FFmpeg selection and provenance

Pinned private-test binary: Gyan FFmpeg `9.0.2-essentials_build`, Windows
x86-64, static GPLv3. FFmpeg's own download page lists Gyan as a Windows build
provider. The immutable release URL is:

<https://github.com/GyanD/codexffmpeg/releases/download/9.0.2/ffmpeg-9.0.2-essentials_build.zip>

ZIP SHA256:

```text
60f467265b1e312373dbcd92200c2618a74850f98d3d078e94296bb3fa2047ba
```

On 2026-10-20, the download on the disposable build VM matched
the publisher's HTTPS checksum at
<https://www.gyan.dev/ffmpeg/builds/packages/ffmpeg-9.0.2-essentials_build.zip.sha256>.
The moving latest-release checksum endpoint returned the same value that day.
This checks against the same publisher, not an independent signature,
independently reproduced binary, or supply-chain audit. Packaging checks the
hard-coded pin even when using its download cache; it never trusts a fresh
checksum to approve a changed binary.

Extracted `ffmpeg.exe` SHA256:

```text
3256173f3f8bffd7df12227c68adf68025edb1832273a9530688a7bb1ed8edec
```

The executable hash was calculated from that first verified ZIP download and
pinned. It is not a separately published publisher hash.

## Included components, not tested hardware

The ZIP's own README lists these compiled components:

- Native capture filters `ddagrab` for DXGI and `gfxcapture` for WGC.
- `hwdownload`, `format`, `scale`, `scale_d3d11`, `zscale`, `lutrgb`, `pad`, `setsar`.
- Software `libx264` and hardware `h264_nvenc`, `h264_qsv`, `h264_amf`, `h264_mf`.
- GDI `gdigrab`, which is not the preferred native capture path.
- `libzimg` for explicit SDR transfer, matrix and range conversion.
- Rawvideo input, H.264 output and AVI muxing for the native C capture/FFmpeg pipe.

The packaging script checks the required names against that publisher
manifest. That is manifest inspection, not an executed Windows `-filters`
test. Windows must still enumerate them and run capture/encode/decode tests.
No Windows GPU, driver, display, native filter or Apple TV playback was tested
by the package worker. The Windows engine uses native C/DXGI primary-monitor
capture with CPU readback and software x264 encoding, with a GDI compatibility
fallback. It does not invoke FFmpeg's own capture filters or hardware encoders
in the first beta. Included components are not claims about the selected path.

## Windows and CPU minimums

Gyan documents Windows 10 or newer for these builds. The 2026 release uses
UCRT. PE import inspection found only Windows/UCRT imports. Hardware APIs can
also load components at runtime. Inspection did not establish that every import
can load on an actual Windows installation. Aircast targets Windows 10/11
x86-64 with working OpenGL drivers for the GUI. Windows on ARM emulation,
Windows 7/8 and headless service sessions are not supported by this test plan.
WGC monitor interop requires Windows 10 1903 or newer; use a fully updated
Windows 10 or Windows 11 machine for the native path.

The publisher manifest says `ARCH x86 (generic)` and `runtime cpu detection
yes`. AVX, AVX2 and AVX-512 are enabled optimizations, not by themselves a
declared mandatory AVX baseline. The Aircast Go binaries use `GOAMD64=v1`.
Neither fact proves that every static third-party FFmpeg library is safe on a
non-AVX CPU. Non-AVX Windows execution remains untested. Do not label AVX as
required, or claim AVX-free compatibility, without that test.

## Native license audit

The binary's embedded configuration contains `--enable-gpl --enable-version3`
and `--enable-libx264`; it does not contain `--enable-nonfree`. The publisher
also explicitly labels the static binary GPLv3. Do not relabel the bundle
LGPL merely because the DoubleTake helper is LGPL.

The publisher README lists exact versions for many external dependencies and
the complete enabled component list. Preserve it, not a shortened substitute.
License families needing attention include:

| Components | Main upstream license family | Distribution implication |
| --- | --- | --- |
| FFmpeg in this configuration | GPLv3 or later | GPL text and complete corresponding source for this executable and its linked covered code. |
| x264, x265, Xvid, vid.stab, Rubber Band | GPL, version varies by project | These are not an LGPL-only codec set. Respect GPLv3-compatible distribution terms. |
| LAME, GnuTLS, libssh, FriBidi, OpenAL Soft, Game Music Emu | LGPL, version varies by project | Preserve notices and satisfy the LGPL terms for the actual statically linked versions. |
| AviSynth integration and other external/transitive libraries | See exact source and publisher manifest | Audit each actual version and build option; a top-level license label is not the complete dependency audit. |
| zimg, libass, aom, vpx, Opus, Vorbis/Theora, WebP, OpenJPEG, SDL, HarfBuzz and others | Mostly permissive, some patent/notice clauses | Preserve actual version-specific copyright, license and patent notices. |
| FreeType, Cairo, Fontconfig, ZeroMQ, SRT and their dependencies | Mixed permissive/LGPL/MPL or other terms | Check exact options, dual-license selection, transitive code and required notices. |

This is a risk inventory, not a complete legal clearance. The publisher's
binary ZIP includes GPLv3 and version information, but not every dependency's
full source and notice text. Complete those materials before public or
third-party redistribution. The user authorized a private local-machine test,
not publication. See `SOURCE-AND-RELINK.md` for the unresolved release gate.
