package casting

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

const maxProtocolLine = 64 * 1024
const gdiCaptureNotice = "Using GDI compatibility capture instead of DXGI"

type startCommand struct {
	Command string `json:"command"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Quality string `json:"quality"`
	FPS     int    `json:"fps"`
	Audio   bool   `json:"audio"`
	Cursor  bool   `json:"cursor"`
}

type protocolResult struct {
	event Event
	err   error
}

func readProtocol(input io.Reader, output chan<- protocolResult, done <-chan struct{}) {
	reader := bufio.NewReaderSize(input, maxProtocolLine+2)
	for {
		line, err := reader.ReadSlice('\n')
		if err == nil {
			line = bytes.TrimSuffix(line[:len(line)-1], []byte{'\r'})
			object := bytes.TrimSpace(line)
			if len(line) > maxProtocolLine || len(object) < 2 || object[0] != '{' || object[len(object)-1] != '}' || !utf8.Valid(line) || !json.Valid(line) {
				err = errors.New("invalid casting engine response")
			}
		} else if err != io.EOF || len(line) != 0 {
			err = errors.New("incomplete or oversized casting engine response")
		}
		result := protocolResult{err: err}
		if err == nil {
			err = json.Unmarshal(line, &result.event)
			if err != nil {
				result.err = errors.New("invalid casting engine event")
			}
		}
		select {
		case output <- result:
		case <-done:
			return
		}
		if result.err != nil {
			return
		}
	}
}

func writeProtocol(output io.Writer, value any) error {
	line, err := json.Marshal(value)
	if err != nil || len(line) > maxProtocolLine {
		return errors.New("invalid casting engine command")
	}
	line = append(line, '\n')
	for len(line) > 0 {
		n, err := output.Write(line)
		if err != nil {
			return errors.New("the casting engine input closed")
		}
		if n <= 0 || n > len(line) {
			return io.ErrShortWrite
		}
		line = line[n:]
	}
	return nil
}

func (c *Controller) accept(s *session, event Event) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active != s || c.generation != s.generation || s.terminal {
		return false
	}
	if event.Type == "stats" {
		if !s.stopping && c.current.State == Streaming {
			c.current.Type = "stats"
			if event.Frames > c.current.Frames {
				c.current.Frames = event.Frames
			}
			if event.Bytes > c.current.Bytes {
				c.current.Bytes = event.Bytes
			}
			c.notifyLocked()
		}
		return false
	}
	if event.Type == "credential" {
		if s.stopping || c.current.State == Streaming {
			return false
		}
		if event.CredentialKind == "pin_or_password" {
			event.CredentialKind = "password"
		}
		if (event.State != "" && event.State != Pairing) || (event.CredentialKind != "pin" && event.CredentialKind != "password") {
			return false
		}
		event.State = Pairing
		s.credentialVersion++
		s.credentialKind, s.credentialPending = event.CredentialKind, true
	} else if event.Type == "state" {
		switch event.State {
		case Idle, Connecting, Pairing, Starting, Streaming, Stopping, Failed:
		default:
			return false
		}
		if s.stopping && event.State != Idle && event.State != Failed {
			return false
		}
		if c.current.State == Streaming && (event.State == Connecting || event.State == Pairing || event.State == Starting) {
			return false
		}
		s.credentialKind, s.credentialPending = "", false
		event.CredentialKind = ""
	} else {
		return false
	}
	current := c.current
	current.Type, current.State = event.Type, event.State
	if strings.Contains(event.Message, "Pairing is session-only; no secrets are saved to disk") || strings.Contains(event.Message, "Session-only pairing; no system keyring is available") {
		s.sessionOnly = true
	}
	for _, warning := range []string{
		"System audio is unavailable. Sharing video only",
		"Receiver did not enable system audio. Sharing video only",
		"Receiver did not enable the prepared audio format. Sharing video only",
		"System audio stopped. Sharing video only",
		"System audio is unavailable on this platform",
	} {
		if strings.Contains(event.Message, warning) {
			s.audioWarning = warning
		}
	}
	if strings.Contains(event.Message, gdiCaptureNotice) {
		s.captureWarning = gdiCaptureNotice
	}
	current.Message = safeMessage(event)
	if s.sessionOnly && !strings.Contains(current.Message, "session-only") {
		current.Message += ". Pairing is session-only; no secrets are saved to disk"
	}
	if s.audioWarning != "" && event.Message != s.audioWarning {
		current.Message += ". " + s.audioWarning
	}
	if s.captureWarning != "" && event.Message != s.captureWarning {
		current.Message += ". " + s.captureWarning
	}
	current.CredentialKind = event.CredentialKind
	if event.Width > 0 && event.Width <= 16384 && event.Height > 0 && event.Height <= 16384 {
		current.Width, current.Height = event.Width, event.Height
	}
	switch event.Codec {
	case "h264", "h.264", "avc":
		current.Codec = "h264"
	case "h265", "h.265", "hevc":
		current.Codec = "hevc"
	}
	c.current = current
	c.notifyLocked()
	if event.State == Idle || event.State == Failed {
		s.terminal = true
		c.stopLocked(s)
		return true
	}
	if event.State == Stopping {
		c.stopLocked(s)
	}
	return false
}

func safeMessage(event Event) string {
	// Only fixed messages may cross the child boundary. Redaction cannot reliably
	// identify an arbitrary password or private key inside upstream error text.
	switch event.Message {
	case gdiCaptureNotice:
		return gdiCaptureNotice
	case "Receiver refused pairing", "Receiver refused the password", "Receiver refused pairing after three attempts",
		"System audio is unavailable on this platform", "Session-only pairing; no system keyring is available",
		"System audio is unavailable. Sharing video only", "Receiver did not enable system audio. Sharing video only", "Receiver did not enable the prepared audio format. Sharing video only", "System audio stopped. Sharing video only",
		"OS credential store unavailable. Pairing is session-only; no secrets are saved to disk",
		"Enter the receiver's pairing PIN or configured password", "Receiver refused the credential. Enter the receiver's pairing PIN or configured password",
		"Enter the receiver's configured password", "Enter the receiver's configured password or displayed code",
		"Receiver refused the credential. Enter the PIN shown on the TV", "Receiver refused the credential. Enter the receiver's configured password",
		"Could not connect to the AirPlay receiver", "Could not read receiver capabilities or authenticate", "Could not establish encrypted media keys",
		"Receiver sent an invalid property list", "Receiver sent malformed AirPlay protocol data", "AirPlay session failed",
		"Screen capture could not start. Check screen permission and the installed media runtime", "Screen capture or video encoding could not start",
		"Receiver changed codec during setup", "Receiver selected an unsupported screen codec", "Receiver advertised invalid display dimensions",
		"Receiver does not advertise support for the requested frame rate", "Receiver could not start an encrypted mirror session",
		"Receiver completed setup without video capture", "Could not attach the encoded video stream", "No screen frames reached the receiver within 15 seconds",
		"Screen frame transmission stopped unexpectedly", "Malformed encoded video data", "Malformed audio data":
		return event.Message
	}
	if event.Type == "credential" {
		if event.CredentialKind == "password" {
			return "Enter the receiver's AirPlay password"
		}
		return "Enter the PIN shown on the TV"
	}
	switch event.State {
	case Connecting:
		return "Connecting to AirPlay receiver"
	case Pairing:
		return "Pairing with AirPlay receiver"
	case Starting:
		return "Starting screen sharing"
	case Streaming:
		return "Screen sharing is transmitting"
	case Stopping:
		return "Stopping screen sharing"
	case Idle:
		return "Screen sharing stopped"
	default:
		return "The casting engine could not complete screen sharing"
	}
}
