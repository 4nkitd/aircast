# Aircast engine

Build this command from the pinned DoubleTake module. It takes no arguments.
The parent writes `start`, `credential`, and `stop` JSON objects to stdin and
reads one JSON event per stdout line. EOF and termination signals cancel the
session. A process serves one session and exits after stop or failure.

The wire contract is `../../../../docs/ENGINE-PROTOCOL.md` from this directory.
Credential values enter only through stdin. Upstream logging is disabled.
Public errors are fixed messages, not receiver response bodies, realms, child
stderr, or recovered panic values.

Pairing identities and Wayland restore tokens use the OS keyring under service
`aircast`, with a hashed receiver ID as the account. Fixed playback passwords
and onscreen PINs are not persisted. If keyring probing, reading, or writing
fails, a visible event announces session-only storage. There is no file-store
fallback. Tests inject an in-memory backend; they do not establish that a real
desktop keyring is working.

The engine requires signed HAP M6/V2 receiver proofs and pins the authenticated
receiver identity with the saved controller keys. A missing or changed saved
identity fails closed instead of silently casting to a newly advertised key.
Remove that receiver's OS-keyring entry before deliberately pairing it again.
Open/transient pairing still has no user-entered secret; use receiver PIN or
password protection on networks where other devices are not trusted.

Default capture is quality-first H.264, 30 fps, cursor enabled, video only.
`balanced`, a lower requested fps, cursor suppression, and optional audio are
accepted in `start`. Capture follows the receiver's post-SETUP nominal canvas,
aspect-fitted under 1920x1080 and even dimensions. It never selects HEVC, HDR,
or an advertised 4K maximum automatically. Requests above receiver `maxFPS`
are rejected. Missing `maxFPS` permits at most 30 fps. Capture/encoding,
receiver presentation, and audio availability still depend on the platform.

`streaming` starts after a complete video frame write succeeds. `frames` counts
those complete access units; `bytes` counts their encoded VCL payload without
transport headers, configuration packets, or AEAD tags. Neither counter proves
that a physical TV decoded or displayed a picture.

## Hardware-free tests

Run builds/tests on an approved Linux VM, not the Mac:

```sh
GOMAXPROCS=2 go test -race -p 2 ./cmd/aircast-engine ./internal/airplay
GOMAXPROCS=2 go test -run='^$' -fuzz='^FuzzReceiverPlistBoundary$' -fuzztime=15s -parallel=2 ./internal/airplay
```

The engine tests use real GStreamer-encoded synthetic video and upstream
`ReceiverServer`. They cover modern HAP, raw legacy pairing, FairPlay encrypted
video, PIN retry/failure, stored identity verification, password/Digest,
subprocess stdin/stdout, EOF, stop, and malformed receiver plists.

An opt-in production-executable test runs normal X11 capture, not synthetic
capture, under a real display or Xvfb:

```sh
GOMAXPROCS=2 go build -p 2 -o ./aircast-engine-linux ./cmd/aircast-engine
AIRCAST_ENGINE_BINARY="$PWD/aircast-engine-linux" xvfb-run -a -s '-screen 0 1920x1080x24' env GOMAXPROCS=2 go test -run '^TestProductionProcessX11PINStreamAndEOF$' -v ./cmd/aircast-engine
```

These are local sender/fixture tests, not Windows capture or hardware receiver
image-quality certification. Keep the upstream LGPL license and required source
material with distribution.
