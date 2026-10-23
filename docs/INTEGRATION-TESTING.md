# Sender integration acceptance

`scripts/integration/run.sh` tests a real `aircast-engine` process and the public
root `casting.Controller` API against `doubletake-test-receiver`. All receivers
are synthetic fixtures on `127.0.0.1`. They are not TVs, even when the profile
is called `roku` or `appletv3`.

The runner displays a caller-supplied quality chart PNG on a new Linux Xvfb
desktop. The production capture path must capture that desktop. It does not
replace capture with `videotestsrc`, use the personal desktop, or discover
network receivers. Audio is explicitly off.

## Safety and prerequisites

Use the approved disposable Linux VM `aircast-engine.exe.xyz`. Keep this work
under `~/integration-worker`. Do not build the media engine on the Mac, start a
second package installation while another worker holds apt locks, or tear down
the shared VM.

The VM needs these existing tools:

- Go matching `go.mod`, `uv`, `unshare`, and `ip`.
- `dbus-daemon`, `Xvfb`, `xauth`, `xdpyinfo`, and `xwininfo`.
- `ffplay` and `ffmpeg` with X11 input support.
- The engine's native media runtime and plugins. See `docs/BUILDING.md`.

`run.sh` needs permission to create network, PID, and mount namespaces. This
usually means running as root inside the disposable VM. It fails rather than
falling back to the VM's external network. The network namespace has only
loopback; the runner verifies that before starting capture or receiver children.
The extra isolation matters because the sender's timing sockets can bind
wildcard addresses. Those sockets remain inside this loopback-only namespace.

Xvfb disables TCP and uses a fresh Xauthority cookie. D-Bus uses a private UNIX
socket. HOME and XDG directories are temporary and private. No session keyring,
personal display, or host audio address is inherited. Receiver names contain
`SYNTHETIC LOOPBACK`. Nothing advertises mDNS services.

The runner only signals its own child process groups. Each operation has a
deadline, and the suite has a 900-second deadline by default. It attempts
SIGTERM, reaps children, and uses SIGKILL only after a bounded shutdown timeout.
A required SIGKILL fails acceptance. The private PID namespace is a final guard
against orphaned descendants. No `pkill`, existing-session shutdown, or VM
teardown is part of these scripts.

## Prepare binaries after core integration

The integration worker does not build active workers' code. Once the engine and
controller have been integrated, the parent can run the following on the VM
from a complete source checkout. Replace `/path/to/aircast` with that checkout.

```sh
mkdir -p "$HOME/integration-worker/bin"
export GOMAXPROCS=2
cd /path/to/aircast
go build -p 2 -o "$HOME/integration-worker/bin/controller-probe" ./scripts/integration/controller-probe
cd third_party/doubletake
go build -p 2 -o "$HOME/integration-worker/bin/aircast-engine" ./cmd/aircast-engine
go build -p 2 -o "$HOME/integration-worker/bin/doubletake-test-receiver" ./cmd/doubletake-test-receiver
```

If the engine worker already produced those engine/receiver binaries, copy
them instead of rebuilding. The probe and real engine must be regular files
beside each other. The engine must be named `aircast-engine`. The controller
deliberately does not use PATH overrides or an engine symlink.

The probe imports `aircast/internal/casting` and `aircast/internal/receiver`.
It calls `New`, `Available`, `Start`, `SubmitCredential`, `Stop`, `Snapshot`,
and `Close`. Its extra `cancel` command cancels the context passed to `Start`.
It does not replace the supervisor or fake engine events.

## Run the acceptance suite

Use a chart made by the quality worker, such as
`/path/to/quality-output/fixtures/frame-0000.png`. The integration suite only
reads that file. It does not modify `scripts/quality` or run its encode study.

From the VM checkout:

```sh
sh scripts/integration/run.sh \
  --engine "$HOME/integration-worker/bin/aircast-engine" \
  --receiver "$HOME/integration-worker/bin/doubletake-test-receiver" \
  --controller-probe "$HOME/integration-worker/bin/controller-probe" \
  --desktop-image /path/to/quality-output/fixtures/frame-0000.png \
  --output "$HOME/integration-worker/acceptance-quality-$(date +%Y%m%d-%H%M%S)"
```

The output path must not exist. The default receiver profiles are `modern`,
`roku`, and `appletv3`. Each runs unauthenticated stop, PIN-authenticated stdin
EOF, and combined-authentication stop. The dummy fixture code is `1234`, passed
to the receiver through `DOUBLETAKE_RECEIVER_CODE` and to the sender through
stdin. It never appears in command arguments. Logs redact both the dummy code
and the wrong-PIN test value. Public event messages containing either fail.

To repeat with the lower-bandwidth policy or a wider receiver profile matrix,
use a new output directory and append either of these options:

```sh
--quality balanced
--profiles modern roku lg appletv3 uxplay airserver
```

Other cases cover:

- Wrong PIN exhaustion, zero transmitted media, and a failed event.
- Stop and stdin EOF while waiting for a PIN.
- Malformed/oversized engine input and a refused loopback TCP connection.
- Controller PIN submission, rejected invalid/unsolicited credentials,
  concurrent-start rejection, async Stop, idle Snapshot, and same-controller
  reconnect with fresh per-session receiver deltas.
- Wrong-PIN failure followed by a correct-PIN reconnect on the same controller.
- Loss of an owned synthetic receiver followed by a new receiver connection.
- Context cancellation during streaming, Close during pairing and streaming,
  and rejection of Available/Start after Close.

Successful streaming requires a `streaming` state, negotiated video metadata,
positive transmitted frames and payload bytes, and a second larger transmitted
frame count. Receiver video packet and byte counts must also increase beyond
the baseline taken before that session. Prior-session totals cannot pass a
reconnect test. `Stop` must return in less than 250 ms; the process can finish
asynchronously within the separate eight-second shutdown deadline.

Expected engine failures must emit `failed` and exit normally with status `1`.
This applies to wrong PIN exhaustion, malformed/oversized input, and refused
connections. Successful stop/EOF and the controller probe require status `0`.
Signals, status `2` from an unhandled Go panic, and other unexpected exit codes
fail acceptance even if the process first emitted a `failed` event. Receiver
fixtures must exit normally with status `0` after their owned SIGTERM shutdown.

The first qualifying receiver observation is only `receiver_progress` in the
report. After the sender stops, exits, cancels, or closes, the runner checks a
fresh receiver snapshot against that session's baseline and records
`receiver_post_sender`. Each reconnect session needs its own shutdown checkpoint
before the next transport check. Terminal controller snapshots also retain the
last transmitted counters.

A requested controller restart starts a new counter epoch at its fresh
`connecting` event. Queued terminal snapshots from the previous session do not
seed the new counters. They still pass schema, credential and integer checks.

Before a case passes, the runner stops its receivers, consumes stdout through
EOF, requires the receiver's `final stats:` line, and validates those final
counters too. The last session's `receiver_after` includes this final snapshot;
earlier reconnect sessions keep their own post-sender boundary. A later crypto
or malformed counter fails the whole case, including reconnect series. Error
peaks remain failures even if later output omits or resets those fields.
`sender_shutdown_checked` and `receiver_shutdown_checked` show the completed
checks. Missing final output or a missing session checkpoint also fails.

## Decryption is not picture decode

The vendored receiver CLI prints `video=packets/bytesB`, `video_decrypted`,
`video_crypto_errors` and `video_malformed`. The parser also accepts older CLIs
that omit optional counters. It accepts
`video_decrypted=N`, `video_crypto_errors=N`, and `video_malformed=N`, plus the
short `decrypted`, `crypto_errors`, and `malformed` forms. It checks zero errors
whenever those fields are exposed.

For `appletv3` and `uxplay`, exposed legacy AES-CTR decryption counters must
increase. Modern encrypted H.264 also reports authenticated decryption and AVCC
validation. Plaintext paths do not count as decrypted, and HEVC framing remains
unvalidated and does not increment that counter. Each session's report states
the applicable limitation. To make a
missing legacy counter a hard failure, append:

```sh
--require-legacy-decryption
```

This option requires `appletv3` or `uxplay` in the selected profiles. Do not
silently call the default byte-only result encrypted-video decode acceptance.
Legacy decryption validates AVCC/NAL structure. It still does not decode a
picture, check text sharpness, measure color fidelity, or prove TV output.

If a caller has a clip copied from the actual outgoing encoded stream, with
decoder configuration and appropriate Annex B/container framing, add:

```sh
--outgoing-clip /path/to/outgoing-stream.h264
```

FFmpeg then decodes up to 300 pictures with error detection enabled and writes
`outgoing-clip.framemd5`. At least one picture must decode. The report hashes the
fixture and labels this check separately. The runner does not intercept media,
manufacture an outgoing clip, or establish that a caller-supplied file came from
this session. A separately encoded quality-study MP4 is only a codec fixture,
not evidence that the production sender's pictures decoded.

## Artifacts and interpretation

`report.json` records case results, per-session sender/receiver counters,
negotiated video metadata, child exits, and decryption/decode gaps. Redacted
stdout/stderr logs are bounded to 2 MiB per child stream. `source-desktop.png`
is a screenshot of the private chart desktop, not receiver output. Review it
before treating a run as useful capture evidence.

A passing run is Linux synthetic receiver transport acceptance. Native GUI
screenshots, real hardware image/audio acceptance, and Windows runtime testing
remain separate gates. This suite makes no actual Windows runtime claim.

## Test the test runner without media builds

These tests use only Python's standard library and short-lived owned fixture
processes. They can run on the Mac without building Go/C, starting capture,
opening a receiver, or touching the personal desktop:

```sh
uv run --no-project scripts/integration/test_run.py
sh -n scripts/integration/run.sh
gofmt -d scripts/integration/controller-probe/main.go
```

They check stats parsing, per-session deltas, decryption gaps/strict failure,
counter monotonicity, credential redaction, protocol line bounds, process EOF,
timeouts, failed exits, and cleanup after a failed case. Linux-only crash
fixtures disable core dumps and verify that a `failed` event followed by
SIGSEGV/SIGABRT cannot pass, including case-level sibling cleanup. Receiver
fixtures emit good initial counters followed by late crypto/malformed errors
at either the post-sender checkpoint or final receiver shutdown. Reconnect
series and missing final/checkpoint output have regression coverage too.
These tests do not substitute for running `run.sh` against integrated binaries.

On 2026-10-20, all 57 runner self-tests and all 21 live cases passed on the
approved Linux VM. The live run used the modern, Roku and Apple TV 3 synthetic
profiles with `--require-legacy-decryption`, no forced child kills, and zero
reported receiver crypto/malformed errors. This is H.264 transport evidence,
not picture decode or physical-TV acceptance.

The final run also waited for the fullscreen chart to paint and required at
least 30 dB RGB PSNR against its original PNG. It measured 42.523 dB. That metric
checks the source desktop, not encoding quality. A separate test-only capture
stdout tap supplied 67 complete access units through the production RTP parser;
FFmpeg decoded all 67 pictures. Those were encoder-side bytes before AirPlay
encryption, not receiver screenshots. `TestExtractCapturedRTPAccessUnits` can
repeat the bounded extraction with `AIRCAST_CAPTURE_TAP` and a new
`AIRCAST_ANNEXB_OUTPUT` path. An unfinished encoder-stop tail is reported and
excluded; malformed complete packets still fail.

### Earlier review-fix evidence

On 2026-10-20, all 47 self-tests passed on `aircast-engine.exe.xyz` in 1.416
seconds, including both crash signals, case-level cleanup, late receiver errors,
and reconnect checkpoints. The copied `run.sh` also passed `sh -n` on that VM.
The exact self-test command was:

```sh
ssh aircast-engine.exe.xyz 'GOMAXPROCS=2 uv run --no-project /home/exedev/integration-worker/review-fixes.zOss8N/test_run.py'
```

That earlier worker check covered only the runner. The later 55-test and live
21-case parent runs above used the integrated binaries and chart fixture.
