# Receiver-first casting panel

The primary job is finding the correct television without opening a dashboard.
Use one compact window, a scrolling receiver list, and a selected-device detail
area, then one screen-sharing control area. Left-align labels. Keep technical
addresses selectable, not decorative. Keep the current cast visible when another
receiver is selected. Selecting a receiver never starts or switches a cast.

## Tokens

- Porcelain `#F2F5F7`: window background.
- Paper `#FFFFFF`: receiver list and dialogs.
- Ink `#163246`: primary text and broadcast mark.
- Harbor `#245F88`: selection, focus, and primary actions.
- Slate `#617586`: secondary text.
- Sea `#237B67`: streaming only after the sender reports frame transmission.

Use Fyne's bundled Noto Sans. Title 28, section 18, body 14, helper 12.
Keep native keyboard focus and platform window controls. No gradients or motion.

```
 [broadcast mark]  Aircast                       About
                    Your screen, on the big screen.

 Receivers                         Refresh   Add
 Searching your local network...
 +---------------------------------------------+
 | TV  Living room                 AirPlay      |
 |     192.168.1.40:7000                        |
 | TV  Office                      Google Cast  |
 |     192.168.1.41:8009                        |
 +---------------------------------------------+

 Living room
 AirPlay / AppleTV14,1
 192.168.1.40:7000       [Check receiver] [Save receiver]
 Diagnostic result, with its precise limits.

 Screen sharing                           Not sharing
 [screen] Desktop                    [Best available v]
 [ ] System audio (beta)              [x] Show cursor
 Quality and platform limitations in plain language.
 Select an AirPlay receiver above.      [Start sharing]

 Closing keeps Aircast in the tray.             [Quit]
```

The window starts at 548 × 820. The receiver list takes the remaining vertical
space; selected-receiver details and casting controls keep their natural size.
Use one quiet control area rather than a dashboard of identical cards. A single
primary action starts screen sharing and becomes an explicit stop action.

## Casting behavior

- `Best available` is the initial quality policy, with 30 fps requested. The
  first compatibility profile is SDR H.264 at a receiver-approved canvas up to
  1080p30. Balanced uses a lower bandwidth budget without promising a different
  resolution. Neither label promises a bitrate or codec the receiver has not
  negotiated. HEVC, 4K, and HDR are not implied by the quality label.
- Windows offers `Primary screen`. Linux says `Desktop` because X11 capture may
  include the whole root desktop across monitors. Wayland uses the system screen
  picker. No mock window, region, file, extended-screen, or multi-receiver
  controls. Windows and Linux are the supported capture targets.
- System audio is optional, labeled beta, and off by default. The sender must
  reject unavailable audio rather than silently sending video only. The cursor
  checkbox starts checked. An unchecked cursor requests exclusion; some Windows
  drivers embed it in the captured pixels, so exclusion must not be claimed
  unless the capture backend can honor it.
- Google Cast and audio-only AirPlay receivers retain discovery, save, and
  diagnostic actions. Their start action stays disabled with a specific reason.
- A cast locks its source and quality until it stops. Selecting another receiver
  does not change the active session or its stop action. A separate Switch
  receiver action asks for confirmation, waits for the old sender to stop, then
  starts the confirmed receiver with the same settings. Cancel switch stays
  stopped. Changing selection while a switch is pending cancels it. There is no
  implicit restart, reconnect, or receiver switch.
- Connecting, pairing, capture preparation, streaming, stopping, and failure are
  distinct states. A successful diagnostic check never changes cast state.
- Negotiated dimensions and codec appear only when the sender reports them.
  Frame and payload counters are labeled as sent, not decoded by the television.
  Receiver hardware output still needs a real TV test.

## Pairing and lifecycle

PIN and password entry use a masked native dialog. Blank credentials cannot be
submitted. Closing or canceling the prompt stops the session; dismissing it after
a legitimate state transition does not. The entry is cleared immediately after
use. Credentials are not logged, saved in receiver configuration, put in command
arguments, or copied to the clipboard.

Every sender callback enters the Fyne event loop with `fyne.Do`. Each new cast
after a terminal state gets a new controller generation, so queued callbacks
from the previous session cannot revive it. Receiver identity and terminal-state
guards also reject stale events. A canceled diagnostic request cannot update
another receiver, including a programmatic selection after adding or saving one.
Selection resolves current inventory aliases before rendering and after saving.
The active cast keeps the identity originally passed to the controller, so an
address-to-hardware-ID promotion does not lose its Stop action or allow a second
cast.

Closing hides the window only when a usable tray host exists. Without one,
closing an active cast asks whether to stop and quit. The Quit action always
asks for an active session, including pairing or preparation. Shutdown cancels
discovery, closes the sender off the UI thread, and then exits.

`func (p *Panel) Close()` is idempotent, has no return value, and must be deferred
by the application entry
point after constructing the panel. That joins sender cleanup when an OS signal
or an application-level quit bypasses the panel's confirmation flow. The panel
does not replace the application's lifecycle callbacks.

## Verification limits

UI tests cover state transitions, stale session and probe callbacks, default
quality and audio settings, unsupported receivers, pairing cancellation, and
quit confirmation. A simulated streaming event tests presentation only. It does
not prove screen capture, transmission, or a receiver's decoded picture. VM GUI
screenshots and real AirPlay hardware tests remain separate checks.
