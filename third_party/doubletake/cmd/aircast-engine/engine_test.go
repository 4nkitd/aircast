package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"doubletake/internal/airplay"
)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	airplay.SetDebugMode(false)
	os.Exit(m.Run())
}

type memoryKeyring struct {
	mu      sync.Mutex
	entries map[string]airplay.SavedCredentials
}

func (m *memoryKeyring) Lookup(id string) (*airplay.SavedCredentials, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, found := m.entries[id]
	if !found {
		return nil, nil
	}
	return &entry, nil
}

func (m *memoryKeyring) Save(id string, saved *airplay.SavedCredentials) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		m.entries = make(map[string]airplay.SavedCredentials)
	}
	m.entries[id] = *saved
	return nil
}

type testEngine struct {
	t        *testing.T
	input    *io.PipeWriter
	events   chan event
	done     chan int
	raw      bytes.Buffer
	seen     []event
	finished bool
	cfg      chan airplay.CaptureConfig
}

func newTestEngine(t *testing.T, backend airplay.CredentialBackend) *testEngine {
	t.Helper()
	if _, err := exec.LookPath("gst-launch-1.0"); err != nil {
		t.Skip("GStreamer is required for hardware-free encoded-media integration tests")
	}
	input, commands := io.Pipe()
	output, events := io.Pipe()
	h := &testEngine{t: t, input: commands, events: make(chan event, 128), done: make(chan int, 1), cfg: make(chan airplay.CaptureConfig, 1)}
	go func() {
		defer output.Close()
		decoder := json.NewDecoder(output)
		for {
			var value event
			if err := decoder.Decode(&value); err != nil {
				close(h.events)
				return
			}
			h.events <- value
		}
	}()
	go func() {
		code := serve(context.Background(), input, io.MultiWriter(events, &h.raw), engineDependencies{
			prepare: func(ctx context.Context, cfg airplay.CaptureConfig) (*airplay.CapturePreparation, error) {
				h.cfg <- cfg
				return airplay.PrepareTestCapture(ctx, cfg)
			},
			keyring: func() (airplay.CredentialBackend, error) { return backend, nil },
		})
		_ = events.Close()
		h.done <- code
	}()
	t.Cleanup(func() {
		if !h.finished {
			_ = h.input.Close()
			select {
			case <-h.done:
			case <-time.After(8 * time.Second):
				t.Error("engine cleanup timed out")
			}
		}
	})
	return h
}

func (h *testEngine) send(value command) {
	h.t.Helper()
	if err := json.NewEncoder(h.input).Encode(value); err != nil {
		h.t.Fatal(err)
	}
}

func (h *testEngine) await(predicate func(event) bool) event {
	h.t.Helper()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case value, ok := <-h.events:
			if !ok {
				h.t.Fatalf("engine output ended; events=%+v", h.seen)
			}
			h.seen = append(h.seen, value)
			if predicate(value) {
				return value
			}
			if value.State == "failed" {
				h.t.Fatalf("unexpected engine failure: %s", value.Message)
			}
		case <-timer.C:
			h.t.Fatalf("engine event timed out; events=%+v", h.seen)
		}
	}
}

func (h *testEngine) waitExit(want int) {
	h.t.Helper()
	select {
	case code := <-h.done:
		h.finished = true
		if code != want {
			h.t.Fatalf("engine exit = %d, want %d", code, want)
		}
	case <-time.After(5 * time.Second):
		h.t.Fatal("engine did not release resources within five seconds")
	}
}

func (h *testEngine) stop(eof bool) {
	h.t.Helper()
	if eof {
		_ = h.input.Close()
	} else {
		h.send(command{Command: "stop"})
	}
	h.await(func(value event) bool { return value.State == "idle" })
	h.waitExit(0)
}

func testReceiver(t *testing.T, profile airplay.ReceiverProfile, auth airplay.ReceiverAuthMode, password string) *airplay.ReceiverServer {
	t.Helper()
	return testConfiguredReceiver(t, airplay.ReceiverConfig{
		ListenAddress: "127.0.0.1:0", Profile: profile, Auth: auth, Code: password,
		DisplayWidth: 640, DisplayHeight: 360, Logger: log.New(io.Discard, "", 0),
	})
}

func testConfiguredReceiver(t *testing.T, config airplay.ReceiverConfig) *airplay.ReceiverServer {
	t.Helper()
	server, err := airplay.NewReceiverServer(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
	})
	return server
}

func startReceiver(server *airplay.ReceiverServer) command {
	address := server.Addr().(*net.TCPAddr)
	return command{Command: "start", Host: address.IP.String(), Port: address.Port}
}

func TestEncryptedScreenTransmissionAndCleanup(t *testing.T) {
	for _, profile := range []airplay.ReceiverProfile{airplay.ReceiverProfileModern, airplay.ReceiverProfileRoku, airplay.ReceiverProfileUxPlay} {
		t.Run(string(profile), func(t *testing.T) {
			server := testReceiver(t, profile, airplay.ReceiverAuthNone, "")
			h := newTestEngine(t, nil)
			h.send(startReceiver(server))
			stream := h.await(func(value event) bool { return value.State == "streaming" })
			if stream.Codec != "h264" || stream.Width != 640 || stream.Height != 360 {
				t.Fatalf("wrong negotiated stream: %+v", stream)
			}
			if !strings.Contains(stream.Message, "session-only") {
				t.Fatal("streaming status hid the session-only keyring warning")
			}
			stats := h.await(func(value event) bool { return value.Event == "stats" && value.Frames >= 3 })
			if stats.Bytes == 0 {
				t.Fatal("no encoded video bytes transmitted")
			}
			cfg := <-h.cfg
			if cfg.Quality != "quality" || cfg.FPS != 30 || !cfg.ShowCursor {
				t.Fatalf("wrong capture defaults: %+v", cfg)
			}
			h.stop(false)
			received := server.Stats()
			if received.SetupRequests < 3 || received.VideoPackets < stats.Frames || received.TeardownRequests == 0 || received.ParameterRequests != 0 {
				t.Fatalf("receiver did not observe video-only setup, frames, and teardown: %+v", received)
			}
			if (profile == airplay.ReceiverProfileUxPlay || profile == airplay.ReceiverProfileModern) && (received.VideoDecrypted < stats.Frames || received.VideoCryptoErrors != 0) {
				t.Fatalf("encrypted pictures were not validated: %+v", received)
			}
			t.Logf("profile=%s sent_frames=%d encoded_bytes=%d receiver_packets=%d decrypted_frames=%d teardown=%d", profile, stats.Frames, stats.Bytes, received.VideoPackets, received.VideoDecrypted, received.TeardownRequests)
			previous := uint64(0)
			for _, value := range h.seen {
				if value.Event == "stats" {
					if value.Frames < previous {
						t.Fatal("transmitted counters regressed")
					}
					previous = value.Frames
				}
			}
			if !strings.Contains(h.raw.String(), "session-only") {
				t.Fatal("keyring fallback was silent")
			}
		})
	}
}

func TestPINRetrySavedPairingAndEOF(t *testing.T) {
	server := testReceiver(t, airplay.ReceiverProfileModern, airplay.ReceiverAuthPIN, "4186")
	keyring := &memoryKeyring{}
	h := newTestEngine(t, keyring)
	h.send(startReceiver(server))
	h.await(func(value event) bool { return value.Event == "credential" && value.Kind == "pin" })
	if stats := server.Stats(); stats.VideoPackets != 0 {
		t.Fatalf("media preceded pairing: %+v", stats)
	}
	h.send(command{Command: "credential", Value: "secret-wrong-pin"})
	h.await(func(value event) bool {
		return value.Event == "credential" && strings.Contains(value.Message, "refused")
	})
	h.send(command{Command: "credential", Value: "4186"})
	h.await(func(value event) bool { return value.State == "streaming" })
	h.stop(true)
	if strings.Contains(h.raw.String(), "secret-wrong-pin") || strings.Contains(h.raw.String(), "4186") {
		t.Fatal("pairing credential appeared in protocol output")
	}
	paired := server.Stats()
	saved := keyring.entries
	if len(saved) != 1 {
		t.Fatal("pairing identity was not saved")
	}
	for _, identity := range saved {
		if len(identity.ReceiverPublicKey) != 32 || identity.ReceiverPairingID == "" {
			t.Fatal("saved pairing omitted the authenticated receiver identity")
		}
	}
	h = newTestEngine(t, keyring)
	h.send(startReceiver(server))
	h.await(func(value event) bool { return value.State == "streaming" })
	h.stop(false)
	verified := server.Stats()
	if verified.PairSetup != paired.PairSetup || verified.PairVerify <= paired.PairVerify {
		t.Fatalf("saved identity was not reused: paired=%+v verified=%+v", paired, verified)
	}
	for _, value := range h.seen {
		if value.Event == "credential" {
			t.Fatal("saved pairing requested a PIN again")
		}
	}
}

func TestWrongPINFailsAfterThreeAttempts(t *testing.T) {
	server := testReceiver(t, airplay.ReceiverProfileModern, airplay.ReceiverAuthPIN, "4186")
	h := newTestEngine(t, nil)
	h.send(startReceiver(server))
	for attempt := 0; attempt < maxCredentialAttempts; attempt++ {
		h.await(func(value event) bool { return value.Event == "credential" })
		h.send(command{Command: "credential", Value: "not-the-secret-pin"})
	}
	h.await(func(value event) bool { return value.State == "failed" })
	h.waitExit(1)
	if strings.Contains(h.raw.String(), "not-the-secret-pin") || server.Stats().VideoPackets != 0 {
		t.Fatal("failed PIN leaked or started media")
	}
}

func TestSavedReceiverIdentityMismatchFailsClosed(t *testing.T) {
	server := testReceiver(t, airplay.ReceiverProfileModern, airplay.ReceiverAuthPIN, "4186")
	keyring := &memoryKeyring{}
	h := newTestEngine(t, keyring)
	h.send(startReceiver(server))
	h.await(func(value event) bool { return value.Event == "credential" })
	h.send(command{Command: "credential", Value: "4186"})
	h.await(func(value event) bool { return value.State == "streaming" })
	h.stop(false)
	keyring.mu.Lock()
	for id, entry := range keyring.entries {
		entry.ReceiverPublicKey = append([]byte(nil), entry.ReceiverPublicKey...)
		entry.ReceiverPublicKey[0] ^= 1
		keyring.entries[id] = entry
	}
	keyring.mu.Unlock()
	before := server.Stats()
	h = newTestEngine(t, keyring)
	h.send(startReceiver(server))
	h.await(func(value event) bool {
		return value.State == "failed" && strings.Contains(value.Message, "identity could not be authenticated")
	})
	h.waitExit(1)
	if server.Stats().SetupRequests != before.SetupRequests {
		t.Fatal("untrusted receiver reached media setup")
	}
}

func TestPasswordAndDigestAuthentication(t *testing.T) {
	for _, test := range []struct {
		profile airplay.ReceiverProfile
		auth    airplay.ReceiverAuthMode
	}{
		{airplay.ReceiverProfileModern, airplay.ReceiverAuthDigest},
		{airplay.ReceiverProfileModern, airplay.ReceiverAuthCombined},
		{airplay.ReceiverProfileRoku, airplay.ReceiverAuthDigest},
		{airplay.ReceiverProfileRoku, airplay.ReceiverAuthCombined},
	} {
		t.Run(string(test.profile)+"/"+string(test.auth), func(t *testing.T) {
			const password = "private receiver password with spaces"
			server := testReceiver(t, test.profile, test.auth, password)
			h := newTestEngine(t, nil)
			h.send(startReceiver(server))
			h.await(func(value event) bool { return value.Event == "credential" && value.Kind == "password" })
			h.send(command{Command: "credential", Value: password})
			h.await(func(value event) bool { return value.State == "streaming" })
			h.stop(false)
			if strings.Contains(h.raw.String(), password) || server.Stats().DigestChallenges == 0 {
				t.Fatal("password leaked or Digest flow was not exercised")
			}
		})
	}
}

func TestLateDigestChallengeReconnectsAfterAcceptedControlSetup(t *testing.T) {
	const password = "private late digest credential"
	server := testConfiguredReceiver(t, airplay.ReceiverConfig{
		ListenAddress: "127.0.0.1:0", Profile: airplay.ReceiverProfileModern,
		Auth: airplay.ReceiverAuthDigest, Code: password,
		HidePasswordStatus: true, DigestAfterControlSetup: true,
		DisplayWidth: 640, DisplayHeight: 360, Logger: log.New(io.Discard, "", 0),
	})
	h := newTestEngine(t, nil)
	h.send(startReceiver(server))
	h.await(func(value event) bool { return value.Event == "credential" && value.Kind == "password" })
	select {
	case <-h.cfg:
	default:
		t.Fatal("receiver challenged before capture preparation")
	}
	h.send(command{Command: "credential", Value: password})
	h.await(func(value event) bool { return value.State == "streaming" })
	h.stop(false)
	stats := server.Stats()
	if stats.Connections < 2 || stats.DigestChallenges < 2 || stats.VideoDecrypted == 0 || stats.VideoCryptoErrors != 0 {
		t.Fatalf("late Digest retry did not reconnect and restore authenticated media: %+v", stats)
	}
	if strings.Contains(h.raw.String(), password) {
		t.Fatal("late Digest password leaked to engine events")
	}
}

func TestEOFCancelsPendingPIN(t *testing.T) {
	server := testReceiver(t, airplay.ReceiverProfileModern, airplay.ReceiverAuthPIN, "4186")
	h := newTestEngine(t, nil)
	h.send(startReceiver(server))
	h.await(func(value event) bool { return value.Event == "credential" })
	h.stop(true)
	if server.Stats().VideoPackets != 0 {
		t.Fatal("EOF during pairing started video")
	}
}

func TestOptionalAudioKeepsVideoAvailable(t *testing.T) {
	server := testReceiver(t, airplay.ReceiverProfileUxPlay, airplay.ReceiverAuthNone, "")
	h := newTestEngine(t, nil)
	start := startReceiver(server)
	start.Audio = true
	h.send(start)
	stream := h.await(func(value event) bool { return value.State == "streaming" })
	h.await(func(value event) bool { return value.Event == "stats" && value.Frames >= 3 })
	h.stop(false)
	if strings.Contains(stream.Message, "Sharing video only") && server.Stats().ParameterRequests != 0 {
		t.Fatal("unavailable audio changed receiver volume")
	}
}

func TestEOFInterruptsBlockedReceiverInfo(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			accepted <- connection
		}
	}()
	h := newTestEngine(t, nil)
	address := listener.Addr().(*net.TCPAddr)
	h.send(command{Command: "start", Host: "127.0.0.1", Port: address.Port})
	select {
	case connection := <-accepted:
		defer connection.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("engine did not connect")
	}
	h.stop(true)
}

func TestProtocolRejectsBadInputWithoutEcho(t *testing.T) {
	for _, input := range []string{
		`{"command":"credential","value":"private-secret"}` + "\n",
		`{"command":"start","host":"private-secret\r\nInjected: header"}` + "\n",
		`{"command":"start","host":"fe80::1%private-secret\r\nInjected:header"}` + "\n",
		`{"command":"start","host":"localhost","password":"private-secret"}` + "\n",
		`{"command":"private-secret"}` + "\n",
		strings.Repeat("private-secret", maxCommandBytes) + "\n",
	} {
		var output bytes.Buffer
		code := serve(context.Background(), io.NopCloser(strings.NewReader(input)), &output, engineDependencies{})
		if code != 1 || !strings.Contains(output.String(), `"state":"failed"`) || strings.Contains(output.String(), "private-secret") {
			t.Fatalf("unsafe invalid-input response: code=%d output=%s", code, output.String())
		}
	}
}

func TestGDICompatibilityNoticeStaysExact(t *testing.T) {
	var output bytes.Buffer
	writer := newEventWriter(&output, func() {})
	writer.warn("Pairing is session-only")
	_ = writer.state("starting", "Preparing capture")
	writer.notice(gdiCaptureNotice)
	decoder := json.NewDecoder(&output)
	var last event
	for decoder.More() {
		if err := decoder.Decode(&last); err != nil {
			t.Fatal(err)
		}
	}
	if last.Event != "state" || last.State != "starting" || last.Message != "Using GDI compatibility capture instead of DXGI" {
		t.Fatalf("GDI notice does not match the supervisor contract: %+v", last)
	}
}

func TestNominalCanvasBounds(t *testing.T) {
	for _, test := range []struct{ width, height, wantW, wantH int }{
		{3840, 2160, 1920, 1080}, {1920, 1080, 1920, 1080}, {1280, 720, 1280, 720}, {2560, 1600, 1728, 1080},
	} {
		width, height, ok := nominalCanvas(test.width, test.height)
		if !ok || width != test.wantW || height != test.wantH {
			t.Fatalf("canvas %dx%d = %dx%d %v", test.width, test.height, width, height, ok)
		}
	}
	for _, size := range [][2]int{{0, 1080}, {-1, 1080}, {1 << 60, 1080}, {8192, 8192}} {
		if _, _, ok := nominalCanvas(size[0], size[1]); ok {
			t.Fatalf("hostile dimensions accepted: %v", size)
		}
	}
}

func malformedReceiverPlist() []byte {
	data := append([]byte("bplist00"), 0x4f, 0x13, 255, 255, 255, 255, 255, 255, 255, 255)
	data = append(data, 8)
	trailer := make([]byte, 32)
	trailer[6], trailer[7] = 1, 1
	binary.BigEndian.PutUint64(trailer[8:16], 1)
	binary.BigEndian.PutUint64(trailer[24:32], 18)
	return append(data, trailer...)
}

func TestMalformedReceiverCannotPanicEngine(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		reader := bufio.NewReader(connection)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if line == "\r\n" {
				break
			}
		}
		data := malformedReceiverPlist()
		_, _ = fmt.Fprintf(connection, "RTSP/1.0 200 OK\r\nContent-Length: %d\r\n\r\n", len(data))
		_, _ = connection.Write(data)
	}()
	h := newTestEngine(t, nil)
	h.send(command{Command: "start", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port})
	h.await(func(value event) bool {
		return value.State == "failed" && strings.Contains(value.Message, "invalid property list")
	})
	h.waitExit(1)
}

func TestEngineProcessHelper(t *testing.T) {
	if os.Getenv("AIRCAST_ENGINE_PROCESS_TEST") != "1" {
		return
	}
	os.Exit(serve(context.Background(), os.Stdin, os.Stdout, engineDependencies{
		prepare: airplay.PrepareTestCapture,
		keyring: func() (airplay.CredentialBackend, error) { return nil, errors.New("unavailable") },
	}))
}

func TestProcessJSONLinesPINStreamAndStop(t *testing.T) {
	if _, err := exec.LookPath("gst-launch-1.0"); err != nil {
		t.Skip("GStreamer is required")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	testProcessPINStream(t, executable, "-test.run=^TestEngineProcessHelper$")
}

func TestProductionProcessX11PINStreamAndEOF(t *testing.T) {
	executable := os.Getenv("AIRCAST_ENGINE_BINARY")
	if executable == "" {
		t.Skip("set AIRCAST_ENGINE_BINARY to a built engine and run under an X11 display")
	}
	testProcessPINStream(t, executable)
}

func testProcessPINStream(t *testing.T, executable string, args ...string) {
	t.Helper()
	var server *airplay.ReceiverServer
	if len(args) == 0 {
		var err error
		server, err = airplay.NewReceiverServer(airplay.ReceiverConfig{
			ListenAddress: "127.0.0.1:0", Profile: airplay.ReceiverProfileModern, Auth: airplay.ReceiverAuthPIN, Code: "4186",
			Logger: log.New(io.Discard, "", 0), DisplayWidth: 1920, DisplayHeight: 1080,
		})
		if err != nil {
			t.Fatal(err)
		}
		go func() { _ = server.Serve(context.Background()) }()
		t.Cleanup(func() { _ = server.Close() })
	} else {
		server = testReceiver(t, airplay.ReceiverProfileModern, airplay.ReceiverAuthPIN, "4186")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, executable, args...)
	process.Env = append(os.Environ(), "AIRCAST_ENGINE_PROCESS_TEST=1")
	var stderr bytes.Buffer
	process.Stderr = &stderr
	stdin, err := process.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := process.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = process.Process.Kill() }()
	encoder, decoder := json.NewEncoder(stdin), json.NewDecoder(stdout)
	if err := encoder.Encode(startReceiver(server)); err != nil {
		t.Fatal(err)
	}
	sentStop, credentialPrompts := false, 0
	var transmitted event
	for {
		var value event
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(value.Message, "4186") {
			t.Fatal("PIN leaked to process output")
		}
		if value.State == "failed" {
			t.Fatalf("process failed: %s", value.Message)
		}
		if value.Event == "credential" {
			credentialPrompts++
			_ = encoder.Encode(command{Command: "credential", Value: "4186"})
		}
		if value.Event == "stats" {
			transmitted = value
		}
		if value.Event == "stats" && value.Frames >= 3 && !sentStop {
			if len(args) == 0 {
				_ = stdin.Close()
			} else {
				_ = encoder.Encode(command{Command: "stop"})
			}
			sentStop = true
		}
		if value.State == "idle" {
			break
		}
	}
	_ = stdin.Close()
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 || !sentStop || credentialPrompts != 1 || server.Stats().TeardownRequests == 0 {
		t.Fatalf("process did not cleanly pair/stream/stop: stderr bytes=%d PIN prompts=%d stats=%+v", stderr.Len(), credentialPrompts, server.Stats())
	}
	if len(args) == 0 && (server.Stats().VideoWidth != 1920 || server.Stats().VideoHeight != 1080) {
		t.Fatalf("production capture did not send the negotiated 1080p raster: %+v", server.Stats())
	}
	received := server.Stats()
	t.Logf("sent_frames=%d encoded_bytes=%d receiver_packets=%d raster=%dx%d teardown=%d stderr_bytes=%d", transmitted.Frames, transmitted.Bytes, received.VideoPackets, received.VideoWidth, received.VideoHeight, received.TeardownRequests, stderr.Len())
}
