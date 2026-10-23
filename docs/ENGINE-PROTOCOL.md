# Private sender process protocol

Aircast supervises a separate `aircast-engine` executable, built from the pinned
LGPL DoubleTake fork in `third_party/doubletake`. On Windows, use `.exe`.
The GUI and CLI never invoke a shell to start it. Bundle it beside the GUI.

Standard input and standard output carry one JSON object per line, at most
64 KiB per object. Standard output contains no logs or secrets. Disable upstream
debug logs. Credential values must never appear in output, arguments, or logs.
stdin EOF cancels the session and releases capture and media resources.

First input:

```json
{"command":"start","host":"192.168.1.40","port":7000,"quality":"quality","fps":30,"audio":false,"cursor":true}
```

`quality` selects a quality-first encoder policy, `balanced` a documented lower
bandwidth policy. The implementation must negotiate receiver capabilities and
preserve aspect ratio. Do not force HEVC or 4K on unsupported receivers.
Native system audio may be explicitly unavailable on a platform. Never silently
claim it is transmitted when only video is implemented.

Subsequent input:

```json
{"command":"credential","value":"1234"}
{"command":"stop"}
```

Output examples:

```json
{"event":"state","state":"connecting","message":"Connecting to AirPlay receiver"}
{"event":"credential","state":"pairing","kind":"pin","message":"Enter the PIN shown on the TV"}
{"event":"state","state":"starting","width":1920,"height":1080,"codec":"h264"}
{"event":"state","state":"streaming","width":1920,"height":1080,"codec":"h264"}
{"event":"stats","frames":100,"bytes":1234567}
{"event":"state","state":"idle","message":"Screen sharing stopped"}
{"event":"state","state":"failed","message":"Receiver refused pairing"}
```

Emit `streaming` only after media setup and actual frame transmission begin.
When native Windows capture selects its GDI compatibility path, emit a `state`
event with the exact fixed message `Using GDI compatibility capture instead of
DXGI` before `streaming`. The supervisor preserves this notice across later
state and stats events, without forwarding arbitrary upstream error text.
Stats are monotonic transmitted-frame/encoded-payload counters, not a claim of
physical TV decoding. No decoder feedback means hardware output stays unverified.
Use OS keyring storage for reusable pairing identities. Lack of a keyring must
not cause a silent plaintext secret-file fallback. Session-only pairing is an
acceptable explicit fallback, with a visible message.

The first delivery is AirPlay screen casting. Chromecast, extended desktops,
file playback, surround sound, and multi-receiver support remain later gates.
