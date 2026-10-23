package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"aircast/internal/config"
	"aircast/internal/diagnostics"
	"aircast/internal/receiver"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func newTestPanel(t *testing.T) *Panel {
	t.Helper()
	a := test.NewApp()
	t.Cleanup(a.Quit)
	p := New(a, filepath.Join(t.TempDir(), "receivers.json"))
	t.Cleanup(p.window.Close)
	t.Cleanup(func() { p.Close() })
	p.window.Show()
	return p
}

func TestEmptyStateAndNoStreamingClaim(t *testing.T) {
	p := newTestPanel(t)
	if !p.empty.Visible() || len(p.devices) != 0 || p.selectedID != "" {
		t.Fatal("empty state is incorrect")
	}
	if len(p.details.Objects) != 1 {
		t.Fatal("unexpected connection controls without a receiver")
	}
}

func TestSelectionSurvivesReorderAndSaving(t *testing.T) {
	p := newTestPanel(t)
	now := time.Now()
	a, _ := receiver.Manual("A room", "192.168.1.40", receiver.AirPlay)
	b, _ := receiver.Manual("B room", "192.168.1.41", receiver.Cast)
	p.inventory.Observe(a, now)
	p.inventory.Observe(b, now)
	p.render()
	p.list.Select(1)
	if p.selectedID != b.ID || p.empty.Visible() {
		t.Fatal("receiver selection failed")
	}
	p.saveDevice(b)
	if p.selectedID != b.ID || p.devices[0].ID != b.ID {
		t.Fatal("selection followed the row index instead of the receiver")
	}
	saved, err := config.Load(p.configPath)
	if err != nil || len(saved) != 1 || saved[0].ID != b.ID {
		t.Fatalf("saved %#v %v", saved, err)
	}
	address := p.details.Objects[1].(*fyne.Container).Objects[0].(*fyne.Container).Objects[1].(*widget.Label)
	if !address.Selectable || address.Text != b.Endpoint() {
		t.Fatal("address is not selectable or belongs to a different receiver")
	}
}

func TestExpiredSelectionAndProbeGeneration(t *testing.T) {
	p := newTestPanel(t)
	d, _ := receiver.Manual("TV", "192.168.1.40", receiver.AirPlay)
	p.inventory.Observe(d, time.Now().Add(-receiver.DiscoveryLifetime+time.Second))
	p.render()
	p.list.Select(0)
	p.probing = true
	previous := p.probeID
	p.inventory.Snapshot(time.Now().Add(2 * time.Second))
	p.render()
	if p.selectedID != "" || p.probing || p.probeID <= previous {
		t.Fatal("expired receiver retained a pending diagnostic")
	}
}

func TestCorruptConfigurationDoesNotGetOverwritten(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	path := filepath.Join(t.TempDir(), "receivers.json")
	os.WriteFile(path, []byte("unreadable fixture"), 0600)
	p := New(a, path)
	defer p.window.Close()
	defer p.Close()
	if p.configErr == nil {
		t.Fatal("configuration failure was hidden")
	}
	d, _ := receiver.Manual("TV", "192.168.1.40", receiver.AirPlay)
	p.inventory.Observe(d, time.Now())
	p.render()
	p.list.Select(0)
	buttons := p.details.Objects[1].(*fyne.Container).Objects[1].(*fyne.Container)
	if !buttons.Objects[1].(*widget.Button).Disabled() {
		t.Fatal("saving enabled despite unreadable existing configuration")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "unreadable fixture" {
		t.Fatal("existing configuration changed")
	}
}

func TestAddingReceiverCancelsAndResetsPreviousProbe(t *testing.T) {
	p := newTestPanel(t)
	a, _ := receiver.Manual("A room", "192.168.1.40", receiver.AirPlay)
	b, _ := receiver.Manual("B room", "192.168.1.41", receiver.AirPlay)
	p.inventory.Observe(a, time.Now())
	p.render()
	p.list.Select(0)
	ctx, cancel := context.WithCancel(p.ctx)
	p.probeCancel, p.probing, p.probeText = cancel, true, "Checking A..."
	previous := p.probeID
	p.saveDevice(b)
	if ctx.Err() == nil || p.probing || p.probeID <= previous || p.selectedID != b.ID {
		t.Fatal("adding B did not cancel A's pending probe before changing selection")
	}
	if p.probeText == "Checking A..." {
		t.Fatal("B inherited A's diagnostic text")
	}
	p.completeProbe(previous, a.ID, a.Endpoint(), diagnostics.Result{Summary: "Stale A response"}, nil)
	if p.selectedID != b.ID || p.probeText == "Stale A response. " || p.probing {
		t.Fatal("A's queued result overwrote the new receiver")
	}
	buttons := p.details.Objects[1].(*fyne.Container).Objects[1].(*fyne.Container)
	if buttons.Objects[0].(*widget.Button).Disabled() {
		t.Fatal("B's check action stayed disabled after A was canceled")
	}
}

func TestProbeGenerationGuardsReselectionOfSameReceiver(t *testing.T) {
	p := newTestPanel(t)
	a, _ := receiver.Manual("A room", "192.168.1.40", receiver.AirPlay)
	b, _ := receiver.Manual("B room", "192.168.1.41", receiver.AirPlay)
	p.inventory.Observe(a, time.Now())
	p.inventory.Observe(b, time.Now())
	p.render()
	p.list.Select(0)
	previous := p.probeID
	p.list.Select(1)
	p.list.Select(0)
	p.probing, p.probeText = true, "New A check"
	p.completeProbe(previous, a.ID, a.Endpoint(), diagnostics.Result{Summary: "Old A result"}, nil)
	if !p.probing || p.probeText != "New A check" {
		t.Fatal("an old request completed a newer check for the same receiver")
	}
	p.completeProbe(p.probeID, a.ID, a.Endpoint(), diagnostics.Result{Summary: "Current A result", Detail: "No sharing started"}, nil)
	if p.probing || p.probeText != "Current A result. No sharing started" {
		t.Fatal("the current diagnostic result was not applied")
	}
}

func TestSavingManualAliasSelectsDiscoveredReceiver(t *testing.T) {
	p := newTestPanel(t)
	discovered, _ := receiver.Manual("Living room", "192.168.1.40", receiver.AirPlay)
	discovered.ID = "airplay:aabbccddeeff"
	p.inventory.Observe(discovered, time.Now())
	manual, _ := receiver.Manual("My TV", "192.168.1.40", receiver.AirPlay)
	p.saveDevice(manual)
	if len(p.devices) != 1 || p.selectedID != discovered.ID || !p.devices[0].Saved {
		t.Fatal("saving a manual alias duplicated or lost the discovered receiver selection")
	}
	p.selectedID = manual.ID
	p.probing, p.probeText = true, "Alias check"
	previous := p.probeID
	p.render()
	if p.selectedID != discovered.ID || p.probing || p.probeID <= previous || p.probeText == "Alias check" {
		t.Fatal("render did not resolve the alias and invalidate its stale probe")
	}
}

func TestProbeFollowsEndpointAsWellAsHardwareIdentity(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprint(pending), func(t *testing.T) {
			p := newTestPanel(t)
			d, _ := receiver.Manual("TV", "192.168.1.40", receiver.AirPlay)
			d.ID = "airplay:aabbccddeeff"
			p.inventory.Observe(d, time.Now())
			p.render()
			p.list.Select(0)
			probeCtx, cancel := context.WithCancel(p.ctx)
			defer cancel()
			p.probeCancel, p.probing, p.probeText = cancel, pending, "Old endpoint result"
			requestID := p.probeID
			moved := d
			moved.Host = "192.168.1.41"
			p.inventory.Observe(moved, time.Now())
			p.render()
			if p.selectedID != d.ID || p.probeID <= requestID || p.probing || p.probeText != probeIdleText || probeCtx.Err() == nil {
				t.Fatal("moving the same hardware receiver retained its old diagnostic")
			}
			p.completeProbe(p.probeID, d.ID, d.Endpoint(), diagnostics.Result{Summary: "Stale address"}, nil)
			if p.probeText != probeIdleText {
				t.Fatal("a matching generation accepted the wrong endpoint")
			}
			p.completeProbe(p.probeID, moved.ID, moved.Endpoint(), diagnostics.Result{Summary: "Current address"}, nil)
			if p.probeText != "Current address. " {
				t.Fatal("the current endpoint result was rejected")
			}
		})
	}
}
