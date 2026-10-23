package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"aircast/internal/casting"
	"aircast/internal/diagnostics"
	"aircast/internal/receiver"
)

type startCall struct {
	ctx     context.Context
	device  receiver.Device
	options casting.Options
}

type fakeCastingController struct {
	starts       chan startCall
	stops        chan struct{}
	credentials  chan string
	mu           sync.Mutex
	closed       int
	closeErr     error
	closeStarted chan struct{}
	closeRelease <-chan struct{}
	snapshot     casting.Event
}

func newFakeCastingController() *fakeCastingController {
	return &fakeCastingController{
		starts: make(chan startCall, 4), stops: make(chan struct{}, 4), credentials: make(chan string, 4),
		snapshot: casting.Event{Type: "state", State: casting.Stopping},
	}
}

func (c *fakeCastingController) Start(ctx context.Context, device receiver.Device, options casting.Options) error {
	c.starts <- startCall{ctx: ctx, device: device, options: options}
	return nil
}

func (c *fakeCastingController) Stop() error {
	c.stops <- struct{}{}
	return nil
}

func (c *fakeCastingController) SubmitCredential(value string) error {
	c.credentials <- value
	return nil
}

func (c *fakeCastingController) Snapshot() casting.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshot
}

func (c *fakeCastingController) Close() error {
	c.mu.Lock()
	c.closed++
	err, started, release := c.closeErr, c.closeStarted, c.closeRelease
	c.mu.Unlock()
	if started != nil {
		started <- struct{}{}
	}
	if release != nil {
		<-release
	}
	return err
}

func (c *fakeCastingController) Available() error { return nil }

func castingTestPanel(t *testing.T) (*Panel, *fakeCastingController, receiver.Device) {
	t.Helper()
	p := newTestPanel(t)
	c := newFakeCastingController()
	p.cast.controller, p.cast.generation, p.cast.available, p.cast.platform = c, 1, true, "windows"
	d, _ := receiver.Manual("Living room", "192.168.1.40", receiver.AirPlay)
	p.inventory.Observe(d, time.Now())
	p.render()
	p.list.Select(0)
	return p, c, d
}

func awaitStart(t *testing.T, c *fakeCastingController) startCall {
	t.Helper()
	select {
	case call := <-c.starts:
		return call
	case <-time.After(2 * time.Second):
		t.Fatal("sender did not receive Start")
		return startCall{}
	}
}

func awaitStop(t *testing.T, c *fakeCastingController) {
	t.Helper()
	select {
	case <-c.stops:
	case <-time.After(2 * time.Second):
		t.Fatal("sender did not receive Stop")
	}
}

func TestQualityDefaultsAndExplicitStreamingState(t *testing.T) {
	p, c, d := castingTestPanel(t)
	if p.cast.quality.Selected != bestQualityLabel || p.castOptions() != defaultCastOptions() {
		t.Fatal("quality-first, audio-off, cursor-on defaults changed")
	}
	if p.cast.button.Disabled() || p.cast.state.Text != "Not sharing" {
		t.Fatal("a checked sender should offer Start without claiming a session")
	}
	p.completeProbe(p.probeID, d.ID, d.Endpoint(), diagnostics.Result{Summary: "AirPlay information received"}, nil)
	if p.cast.state.Text != "Not sharing" {
		t.Fatal("a probe result was mistaken for a cast")
	}
	p.startCasting()
	call := awaitStart(t, c)
	if call.device.ID != d.ID || call.options != defaultCastOptions() || call.ctx.Err() != nil {
		t.Fatal("Start did not receive the selected receiver and quality defaults")
	}
	if p.cast.state.Text != "Connecting" || !p.cast.quality.Disabled() || !p.cast.audio.Disabled() {
		t.Fatal("starting should lock settings without claiming streaming")
	}
	p.applyCastEvent(1, casting.Event{Type: "stats", Frames: 12, Bytes: 1024})
	if p.cast.state.Text != "Connecting" {
		t.Fatal("statistics alone promoted the cast to streaming")
	}
	p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Starting, Width: 1920, Height: 1080, Codec: "h264"})
	if p.cast.state.Text != "Preparing" || !strings.Contains(p.cast.stats.Text, "1920 × 1080 negotiated / H.264") {
		t.Fatal("negotiated format was not shown while preparing")
	}
	p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Streaming})
	if p.cast.state.Text != "Streaming" || p.cast.button.Text != "Stop sharing" || !strings.Contains(p.cast.stats.Text, "12 frames sent") {
		t.Fatal("the real sender event did not update streaming controls and counters")
	}
	p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Idle})
	if p.cast.state.Text != "Not sharing" || p.cast.button.Text != "Start sharing" || !strings.HasPrefix(p.cast.stats.Text, "Last session: ") {
		t.Fatal("the stopped session retained active controls")
	}
}

func TestBalancedAudioAndCursorOptionsAreExplicit(t *testing.T) {
	p, c, _ := castingTestPanel(t)
	p.cast.platform = "linux"
	p.cast.quality.SetSelected(balancedQualityLabel)
	p.cast.audio.SetChecked(true)
	p.cast.cursor.SetChecked(false)
	p.startCasting()
	options := awaitStart(t, c).options
	if options.Quality != casting.Balanced || !options.Audio || options.ShowCursor || options.FPS != 30 {
		t.Fatal("selected capture options were not passed to the sender")
	}
}

func TestUnsupportedReceiverStartIsDisabled(t *testing.T) {
	for _, kind := range []receiver.Kind{receiver.Cast, receiver.Audio} {
		t.Run(string(kind), func(t *testing.T) {
			p, c, _ := castingTestPanel(t)
			d, _ := receiver.Manual("Unsupported", "192.168.1.41", kind)
			p.inventory.Observe(d, time.Now())
			p.selectDevice(d.ID)
			p.render()
			if !p.cast.button.Disabled() || !strings.Contains(p.cast.message.Text, "not supported yet") {
				t.Fatal("unsupported receiver has an enabled or unexplained start action")
			}
			p.startCasting()
			select {
			case <-c.starts:
				t.Fatal("unsupported receiver reached the sender")
			default:
			}
		})
	}
}

func TestUnavailableSenderAndUnsupportedPlatformExplainDisabledStart(t *testing.T) {
	p, _, _ := castingTestPanel(t)
	p.cast.available = false
	p.cast.availabilityErr = errors.New("sender is not installed")
	p.renderCasting()
	if !p.cast.button.Disabled() || !strings.Contains(p.cast.message.Text, "sender is not installed") {
		t.Fatal("missing sender left an unexplained start action")
	}
	p.cast.availabilityErr = nil
	p.cast.available, p.cast.platform = true, "darwin"
	p.renderCasting()
	if !p.cast.button.Disabled() || !strings.Contains(p.cast.message.Text, "Windows and Linux only") {
		t.Fatal("unsupported capture platform left an unexplained start action")
	}
}

func TestSourceDescriptionDoesNotPromisePrimaryMonitorOnX11(t *testing.T) {
	p, _, _ := castingTestPanel(t)
	if p.cast.source.Text != "Primary screen" {
		t.Fatal("Windows primary-screen capture is not identified")
	}
	p.cast.platform = "linux"
	p.renderCasting()
	if p.cast.source.Text != "Desktop" || !strings.Contains(p.cast.helper.Text, "all monitors") || strings.Contains(p.cast.helper.Text, "primary screen") {
		t.Fatal("Linux capture incorrectly promises a primary-monitor crop")
	}
	p.cast.platform = "windows"
	p.cast.cursor.SetChecked(false)
	if !strings.Contains(p.cast.helper.Text, "Cursor exclusion is unavailable on Windows") || !p.cast.cursor.Disabled() || !p.cast.button.Disabled() {
		t.Fatal("cursor exclusion omits the Windows driver limitation")
	}
}

func TestSelectionNeverRestartsOrSwitchesActiveCast(t *testing.T) {
	p, c, a := castingTestPanel(t)
	p.startCasting()
	awaitStart(t, c)
	p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Streaming})
	b, _ := receiver.Manual("Office", "192.168.1.41", receiver.AirPlay)
	p.inventory.Observe(b, time.Now())
	p.selectDevice(b.ID)
	p.render()
	p.startCasting()
	if p.cast.session.device.ID != a.ID || !strings.Contains(p.cast.message.Text, a.Name) || p.cast.button.Text != "Stop sharing" {
		t.Fatal("selection changed or concealed the active cast")
	}
	select {
	case <-c.starts:
		t.Fatal("active cast restarted without confirmation")
	default:
	}
}

func TestIdentityPromotionKeepsGlobalStopAndOriginalSessionEvents(t *testing.T) {
	p, c, manual := castingTestPanel(t)
	p.startCasting()
	call := awaitStart(t, c)
	discovered := manual
	discovered.ID = "airplay:aabbccddeeff"
	p.inventory.Observe(discovered, time.Now())
	p.render()
	if p.selectedID != discovered.ID || len(p.devices) != 1 {
		t.Fatal("the selected row did not follow the promoted hardware identity")
	}
	p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Streaming, DeviceID: manual.ID})
	p.startCasting()
	if p.cast.state.Text != "Streaming" || p.cast.button.Text != "Stop sharing" || !p.cast.session.busy() || p.cast.switchButton.Visible() {
		t.Fatal("identity promotion lost the original session's state or global stop")
	}
	select {
	case <-c.starts:
		t.Fatal("identity promotion allowed a second cast")
	default:
	}
	p.stopCasting()
	awaitStop(t, c)
	if call.ctx.Err() == nil || !p.cast.session.stopping {
		t.Fatal("the promoted row could not stop the original session")
	}
}

func TestSwitchRequiresConfirmationAndWaitsForStoppedSender(t *testing.T) {
	p, first, _ := castingTestPanel(t)
	p.startCasting()
	old := awaitStart(t, first)
	p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Streaming})
	destination, _ := receiver.Manual("Office", "192.168.1.41", receiver.AirPlay)
	p.inventory.Observe(destination, time.Now())
	p.selectDevice(destination.ID)
	p.render()
	second := newFakeCastingController()
	p.cast.factory = func(func(casting.Event)) castingController { return second }
	p.requestSwitch()
	if p.cast.switchDialog == nil || p.cast.pendingSwitch != nil {
		t.Fatal("switching must ask before changing the session")
	}
	declined := p.cast.switchDialog
	declined.Hide()
	declined.Confirm()
	if p.cast.session.event.State != casting.Streaming || p.cast.pendingSwitch != nil {
		t.Fatal("a dismissed or stale confirmation changed the cast")
	}
	p.requestSwitch()
	p.cast.switchDialog.Confirm()
	awaitStop(t, first)
	if p.cast.pendingSwitch == nil || p.cast.button.Text != "Cancel switch" || p.cast.button.Disabled() || old.ctx.Err() == nil {
		t.Fatal("confirmed switch did not stop the old sender with a cancel action")
	}
	select {
	case <-second.starts:
		t.Fatal("the replacement sender started before the old session stopped")
	default:
	}
	p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Idle})
	started := awaitStart(t, second)
	if started.device.ID != destination.ID || started.options != old.options || p.cast.session.event.State != casting.Connecting || p.cast.pendingSwitch != nil {
		t.Fatal("the confirmed receiver did not start with the original settings")
	}
	p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Streaming})
	if p.cast.session.event.State != casting.Connecting {
		t.Fatal("the old sender revived after a confirmed switch")
	}
}

func TestPendingSwitchCanBeCanceledAndNeverFollowsSelection(t *testing.T) {
	for _, action := range []string{"cancel", "selection", "failure"} {
		t.Run(action, func(t *testing.T) {
			p, first, original := castingTestPanel(t)
			p.startCasting()
			awaitStart(t, first)
			p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Streaming})
			destination, _ := receiver.Manual("Office", "192.168.1.41", receiver.AirPlay)
			p.inventory.Observe(destination, time.Now())
			p.selectDevice(destination.ID)
			p.render()
			p.requestSwitch()
			p.cast.switchDialog.Confirm()
			awaitStop(t, first)
			switch action {
			case "cancel":
				p.stopCasting()
			case "selection":
				p.selectDevice(original.ID)
				p.render()
			case "failure":
				p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Failed})
			}
			p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Idle})
			if p.cast.pendingSwitch != nil || p.cast.session.busy() || p.cast.generation != 1 {
				t.Fatal("a canceled, changed, or failed switch restarted sharing")
			}
		})
	}
}

func TestPairingCancelClearsMaskedCredentialAndStops(t *testing.T) {
	p, c, _ := castingTestPanel(t)
	p.startCasting()
	call := awaitStart(t, c)
	p.applyCastEvent(1, casting.Event{Type: "credential", State: casting.Pairing, CredentialKind: "pin"})
	entry, prompt := p.cast.credentialEntry, p.cast.credential
	if entry == nil || prompt == nil || !entry.Password || entry.Validator("") == nil {
		t.Fatal("pairing must use a masked, nonempty credential")
	}
	entry.SetText("1234")
	prompt.Hide()
	awaitStop(t, c)
	if entry.Text != "" || p.cast.credential != nil || !p.cast.session.stopping || call.ctx.Err() == nil {
		t.Fatal("pairing cancellation did not clear the secret and cancel capture")
	}
	p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Streaming})
	if p.cast.state.Text != "Stopping" {
		t.Fatal("late startup event revived a canceled pairing session")
	}
}

func TestPairingTransitionDismissesWithoutCanceling(t *testing.T) {
	p, c, _ := castingTestPanel(t)
	p.startCasting()
	awaitStart(t, c)
	p.applyCastEvent(1, casting.Event{Type: "credential", CredentialKind: "password"})
	entry, oldPrompt := p.cast.credentialEntry, p.cast.credential
	entry.SetText("fixture password")
	p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Starting})
	oldPrompt.Hide()
	if p.cast.credential != nil || entry.Text != "" || p.cast.session.stopping {
		t.Fatal("programmatic prompt dismissal canceled a valid session transition")
	}
	select {
	case <-c.stops:
		t.Fatal("state-driven dismissal sent Stop")
	default:
	}
}

func TestNewPairingChallengeReplacesAndInvalidatesOldPrompt(t *testing.T) {
	p, c, _ := castingTestPanel(t)
	p.startCasting()
	awaitStart(t, c)
	p.applyCastEvent(1, casting.Event{Type: "credential", CredentialKind: "pin"})
	oldEntry, oldPrompt := p.cast.credentialEntry, p.cast.credential
	oldEntry.SetText("1234")
	p.applyCastEvent(1, casting.Event{Type: "credential", CredentialKind: "password"})
	if p.cast.credential == oldPrompt || oldEntry.Text != "" || p.cast.credentialEntry.PlaceHolder != "Password" {
		t.Fatal("a new pairing challenge retained the old prompt or credential")
	}
	oldPrompt.Hide()
	if p.cast.session.stopping || p.cast.credential == nil {
		t.Fatal("a stale prompt canceled the new pairing challenge")
	}
	p.cast.credentialEntry.SetText("fixture password")
	p.cast.credential.Submit()
	select {
	case value := <-c.credentials:
		if value != "fixture password" {
			t.Fatal("the new challenge received the previous credential")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the replacement prompt did not submit")
	}
}

func TestCredentialSubmissionPreservesPasswordAndClearsEntry(t *testing.T) {
	for _, kind := range []string{"pin", "password"} {
		t.Run(kind, func(t *testing.T) {
			p, c, _ := castingTestPanel(t)
			p.startCasting()
			awaitStart(t, c)
			p.applyCastEvent(1, casting.Event{Type: "credential", CredentialKind: kind})
			entry, prompt := p.cast.credentialEntry, p.cast.credential
			value, want := " 1234 ", "1234"
			if kind == "password" {
				value, want = " fixture password ", " fixture password "
			}
			entry.SetText(value)
			prompt.Submit()
			select {
			case got := <-c.credentials:
				if got != want {
					t.Fatal("credential was changed unexpectedly")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("credential was not submitted")
			}
			if entry.Text != "" || p.cast.credentialEntry != nil || strings.Contains(p.cast.message.Text, want) {
				t.Fatal("credential remained in an entry or a status label")
			}
		})
	}
}

func TestTerminalAndPreviousControllerCallbacksCannotReviveSession(t *testing.T) {
	p, first, d := castingTestPanel(t)
	p.startCasting()
	awaitStart(t, first)
	p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Idle})
	p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Streaming})
	if p.cast.session.busy() {
		t.Fatal("a terminal cast accepted a late streaming event")
	}
	second := newFakeCastingController()
	p.cast.factory = func(func(casting.Event)) castingController { return second }
	p.startCasting()
	awaitStart(t, second)
	p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Streaming, DeviceID: d.ID})
	p.applyCastEvent(2, casting.Event{Type: "state", State: casting.Streaming, DeviceID: "another-receiver"})
	if p.cast.session.event.State != casting.Connecting {
		t.Fatal("a previous controller or wrong receiver updated the new cast")
	}
	p.applyCastEvent(2, casting.Event{Type: "state", State: casting.Streaming, DeviceID: d.ID})
	if p.cast.session.event.State != casting.Streaming {
		t.Fatal("the current controller's event was rejected")
	}
}

func TestCastSessionStatsAreMonotonicAndNeverInventFormat(t *testing.T) {
	d, _ := receiver.Manual("TV", "192.168.1.40", receiver.AirPlay)
	var session castSession
	session.begin(4, d)
	if mediaSummary(session.event) != "" {
		t.Fatal("format was advertised before negotiation")
	}
	session.apply(4, casting.Event{Type: "stats", Frames: 20, Bytes: 2048})
	session.apply(4, casting.Event{Type: "stats", Frames: 10, Bytes: 1024})
	if session.event.State != casting.Connecting || session.event.Frames != 20 || session.event.Bytes != 2048 {
		t.Fatal("stats regressed or incorrectly changed cast state")
	}
	session.apply(4, casting.Event{Type: "state", State: casting.Failed})
	if session.busy() || session.apply(4, casting.Event{Type: "credential", CredentialKind: "pin"}) {
		t.Fatal("failed session accepted a stale pairing request")
	}
}

func TestStatsPreserveSupervisorWarnings(t *testing.T) {
	p, c, _ := castingTestPanel(t)
	p.startCasting()
	awaitStart(t, c)
	notice := "Screen sharing is transmitting. Using GDI compatibility capture instead of DXGI. Pairing is session-only; no secrets are saved to disk"
	p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Streaming, Message: notice})
	p.applyCastEvent(1, casting.Event{Type: "stats", Frames: 25, Message: notice + ". System audio stopped. Sharing video only"})
	for _, text := range []string{"GDI compatibility capture", "session-only", "System audio stopped"} {
		if !strings.Contains(p.cast.message.Text, text) {
			t.Fatal("a supervisor warning disappeared during statistics updates")
		}
	}
}

func TestCredentialValidationMatchesSenderBoundaries(t *testing.T) {
	for _, fixture := range []struct {
		kind  string
		value string
		valid bool
	}{
		{"pin", "1234", true}, {"pin", " 1234 ", true}, {"pin", "", false},
		{"pin", "12ab", false}, {"pin", "１２３４", false}, {"pin", strings.Repeat("1", 17), false},
		{"password", " fixture password ", true}, {"password", "", false},
		{"password", "line\nbreak", false}, {"password", "hidden\u200bvalue", false},
		{"password", string([]byte{0xff}), false}, {"password", strings.Repeat("a", 1025), false},
	} {
		if (validateCredential(fixture.kind, fixture.value) == nil) != fixture.valid {
			t.Fatalf("unexpected %s credential validation result", fixture.kind)
		}
	}
}

func TestCloseWithoutTrayConfirmsActiveSession(t *testing.T) {
	p, c, d := castingTestPanel(t)
	p.cast.session.begin(1, d)
	p.trayAvailable = false
	quit := make(chan struct{}, 1)
	p.quit = func() { quit <- struct{}{} }
	p.closeWindow()
	if p.quitDialog == nil || p.quitting || p.cast.closed.Load() {
		t.Fatal("closing an active session without a tray must ask first")
	}
	p.quitDialog.Hide()
	if p.quitting || !p.cast.session.busy() {
		t.Fatal("dismissing quit confirmation stopped the session")
	}
	p.closeWindow()
	p.quitDialog.Confirm()
	select {
	case <-quit:
	case <-time.After(2 * time.Second):
		t.Fatal("confirmed quit did not finish sender cleanup")
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed != 1 || !p.cast.closed.Load() || p.ctx.Err() == nil {
		t.Fatal("quit did not close the sender exactly once")
	}
}

func TestCloseWithTrayDoesNotStopActiveSession(t *testing.T) {
	p, _, d := castingTestPanel(t)
	p.cast.session.begin(1, d)
	p.trayAvailable = true
	p.closeWindow()
	if p.quitDialog != nil || p.quitting || p.cast.closed.Load() || !p.cast.session.busy() {
		t.Fatal("hiding a tray-backed window changed the cast")
	}
}

func TestCloseIsIdempotentAndRejectsLateEvents(t *testing.T) {
	p, c, d := castingTestPanel(t)
	p.cast.session.begin(1, d)
	want := errors.New("fixture close error")
	c.closeErr = want
	probeCtx, cancelProbe := context.WithCancel(p.ctx)
	p.probeCancel = cancelProbe
	sessionCtx, cancelSession := context.WithCancel(p.ctx)
	p.cast.sessionCancel = cancelSession
	before := p.cast.state.Text
	p.Close()
	p.Close()
	p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Streaming})
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed != 1 || p.ctx.Err() == nil || probeCtx.Err() == nil || sessionCtx.Err() == nil || p.cast.session.event.State == casting.Streaming || p.cast.state.Text != before {
		t.Fatal("Close repeated cleanup or accepted a late event")
	}
}

func TestCloseWaitsForRetiringControllerDuringRetry(t *testing.T) {
	p, first, _ := castingTestPanel(t)
	p.startCasting()
	awaitStart(t, first)
	p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Idle})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	first.closeStarted, first.closeRelease = make(chan struct{}, 1), release
	second := newFakeCastingController()
	second.closeStarted = make(chan struct{}, 1)
	p.cast.factory = func(func(casting.Event)) castingController { return second }
	p.startCasting()
	select {
	case <-first.closeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("retry did not retire its previous controller")
	}
	closed := make(chan struct{})
	go func() { p.Close(); close(closed) }()
	select {
	case <-second.closeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not close the replacement controller")
	}
	select {
	case <-closed:
		t.Fatal("Close returned before the retiring controller finished cleanup")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not join completed retiring cleanup")
	}
	select {
	case <-second.starts:
		t.Fatal("the canceled replacement started after Close")
	default:
	}
}

func TestWindowsCursorExclusionCannotStart(t *testing.T) {
	p, controller, _ := castingTestPanel(t)
	if !p.cast.cursor.Checked || !p.cast.cursor.Disabled() {
		t.Fatal("Windows should require cursor sharing")
	}
	p.cast.cursor.SetChecked(false)
	p.startCasting()
	if !p.cast.button.Disabled() || !strings.Contains(p.cast.message.Text, "Windows capture requires cursor sharing") {
		t.Fatal("unsupported cursor exclusion did not explain why sharing is blocked")
	}
	select {
	case <-controller.starts:
		t.Fatal("unsupported Windows cursor exclusion reached the sender")
	default:
	}
}

func TestStopDuringRetryCannotBypassRetiringCleanup(t *testing.T) {
	p, first, _ := castingTestPanel(t)
	p.startCasting()
	awaitStart(t, first)
	p.applyCastEvent(1, casting.Event{Type: "state", State: casting.Idle})
	release := make(chan struct{})
	defer close(release)
	first.closeStarted, first.closeRelease = make(chan struct{}, 1), release
	second := newFakeCastingController()
	second.snapshot.State = casting.Idle
	factories := 0
	p.cast.factory = func(func(casting.Event)) castingController { factories++; return second }
	p.startCasting()
	select {
	case <-first.closeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("previous sender did not enter retirement")
	}
	p.stopCasting()
	awaitStop(t, second)
	time.Sleep(50 * time.Millisecond)
	if !p.cast.session.busy() || !p.cast.session.stopping {
		t.Fatal("an unstarted replacement's idle snapshot bypassed retiring cleanup")
	}
	p.startCasting()
	if factories != 1 {
		t.Fatal("another sender was created before retiring cleanup completed")
	}
	select {
	case <-second.starts:
		t.Fatal("the canceled replacement started before retiring cleanup")
	default:
	}
}
