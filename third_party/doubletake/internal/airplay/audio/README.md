# Windows system-output audio

`StartAudioCapture` uses shared-mode WASAPI loopback on the default `eRender` / `eConsole` output endpoint. It never opens `eCapture`, a microphone, Stereo Mix, or a virtual audio device. Linux retains the existing GStreamer capture path.

Loopback is **off by default** while hardware verification is pending. Audio must be selected by the caller, and the Windows process must also have `AIRCAST_EXPERIMENTAL_WASAPI_LOOPBACK=1`. Explicit test tone bypasses this gate and needs no device, GStreamer, or CGO.

## Build and runtime

- Go 1.25+, `CGO_ENABLED=1`, and a Windows C compiler. The tested cross compiler is `x86_64-w64-mingw32-gcc` on the Linux build VM.
- Only Windows system COM/audio libraries are used. No FFmpeg, resampler DLL, or audio driver installation is required. ALAC is built in. AAC-ELD still requires the existing optional FDK-AAC build.
- A single locked OS thread initializes an STA, creates, reads, stops, and releases every WASAPI/COM resource. Polling avoids the pre-Windows-10 event-loopback restriction.
- Source mix formats are 8..192 kHz, 1..8 channels, float32/float64 or unsigned PCM8 / signed PCM16/24/32, including left-aligned valid bits in extensible PCM. Ambiguous multichannel masks and unsupported formats fail explicitly.
- Conversion produces interleaved 44.1 kHz stereo S16LE. A bounded, interpolated Blackman-windowed sinc filter handles sample-rate conversion. Standard surround channels are normalized into stereo; LFE is excluded.
- PCM frames retain device-frame positions and packet QPC timestamps mapped to Go's monotonic clock. Invalid timestamps fall back to the last device mapping and counted real samples. The first invalid timestamp can use a read-time estimate. An un-timestamped discontinuity fails instead of concealing a gap.
- The native requested buffer is 100 ms and rejected if it exceeds 250 ms or 4 MiB. The Go queue retains at most 250 ms and drops oldest complete frames under backpressure without inventing replacement samples. The filter's maximum coefficient table is about 1.6 MiB.

## Failure behavior and remaining verification

No default endpoint, invalidated devices, stopped Windows audio service, unsupported format, and native API failures return errors. WASAPI `SILENT` packets become silence because the device supplied those frames. No packets are not converted into silence; five seconds without packets ends capture with an explicit idle/unavailable error. This means starting capture with no Windows playback can time out. Start playback before testing.

The selected default endpoint is fixed for the capture lifetime. Default-device changes require stopping and restarting audio. Protected/DRM and exclusive-mode playback may not be available to shared-mode loopback. COM activation calls cannot themselves be interrupted; cancellation returns promptly to a starting caller, and the worker cleans up when the in-flight Windows call returns.

Hardware-free tests cover conversion, resampling passband/alias rejection, chunk boundaries, device-clock mapping, real silent packets, discontinuities, bounded backpressure, cancellation, and test tone. Windows C/CGO cross-compilation does not verify Windows runtime capture or AirPlay receiver playback. Before enabling by default, test default-output playback, initial idle state, pause/resume, silent packets, endpoint removal, stop/restart, 44.1/48/96/192 kHz mixes, mono/stereo/surround, and receiver A/V sync on Windows hardware.
