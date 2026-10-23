package ui

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"aircast/internal/casting"
	"aircast/internal/receiver"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type castingController interface {
	Start(context.Context, receiver.Device, casting.Options) error
	Stop() error
	SubmitCredential(string) error
	Snapshot() casting.Event
	Close() error
	Available() error
}

type castingPanel struct {
	mu               sync.Mutex
	controller       castingController
	factory          func(func(casting.Event)) castingController
	closed           atomic.Bool
	closeOnce        sync.Once
	retiring         sync.WaitGroup
	generation       uint64
	used             bool
	checking         bool
	available        bool
	availabilityErr  error
	session          castSession
	sessionCancel    context.CancelFunc
	startupReady     <-chan struct{}
	activeOptions    casting.Options
	pendingSwitch    *castSwitch
	switchDialog     *dialog.ConfirmDialog
	switchPrompt     uint64
	stopErr          bool
	platform         string
	box              *fyne.Container
	state            *widget.Label
	source           *widget.Label
	quality          *widget.Select
	audio            *widget.Check
	cursor           *widget.Check
	helper           *widget.Label
	message          *widget.Label
	stats            *widget.Label
	button           *widget.Button
	switchButton     *widget.Button
	credential       *dialog.FormDialog
	credentialEntry  *widget.Entry
	credentialPrompt uint64
}

func (p *Panel) buildCasting() {
	p.cast.platform = runtime.GOOS
	defaults := defaultCastOptions()
	p.cast.state = widget.NewLabel("Not sharing")
	p.cast.state.Importance = widget.LowImportance
	heading := widget.NewLabelWithStyle("Screen sharing", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	heading.SizeName = theme.SizeNameSubHeadingText
	p.cast.quality = widget.NewSelect([]string{bestQualityLabel, balancedQualityLabel}, func(string) { p.renderCasting() })
	p.cast.quality.SetSelected(bestQualityLabel)
	p.cast.audio = widget.NewCheck("System audio (beta)", nil)
	p.cast.audio.SetChecked(defaults.Audio)
	p.cast.cursor = widget.NewCheck("Show cursor", func(bool) { p.renderCasting() })
	p.cast.cursor.SetChecked(defaults.ShowCursor)
	p.cast.helper = widget.NewLabel("")
	p.cast.helper.Wrapping, p.cast.helper.Importance = fyne.TextWrapWord, widget.LowImportance
	p.cast.helper.SizeName = theme.SizeNameCaptionText
	p.cast.message = widget.NewLabel("")
	p.cast.message.Wrapping = fyne.TextWrapWord
	p.cast.stats = widget.NewLabel("")
	p.cast.stats.Wrapping, p.cast.stats.Importance = fyne.TextWrapWord, widget.LowImportance
	p.cast.stats.SizeName = theme.SizeNameCaptionText
	p.cast.button = widget.NewButtonWithIcon("Start sharing", theme.MediaPlayIcon(), func() {
		if p.cast.session.busy() {
			p.stopCasting()
		} else {
			p.startCasting()
		}
	})
	p.cast.button.Importance = widget.HighImportance
	p.cast.source = widget.NewLabel("")
	source := container.NewHBox(widget.NewIcon(theme.ComputerIcon()), p.cast.source)
	options := container.NewBorder(nil, nil, source, nil, p.cast.quality)
	checks := container.NewHBox(p.cast.audio, p.cast.cursor)
	p.cast.switchButton = widget.NewButton("Switch receiver", p.requestSwitch)
	p.cast.switchButton.Importance = widget.LowImportance
	action := container.NewBorder(nil, nil, nil, p.cast.button, p.cast.message)
	p.cast.box = container.NewVBox(container.NewBorder(nil, nil, heading, p.cast.state, nil), options,
		container.NewBorder(nil, nil, checks, p.cast.switchButton, nil), p.cast.helper, p.cast.stats, action)
}

func (p *Panel) castOptions() casting.Options {
	options := defaultCastOptions()
	if p.cast.quality.Selected == balancedQualityLabel {
		options.Quality = casting.Balanced
	}
	options.Audio, options.ShowCursor = p.cast.audio.Checked, p.cast.cursor.Checked
	return options
}

func unsupportedReceiver(device receiver.Device) string {
	switch device.Kind {
	case receiver.AirPlay:
		return ""
	case receiver.Cast:
		return "Google Cast screen sharing is not supported yet. You can still save or check this receiver."
	case receiver.Audio:
		return "This is an audio-only AirPlay receiver. Audio-only streaming is not supported yet."
	default:
		return "Select an AirPlay receiver to share your screen."
	}
}

func (p *Panel) startReason() string {
	device, ok := p.selected()
	if !ok {
		return "Select an AirPlay receiver above."
	}
	if reason := unsupportedReceiver(device); reason != "" {
		return reason
	}
	if p.cast.platform != "windows" && p.cast.platform != "linux" {
		return "Screen sharing is available on Windows and Linux only."
	}
	if p.cast.platform == "windows" && !p.cast.cursor.Checked {
		return "Windows capture requires cursor sharing. Enable Show cursor to start."
	}
	if p.cast.checking {
		return "Checking the local AirPlay sender..."
	}
	if p.cast.availabilityErr != nil {
		return "AirPlay sender unavailable. " + p.cast.availabilityErr.Error()
	}
	if !p.cast.available {
		return "The local AirPlay sender has not been checked."
	}
	return ""
}

func (p *Panel) renderCasting() {
	if p.cast.box == nil {
		return
	}
	session := &p.cast.session
	busy := session.busy()
	reason := p.startReason()
	editable := !busy && reason == "" && !p.quitting && !p.cast.closed.Load()
	if editable {
		p.cast.quality.Enable()
		p.cast.audio.Enable()
		p.cast.cursor.Enable()
	} else {
		p.cast.quality.Disable()
		p.cast.audio.Disable()
		p.cast.cursor.Disable()
	}
	if p.cast.platform == "windows" {
		p.cast.cursor.Disable()
	}
	state := "Not sharing"
	p.cast.state.Importance = widget.LowImportance
	message := reason
	if message == "" {
		message = "Nothing is being shared."
	}
	helper := "Best available prioritizes detail. The receiver sets the final resolution."
	if p.cast.quality.Selected == balancedQualityLabel {
		helper = "Balanced uses less bandwidth. The receiver sets the final resolution."
	}
	switch p.cast.platform {
	case "windows":
		helper += " Sharing includes notifications and the cursor. Cursor exclusion is unavailable on Windows. System audio is beta."
	case "linux":
		helper += " X11 may include all monitors. Wayland uses the system screen picker. Audio is beta."
	default:
		helper = "Screen capture requires Windows or Linux. Receiver discovery and checks remain available."
	}
	p.cast.source.SetText(captureSource(p.cast.platform))
	p.cast.button.SetText("Start sharing")
	p.cast.button.SetIcon(theme.MediaPlayIcon())
	p.cast.button.Importance = widget.HighImportance
	if session.started {
		switch session.event.State {
		case casting.Connecting:
			state, message = "Connecting", "Connecting to "+session.device.Name+"..."
		case casting.Pairing:
			state, message = "Pairing", "Waiting for pairing with "+session.device.Name+"."
		case casting.Starting:
			state, message = "Preparing", "Preparing screen capture for "+session.device.Name+"..."
		case casting.Streaming:
			state, message = "Streaming", "Sending your screen to "+session.device.Name+"."
			p.cast.state.Importance = widget.SuccessImportance
		case casting.Stopping:
			state, message = "Stopping", "Stopping sharing with "+session.device.Name+"..."
		case casting.Failed:
			state, message = "Sharing failed", "The sender reported a failure. Check the receiver and try again."
			p.cast.state.Importance = widget.DangerImportance
		case casting.Idle:
			message = "Sharing stopped."
		}
		if session.event.Message != "" {
			message = session.event.Message
			if busy && !strings.Contains(message, session.device.Name) {
				message = session.device.Name + ": " + message
			}
		}
		if !busy {
			message = "Last session with " + session.device.Name + ": " + message
		}
		if !busy && reason != "" {
			message += "\n" + reason
		}
	}
	if busy {
		helper = "Settings are locked while sharing. Select another AirPlay receiver to switch with confirmation."
		p.cast.button.SetText("Stop sharing")
		p.cast.button.SetIcon(theme.MediaStopIcon())
		p.cast.button.Importance = widget.DangerImportance
		if session.stopping && !p.cast.stopErr {
			p.cast.button.SetText("Stopping...")
		} else if p.cast.stopErr {
			p.cast.button.SetText("Retry stop")
		}
	}
	if p.cast.pendingSwitch != nil {
		helper = "Waiting for the current sender to stop before switching to " + p.cast.pendingSwitch.device.Name + "."
		p.cast.button.SetText("Cancel switch")
		p.cast.button.Importance = widget.MediumImportance
	}
	if p.canSwitch() {
		p.cast.switchButton.Show()
	} else {
		p.cast.switchButton.Hide()
	}
	if p.quitting {
		message = "Closing the AirPlay sender..."
	}
	p.cast.state.SetText(state)
	p.cast.state.Refresh()
	p.cast.helper.SetText(helper)
	p.cast.message.SetText(message)
	summary := mediaSummary(session.event)
	if summary == "" {
		p.cast.stats.Hide()
	} else {
		if !busy {
			summary = "Last session: " + summary
		}
		p.cast.stats.SetText(summary)
		p.cast.stats.Show()
	}
	if p.quitting || p.cast.closed.Load() || busy && session.stopping && !p.cast.stopErr && p.cast.pendingSwitch == nil || !busy && reason != "" {
		p.cast.button.Disable()
	} else {
		p.cast.button.Enable()
	}
	p.cast.button.Refresh()
}

func (p *Panel) replaceSender() (castingController, castingController, uint64) {
	p.cast.mu.Lock()
	defer p.cast.mu.Unlock()
	if p.cast.closed.Load() {
		return nil, nil, 0
	}
	if p.cast.factory == nil {
		p.cast.factory = func(onEvent func(casting.Event)) castingController { return casting.New(onEvent) }
	}
	p.cast.generation++
	generation := p.cast.generation
	previous := p.cast.controller
	if previous != nil {
		p.cast.retiring.Add(1)
	}
	p.cast.controller = p.cast.factory(func(event casting.Event) {
		fyne.Do(func() { p.applyCastEvent(generation, event) })
	})
	p.cast.used = false
	return p.cast.controller, previous, generation
}

func (p *Panel) sender() castingController {
	p.cast.mu.Lock()
	defer p.cast.mu.Unlock()
	return p.cast.controller
}

func (p *Panel) startSender() {
	controller, previous, generation := p.replaceSender()
	if controller == nil {
		return
	}
	p.cast.checking = true
	p.renderCasting()
	ctx := p.ctx
	go func() {
		p.closePrevious(previous)
		err := controller.Available()
		fyne.Do(func() {
			if p.cast.closed.Load() || ctx.Err() != nil || generation != p.cast.generation {
				return
			}
			p.cast.checking, p.cast.available, p.cast.availabilityErr = false, err == nil, err
			p.renderCasting()
		})
	}()
}

func (p *Panel) startCasting() {
	if p.quitting || p.cast.closed.Load() || p.ctx.Err() != nil || p.cast.session.busy() || p.startReason() != "" {
		return
	}
	device, ok := p.selected()
	if !ok {
		return
	}
	p.startDevice(device, p.castOptions())
}

func (p *Panel) startDevice(device receiver.Device, options casting.Options) {
	if p.quitting || p.cast.closed.Load() || p.ctx.Err() != nil || p.cast.session.busy() {
		return
	}
	controller := p.sender()
	var previous castingController
	if p.cast.used {
		controller, previous, _ = p.replaceSender()
	}
	if controller == nil {
		return
	}
	p.dismissCredential()
	p.cast.used, p.cast.stopErr = true, false
	generation := p.cast.generation
	p.cast.session.begin(generation, device)
	ctx, cancel := context.WithCancel(p.ctx)
	p.cast.sessionCancel = cancel
	p.cast.activeOptions = options
	startupReady := make(chan struct{})
	p.cast.startupReady = startupReady
	p.renderCasting()
	go func() {
		var err error
		if previous != nil {
			err = p.closePrevious(previous)
		}
		close(startupReady)
		if err == nil {
			if ctx.Err() != nil {
				fyne.Do(func() {
					p.applyCastEvent(generation, casting.Event{Type: "state", State: casting.Idle, DeviceID: device.ID, Message: "Sharing canceled before the sender started."})
				})
				return
			}
			err = controller.Start(ctx, device, options)
		}
		if err != nil {
			event := casting.Event{Type: "state", State: casting.Failed, DeviceID: device.ID, Message: err.Error()}
			if ctx.Err() != nil {
				if controller.Close() != nil {
					fyne.Do(func() { p.stoppingFailed(generation) })
					return
				}
				event.State, event.Message = casting.Idle, "Sharing canceled."
			}
			fyne.Do(func() {
				p.applyCastEvent(generation, event)
			})
		}
	}()
}

func (p *Panel) closePrevious(controller castingController) error {
	if controller == nil {
		return nil
	}
	defer p.cast.retiring.Done()
	return controller.Close()
}

func (p *Panel) applyCastEvent(generation uint64, event casting.Event) {
	if p.cast.closed.Load() || p.ctx.Err() != nil || !p.cast.session.apply(generation, event) {
		return
	}
	if p.cast.session.terminal {
		if p.cast.sessionCancel != nil {
			p.cast.sessionCancel()
			p.cast.sessionCancel = nil
		}
		p.dismissCredential()
		p.finishSwitch(generation)
	} else if event.Type == "credential" {
		p.showCredential(generation, event.CredentialKind)
	} else if p.cast.session.event.State != casting.Pairing {
		p.dismissCredential()
	}
	p.renderCasting()
}

func (p *Panel) stopCasting() {
	p.cancelSwitch()
	p.stopSession()
	p.renderCasting()
}

func (p *Panel) stopSession() {
	if p.cast.closed.Load() || !p.cast.session.busy() || p.cast.session.stopping && !p.cast.stopErr {
		return
	}
	controller := p.sender()
	if controller == nil {
		return
	}
	p.cast.stopErr = false
	p.cast.session.stop()
	p.dismissCredential()
	if p.cast.sessionCancel != nil {
		p.cast.sessionCancel()
	}
	generation := p.cast.session.generation
	startupReady := p.cast.startupReady
	p.renderCasting()
	go func() {
		err := controller.Stop()
		if startupReady != nil {
			<-startupReady
		}
		snapshot := controller.Snapshot()
		if snapshot.State == casting.Idle || snapshot.State == casting.Failed {
			snapshot.Type = "state"
			fyne.Do(func() { p.applyCastEvent(generation, snapshot) })
		} else if err != nil {
			fyne.Do(func() { p.stoppingFailed(generation) })
		}
	}()
}

func (p *Panel) stoppingFailed(generation uint64) {
	if p.cast.closed.Load() || generation != p.cast.session.generation || !p.cast.session.stopping || p.cast.session.terminal {
		return
	}
	p.cast.stopErr = true
	p.cast.session.event.Message = "The sender did not confirm stopping. Retry stop, or quit Aircast to close it."
	p.renderCasting()
}

func (p *Panel) dismissCredential() {
	p.cast.credentialPrompt++
	entry, prompt := p.cast.credentialEntry, p.cast.credential
	p.cast.credentialEntry, p.cast.credential = nil, nil
	if entry != nil {
		entry.SetText("")
	}
	if prompt != nil {
		prompt.Hide()
	}
}

func (p *Panel) showCredential(generation uint64, kind string) {
	if generation != p.cast.session.generation || !p.cast.session.busy() || p.cast.session.stopping {
		return
	}
	p.dismissCredential()
	if kind != "pin" && kind != "password" {
		p.stopCasting()
		p.cast.session.event.Message = "The receiver requested an unsupported pairing method. Stopping the session."
		return
	}
	entry := widget.NewPasswordEntry()
	label, hint := "PIN", "Enter the code shown on the TV. Cancel stops the session."
	if kind == "password" {
		label, hint = "Password", "Enter the receiver's AirPlay password. Cancel stops the session."
	}
	entry.SetPlaceHolder(label)
	entry.Validator = func(value string) error { return validateCredential(kind, value) }
	item := widget.NewFormItem(label, entry)
	item.HintText = hint
	p.cast.credentialPrompt++
	promptID := p.cast.credentialPrompt
	name := []rune(p.cast.session.device.Name)
	if len(name) > 36 {
		name = append(name[:35], '…')
	}
	prompt := dialog.NewForm("Pair with "+string(name), "Continue", "Cancel sharing", []*widget.FormItem{item}, func(ok bool) {
		if promptID != p.cast.credentialPrompt || generation != p.cast.session.generation || p.cast.closed.Load() {
			return
		}
		value := entry.Text
		p.cast.credentialPrompt++
		p.cast.credential, p.cast.credentialEntry = nil, nil
		entry.SetText("")
		if !ok {
			p.stopCasting()
			return
		}
		if kind == "pin" {
			value = strings.TrimSpace(value)
		}
		p.sendCredential(generation, value)
	}, p.window)
	entry.OnSubmitted = func(string) { prompt.Submit() }
	p.cast.credential, p.cast.credentialEntry = prompt, entry
	p.window.Show()
	prompt.Resize(fyne.NewSize(440, 220))
	prompt.Show()
	p.window.Canvas().Focus(entry)
}

func (p *Panel) sendCredential(generation uint64, value string) {
	if p.cast.closed.Load() || generation != p.cast.session.generation || !p.cast.session.busy() || p.cast.session.stopping {
		return
	}
	controller := p.sender()
	if controller == nil {
		return
	}
	p.cast.session.event.Message = "Waiting for the receiver to accept pairing..."
	p.renderCasting()
	promptID := p.cast.credentialPrompt
	go func() {
		if err := controller.SubmitCredential(value); err != nil {
			fyne.Do(func() {
				if p.cast.closed.Load() || generation != p.cast.session.generation || promptID != p.cast.credentialPrompt || p.cast.session.event.State != casting.Pairing || !p.cast.session.busy() || p.cast.session.stopping {
					return
				}
				p.stopCasting()
				p.cast.session.event.Message = "The credential could not be sent. Stopping the session."
				p.renderCasting()
			})
		}
	}()
}

func (p *Panel) closeWindow() {
	if p.trayAvailable {
		p.window.Hide()
		return
	}
	p.requestQuit()
}

func (p *Panel) requestQuit() {
	if p.quitting || p.quitDialog != nil {
		return
	}
	if !p.cast.session.busy() {
		p.finishQuit()
		return
	}
	p.window.Show()
	p.quitDialog = dialog.NewConfirm("Stop sharing and quit?", fmt.Sprintf("An AirPlay session with %s is active. Quitting stops capture and closes the sender.", p.cast.session.device.Name), func(ok bool) {
		p.quitDialog = nil
		if ok {
			p.finishQuit()
		}
	}, p.window)
	p.quitDialog.SetConfirmText("Stop and quit")
	p.quitDialog.SetDismissText("Keep Aircast open")
	p.quitDialog.Show()
}

func (p *Panel) finishQuit() {
	if p.quitting {
		return
	}
	p.quitting = true
	p.cancelProbe()
	p.cancelSwitch()
	p.dismissCredential()
	p.renderCasting()
	quit := p.quit
	go func() {
		p.Close()
		fyne.Do(quit)
	}()
}

func (p *Panel) Close() {
	p.cast.closeOnce.Do(func() {
		p.cast.closed.Store(true)
		p.cast.mu.Lock()
		controller, cancel := p.cast.controller, p.cancel
		p.cast.controller = nil
		p.cast.mu.Unlock()
		cancel()
		if controller != nil {
			controller.Close()
		}
		p.cast.retiring.Wait()
	})
}
