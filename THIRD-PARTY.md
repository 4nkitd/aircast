# Third-party code

Direct dependencies in this milestone:

| Project | Version | License | Purpose |
| --- | --- | --- | --- |
| Fyne | 2.8.2 | BSD-3-Clause | Cross-platform panel, native windows, tray integration |
| HashiCorp mDNS | 1.0.7 | MIT | DNS-SD query and test fixture advertisement |
| howett.net/plist | 1.0.1 | BSD-2-Clause | AirPlay information property lists |
| godbus/dbus | 5.2.2 | BSD-2-Clause | Detect a Linux StatusNotifier tray host |

The complete module graph and integrity hashes are in `go.mod` and `go.sum`.
Distributions must retain dependency notices and their transitive notices.
Aircast's own code is GPL-3.0; the complete notice bundle ships in `licenses/`.

## AirPlay sender integration

DoubleTake: https://github.com/omarroth/doubletake

Evaluated revision: `5e2f1cbe3e1070bcefec8e549c7baf884727bc00`.
License: LGPL-3.0, with GPL-3.0 text included by upstream.

The pinned source is vendored as a separate Go module in `third_party/doubletake`,
including upstream `LICENSE` and `COPYING.GPL`. Aircast's casting work adds a
separately executable sender, Windows capture/audio ports, and quality fixes.
The original was Linux-specific; a Windows cross-build alone does not prove the
new platform paths work on a Windows desktop.

The application supervises the LGPL sender over private standard-input/output
JSON, not a network control service. Preserve attribution, supply the complete
modified sender source and build instructions with binary handoffs, and allow
replacement of that executable. Audit native codec/plugin licenses separately.
FFmpeg builds with libx264 are generally GPL builds; bundling them as a separate
executable does not remove FFmpeg's own source and notice obligations. No
`--enable-nonfree` build is acceptable for redistribution.

AirPlay, Apple TV, Chromecast, Google Cast, and AirParrot are their respective
owners' marks. Aircast is not affiliated with Apple, Google, or Squirrels.
