package ui

import (
	"fmt"

	"aircast/internal/casting"
	"aircast/internal/receiver"

	"fyne.io/fyne/v2/dialog"
)

type castSwitch struct {
	generation uint64
	device     receiver.Device
	options    casting.Options
}

func (p *Panel) canSwitch() bool {
	if p.quitting || p.cast.closed.Load() || !p.cast.session.busy() || p.cast.session.stopping || p.cast.pendingSwitch != nil || p.startReason() != "" {
		return false
	}
	device, ok := p.selected()
	return ok && device.ID != p.inventory.ResolveID(p.cast.session.device.ID)
}

func (p *Panel) requestSwitch() {
	if !p.canSwitch() || p.cast.switchDialog != nil {
		return
	}
	device, _ := p.selected()
	request := castSwitch{generation: p.cast.session.generation, device: device, options: p.cast.activeOptions}
	p.cast.switchPrompt++
	promptID := p.cast.switchPrompt
	p.window.Show()
	p.cast.switchDialog = dialog.NewConfirm("Switch screen sharing?", fmt.Sprintf("Stop sharing with %s and share your screen with %s instead? Your current quality, audio, and cursor settings will stay the same.", p.cast.session.device.Name, device.Name), func(ok bool) {
		if promptID != p.cast.switchPrompt {
			return
		}
		p.cast.switchPrompt++
		p.cast.switchDialog = nil
		if !ok || p.quitting || p.cast.closed.Load() || request.generation != p.cast.session.generation || !p.canSwitch() {
			return
		}
		selected, valid := p.selected()
		if !valid || selected.ID != p.inventory.ResolveID(request.device.ID) || selected.Endpoint() != request.device.Endpoint() {
			return
		}
		p.cast.pendingSwitch = &request
		p.stopSession()
	}, p.window)
	p.cast.switchDialog.SetConfirmText("Stop and switch")
	p.cast.switchDialog.SetDismissText("Keep current receiver")
	p.cast.switchDialog.Show()
}

func (p *Panel) cancelSwitch() {
	p.cast.pendingSwitch = nil
	p.cast.switchPrompt++
	prompt := p.cast.switchDialog
	p.cast.switchDialog = nil
	if prompt != nil {
		prompt.Hide()
	}
}

func (p *Panel) finishSwitch(generation uint64) {
	request := p.cast.pendingSwitch
	p.cancelSwitch()
	if request == nil {
		return
	}
	if p.quitting || p.cast.closed.Load() || generation != request.generation || p.cast.session.event.State != casting.Idle || p.startReason() != "" {
		return
	}
	selected, ok := p.selected()
	if !ok || selected.ID != p.inventory.ResolveID(request.device.ID) || selected.Endpoint() != request.device.Endpoint() {
		return
	}
	p.startDevice(selected, request.options)
}
