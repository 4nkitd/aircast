package casting

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReadProtocolLimits(t *testing.T) {
	base := `{"event":"state","state":"connecting","message":"` + `"}`
	for _, tc := range []struct {
		name  string
		input string
		valid bool
	}{
		{"normal", `{"event":"state","state":"connecting"}` + "\n", true},
		{"crlf", `{"event":"state","state":"connecting"}` + "\r\n", true},
		{"exact limit", `{"event":"state","state":"connecting","message":"` + strings.Repeat("a", maxProtocolLine-len(base)) + `"}` + "\n", true},
		{"over limit", `{"event":"state","state":"connecting","message":"` + strings.Repeat("a", maxProtocolLine-len(base)+1) + `"}` + "\n", false},
		{"unterminated", `{"event":"state","state":"connecting"}`, false},
		{"invalid json", "{bad}\n", false},
		{"blank", "\n", false},
		{"array", "[]\n", false},
		{"null", "null\n", false},
		{"invalid UTF8", "{\"message\":\"\xff\"}\n", false},
		{"oversized garbage", strings.Repeat("a", maxProtocolLine*2), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results := make(chan protocolResult, 2)
			readProtocol(strings.NewReader(tc.input), results, make(chan struct{}))
			result := <-results
			if (result.err == nil) != tc.valid {
				t.Fatalf("valid=%v, err=%v", tc.valid, result.err)
			}
			if tc.valid {
				if result.event.State != Connecting {
					t.Fatalf("unexpected state %q", result.event.State)
				}
				if !errors.Is((<-results).err, io.EOF) {
					t.Fatal("expected EOF after final event")
				}
			}
		})
	}
}

type shortWriter struct{ bytes.Buffer }

func (w *shortWriter) Write(value []byte) (int, error) {
	if len(value) > 3 {
		value = value[:3]
	}
	return w.Buffer.Write(value)
}

type emptyWriter struct{}

func (emptyWriter) Write([]byte) (int, error) { return 0, nil }

func TestWriteProtocol(t *testing.T) {
	writer := &shortWriter{}
	command := map[string]string{"command": "credential", "value": "quotes\"\\\nUnicode電"}
	if err := writeProtocol(writer, command); err != nil {
		t.Fatal(err)
	}
	if bytes.Count(writer.Bytes(), []byte{'\n'}) != 1 {
		t.Fatal("command escaped its JSON line")
	}
	var decoded map[string]string
	if err := json.Unmarshal(bytes.TrimSpace(writer.Bytes()), &decoded); err != nil || decoded["value"] != command["value"] {
		t.Fatal("credential did not round trip")
	}
	if !errors.Is(writeProtocol(emptyWriter{}, command), io.ErrShortWrite) {
		t.Fatal("accepted a zero-length write")
	}
	if err := writeProtocol(io.Discard, strings.Repeat("a", maxProtocolLine)); err == nil {
		t.Fatal("accepted an oversized command")
	}
}

func TestValidCredential(t *testing.T) {
	for _, tc := range []struct {
		value string
		kind  string
		valid bool
	}{
		{"1234", "pin", true},
		{"123456", "pin", true},
		{"", "pin", false},
		{"12 34", "pin", false},
		{"１２３４", "pin", false},
		{strings.Repeat("1", 17), "pin", false},
		{"pa ss\"\\電", "password", true},
		{"password\n", "password", false},
		{"password\u202e", "password", false},
		{string([]byte{0xff}), "password", false},
		{strings.Repeat("p", 1024), "password", true},
		{strings.Repeat("p", 1025), "password", false},
		{"password", "unknown", false},
	} {
		if got := validCredential(tc.value, tc.kind); got != tc.valid {
			t.Fatalf("kind=%q bytes=%d valid UTF8=%v: got %v, want %v", tc.kind, len(tc.value), utf8.ValidString(tc.value), got, tc.valid)
		}
	}
}

func TestEventBoundary(t *testing.T) {
	c := New(nil)
	t.Cleanup(func() { c.Close() })
	s := &session{generation: 1, stop: make(chan struct{}), done: make(chan struct{})}
	c.active, c.generation = s, 1
	c.current = Event{Type: "state", State: Connecting, DeviceID: "trusted-id", DeviceName: "Trusted TV"}
	c.accept(s, Event{Type: "stats", State: Streaming, Frames: 999})
	if c.Snapshot().State != Connecting || c.Snapshot().Frames != 0 {
		t.Fatal("stats claimed streaming before a media event")
	}
	c.accept(s, Event{Type: "state", State: Starting, Width: 1920, Height: 1080, Codec: "h264"})
	c.accept(s, Event{Type: "state", State: Streaming, DeviceID: "forged", DeviceName: "Forged", Message: "private key and PIN 7654", Width: -1, Height: 1 << 30, Codec: "secret codec"})
	c.accept(s, Event{Type: "stats", Frames: 100, Bytes: 5000})
	c.accept(s, Event{Type: "stats", Frames: 1, Bytes: 2, DeviceID: "forged", Width: 1 << 30, Codec: "secret"})
	c.accept(s, Event{Type: "state", State: Connecting})
	c.accept(s, Event{Type: "credential", State: Pairing, CredentialKind: "pin"})
	got := c.Snapshot()
	if got.State != Streaming || got.Frames != 100 || got.Bytes != 5000 || got.Width != 1920 || got.Height != 1080 || got.Codec != "h264" || got.DeviceID != "trusted-id" || got.DeviceName != "Trusted TV" {
		t.Fatalf("unsafe event merge: %#v", got)
	}
	if strings.Contains(got.Message, "7654") || strings.Contains(got.Message, "private") {
		t.Fatal("child message escaped the fixed-message boundary")
	}
	c.accept(s, Event{Type: "credential", State: Pairing, CredentialKind: strings.Repeat("pin", 1000)})
	if c.Snapshot().CredentialKind != "" {
		t.Fatal("accepted an unsupported credential kind")
	}
	c.Stop()
	c.accept(s, Event{Type: "state", State: Streaming})
	if c.Snapshot().State != Stopping {
		t.Fatal("late event reversed stop")
	}
	c.finish(s)
}

func TestSessionGenerationRejectsOldEvents(t *testing.T) {
	c := New(nil)
	t.Cleanup(func() { c.Close() })
	old := &session{generation: 1}
	current := &session{generation: 2, stop: make(chan struct{}), done: make(chan struct{})}
	c.active, c.generation = current, 2
	c.current = Event{Type: "state", State: Connecting, DeviceID: "second"}
	c.accept(old, Event{Type: "state", State: Streaming})
	c.fail(old, "stale failure")
	if got := c.Snapshot(); got.State != Connecting || got.DeviceID != "second" {
		t.Fatalf("old session altered current event: %#v", got)
	}
	c.Stop()
	c.finish(current)
}

func TestPairingFallbackAndWarningsPersist(t *testing.T) {
	c := New(nil)
	t.Cleanup(func() { c.Close() })
	s := &session{generation: 1, stop: make(chan struct{}), done: make(chan struct{}), credentials: make(chan credential, 1)}
	c.active, c.generation = s, 1
	c.current = Event{Type: "state", State: Connecting}
	c.accept(s, Event{Type: "state", State: Connecting, Message: "OS credential store unavailable. Pairing is session-only; no secrets are saved to disk"})
	c.accept(s, Event{Type: "credential", State: Pairing, CredentialKind: "pin_or_password", Message: "Enter the receiver's pairing PIN or configured password"})
	if got := c.Snapshot(); got.CredentialKind != "password" || !strings.Contains(got.Message, "PIN") || !strings.Contains(got.Message, "session-only") {
		t.Fatalf("fallback credential request was lost: %#v", got)
	}
	if err := c.SubmitCredential("test password 電"); err != nil {
		t.Fatal(err)
	}
	c.accept(s, Event{Type: "state", State: Starting, Message: "System audio is unavailable. Sharing video only"})
	if c.SubmitCredential("test password 電") == nil {
		t.Fatal("a stale credential request survived the state change")
	}
	c.accept(s, Event{Type: "state", State: Streaming})
	c.accept(s, Event{Type: "stats", Frames: 100, Bytes: 1000})
	got := c.Snapshot()
	if !strings.Contains(got.Message, "session-only") || !strings.Contains(got.Message, "Sharing video only") || len(got.Message) > 512 {
		t.Fatalf("critical warnings were lost: %#v", got)
	}
	c.Stop()
	c.finish(s)
}

func TestGDICompatibilityNoticeSurvivesStreamingAndStats(t *testing.T) {
	c := New(nil)
	t.Cleanup(func() { c.Close() })
	s := &session{generation: 1, stop: make(chan struct{}), done: make(chan struct{})}
	c.active, c.generation = s, 1
	c.current = Event{Type: "state", State: Connecting}
	c.accept(s, Event{Type: "state", State: Starting, Message: gdiCaptureNotice})
	c.accept(s, Event{Type: "state", State: Streaming})
	c.accept(s, Event{Type: "stats", Frames: 60, Bytes: 100000})
	if got := c.Snapshot(); got.State != Streaming || !strings.Contains(got.Message, gdiCaptureNotice) || got.Frames != 60 {
		t.Fatalf("compatibility notice was hidden: %#v", got)
	}
	c.Stop()
	c.finish(s)
}

func TestCombinedWarningsRemainVisibleWithoutForwardingChildSecrets(t *testing.T) {
	c := New(nil)
	t.Cleanup(func() { c.Close() })
	s := &session{generation: 1, stop: make(chan struct{}), done: make(chan struct{})}
	c.active, c.generation = s, 1
	c.current = Event{Type: "state", State: Connecting}
	const audioWarning = "Receiver did not enable the prepared audio format. Sharing video only"
	c.accept(s, Event{Type: "state", State: Starting, Message: "OS credential store unavailable. Pairing is session-only; no secrets are saved to disk. " + audioWarning + ". " + gdiCaptureNotice + ". private password 7654"})
	c.accept(s, Event{Type: "state", State: Streaming})
	c.accept(s, Event{Type: "stats", Frames: 60, Bytes: 100000})
	got := c.Snapshot()
	for _, message := range []string{"session-only", audioWarning, gdiCaptureNotice} {
		if !strings.Contains(got.Message, message) {
			t.Fatalf("combined warning was hidden: %#v", got)
		}
	}
	if strings.Contains(got.Message, "private") || strings.Contains(got.Message, "7654") {
		t.Fatal("arbitrary child text crossed the fixed-message boundary")
	}
	c.Stop()
	c.finish(s)
}
