package ui

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"aircast/internal/casting"
	"aircast/internal/receiver"
)

const bestQualityLabel = "Best available"
const balancedQualityLabel = "Balanced"

type castSession struct {
	generation uint64
	device     receiver.Device
	event      casting.Event
	started    bool
	terminal   bool
	stopping   bool
}

func defaultCastOptions() casting.Options {
	return casting.Options{Quality: casting.BestQuality, FPS: 30, Audio: false, ShowCursor: true}
}

func captureSource(platform string) string {
	switch platform {
	case "windows":
		return "Primary screen"
	case "linux":
		return "Desktop"
	default:
		return "Screen"
	}
}

func (s *castSession) begin(generation uint64, device receiver.Device) {
	*s = castSession{
		generation: generation,
		device:     device,
		started:    true,
		event:      casting.Event{State: casting.Connecting, DeviceID: device.ID, DeviceName: device.Name, Message: "Connecting to " + device.Name + "..."},
	}
}

func (s *castSession) busy() bool {
	if !s.started || s.terminal {
		return false
	}
	switch s.event.State {
	case casting.Connecting, casting.Pairing, casting.Starting, casting.Streaming, casting.Stopping:
		return true
	}
	return false
}

func (s *castSession) stop() {
	s.stopping = true
	s.event.State = casting.Stopping
	s.event.Message = "Stopping sharing with " + s.device.Name + "..."
}

func (s *castSession) apply(generation uint64, event casting.Event) bool {
	if !s.started || s.terminal || generation != s.generation || event.DeviceID != "" && event.DeviceID != s.device.ID {
		return false
	}
	if event.Type == "stats" {
		if s.stopping {
			return false
		}
		s.mergeMedia(event)
		if event.Message != "" {
			s.event.Message = event.Message
		}
		return true
	}
	if event.Type == "credential" {
		if s.stopping || s.event.State == casting.Streaming {
			return false
		}
		event.State = casting.Pairing
	} else if event.Type != "state" {
		return false
	}
	switch event.State {
	case casting.Idle, casting.Connecting, casting.Pairing, casting.Starting, casting.Streaming, casting.Stopping, casting.Failed:
	default:
		return false
	}
	if s.stopping && event.State != casting.Idle && event.State != casting.Failed {
		return false
	}
	if s.event.State == casting.Streaming && (event.State == casting.Connecting || event.State == casting.Pairing || event.State == casting.Starting) {
		return false
	}
	s.event.Type, s.event.State = event.Type, event.State
	s.event.CredentialKind = event.CredentialKind
	s.event.Message = event.Message
	s.mergeMedia(event)
	s.stopping = event.State == casting.Stopping
	s.terminal = event.State == casting.Idle || event.State == casting.Failed
	return true
}

func (s *castSession) mergeMedia(event casting.Event) {
	if event.Width > 0 && event.Height > 0 {
		s.event.Width, s.event.Height = event.Width, event.Height
	}
	if event.Codec != "" {
		s.event.Codec = event.Codec
	}
	if event.Frames > s.event.Frames {
		s.event.Frames = event.Frames
	}
	if event.Bytes > s.event.Bytes {
		s.event.Bytes = event.Bytes
	}
}

func mediaSummary(event casting.Event) string {
	parts := make([]string, 0, 4)
	if event.Width > 0 && event.Height > 0 {
		parts = append(parts, fmt.Sprintf("%d × %d negotiated", event.Width, event.Height))
	}
	if event.Codec != "" {
		codec := strings.ToUpper(event.Codec)
		switch strings.ToLower(event.Codec) {
		case "h264", "h.264", "avc":
			codec = "H.264"
		case "h265", "h.265", "hevc":
			codec = "HEVC"
		}
		parts = append(parts, codec)
	}
	if event.Frames > 0 {
		parts = append(parts, fmt.Sprintf("%d frames sent", event.Frames))
	}
	if event.Bytes > 0 {
		parts = append(parts, fmt.Sprintf("%.1f MiB sent", float64(event.Bytes)/(1024*1024)))
	}
	return strings.Join(parts, " / ")
}

func validateCredential(kind, value string) error {
	if kind == "pin" {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 16 {
			return errors.New("Enter the numeric PIN shown on the TV")
		}
		for _, r := range value {
			if r < '0' || r > '9' {
				return errors.New("Enter the numeric PIN shown on the TV")
			}
		}
		return nil
	}
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) {
		return errors.New("Enter the receiver's password, up to 1024 bytes")
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return errors.New("The password must not contain control characters")
		}
	}
	return nil
}
