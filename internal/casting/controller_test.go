package casting

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"aircast/internal/receiver"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("AIRCAST_TEST_ENGINE"); mode != "" {
		os.Exit(engineHelper(mode))
	}
	os.Exit(m.Run())
}

func testDevice(t *testing.T) receiver.Device {
	t.Helper()
	device, err := receiver.Manual("TV \"電\" \\", "127.0.0.1:7000", receiver.AirPlay)
	if err != nil {
		t.Fatal(err)
	}
	return device
}

func testOptions() Options {
	return Options{Quality: BestQuality, FPS: 30, ShowCursor: true}
}

func helperController(t *testing.T, mode string, callback func(Event)) *Controller {
	t.Helper()
	t.Setenv("AIRCAST_TEST_ENGINE", mode)
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := New(callback)
	c.resolve = func() (string, error) { return path, nil }
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c
}

func waitSnapshot(t *testing.T, c *Controller, predicate func(Event) bool) Event {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		event := c.Snapshot()
		if predicate(event) {
			return event
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out, last state=%q type=%q", event.State, event.Type)
		case <-tick.C:
		}
	}
}

func waitSession(t *testing.T, c *Controller) {
	t.Helper()
	c.mu.Lock()
	s := c.active
	c.mu.Unlock()
	if s == nil {
		return
	}
	select {
	case <-s.done:
	case <-time.After(8 * time.Second):
		t.Fatal("engine process was not reaped")
	}
}

func replaceEnv(env []string, key, value string) []string {
	result := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(item, key+"=") {
			result = append(result, item)
		}
	}
	return append(result, key+"="+value)
}

func readHelperPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, err := strconv.Atoi(string(data))
			if err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("helper did not write its child PID")
	return 0
}

func TestStartValidatesBeforeLaunch(t *testing.T) {
	c := New(nil)
	t.Cleanup(func() { c.Close() })
	launched := false
	c.resolve = func() (string, error) { launched = true; return "", nil }
	valid := testDevice(t)
	for _, tc := range []struct {
		name    string
		device  receiver.Device
		options Options
	}{
		{"cast", receiver.Device{Kind: receiver.Cast}, testOptions()},
		{"audio", receiver.Device{Kind: receiver.Audio}, testOptions()},
		{"invalid host", receiver.Device{Kind: receiver.AirPlay, Name: "TV", ID: "id", Host: "host;evil", Port: 7000}, testOptions()},
		{"invalid identity", receiver.Device{Kind: receiver.AirPlay, Name: "TV", ID: "id\n", Host: "127.0.0.1", Port: 7000}, testOptions()},
		{"invalid profile", valid, Options{Quality: "unvalidated-4k", FPS: 30}},
		{"zero fps", valid, Options{Quality: BestQuality}},
		{"negative fps", valid, Options{Quality: BestQuality, FPS: -1}},
		{"high fps", valid, Options{Quality: Balanced, FPS: 61}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := c.Start(context.Background(), tc.device, tc.options); err == nil {
				t.Fatal("invalid start succeeded")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c.Start(ctx, valid, testOptions()) == nil || c.Start(nil, valid, testOptions()) == nil {
		t.Fatal("accepted a missing or canceled context")
	}
	if launched || c.Snapshot().State != Idle {
		t.Fatal("invalid start reached the engine")
	}
}

func TestBundledEngineOnly(t *testing.T) {
	dir := t.TempDir()
	gui := filepath.Join(dir, "aircast")
	engineName := "aircast-engine"
	if runtime.GOOS == "windows" {
		engineName += ".exe"
	}
	engine := filepath.Join(dir, engineName)
	if err := os.WriteFile(gui, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	untrusted := t.TempDir()
	if err := os.WriteFile(filepath.Join(untrusted, engineName), []byte("not bundled"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", untrusted)
	t.Chdir(untrusted)
	if _, err := siblingEngine(gui); err == nil {
		t.Fatal("found a nonexistent sibling engine")
	}
	if _, err := siblingEngine("aircast"); err == nil {
		t.Fatal("accepted a relative executable path")
	}
	if err := os.WriteFile(engine, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := siblingEngine(gui); err != nil || got != engine {
		t.Fatalf("sibling resolution got %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(engine, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := siblingEngine(gui); err == nil {
			t.Fatal("accepted a non-executable sibling")
		}
	}
	if err := os.Remove(engine); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), engineName)
	if err := os.WriteFile(outside, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, engine); err == nil {
		if _, err := siblingEngine(gui); err == nil {
			t.Fatal("accepted an engine symlink outside the application bundle")
		}
	}
}

func TestStartFailureCanRetry(t *testing.T) {
	c := New(nil)
	t.Cleanup(func() { c.Close() })
	c.resolve = func() (string, error) { return filepath.Join(t.TempDir(), "missing-engine"), nil }
	if err := c.Start(context.Background(), testDevice(t), testOptions()); err == nil {
		t.Fatal("missing engine started")
	}
	waitSession(t, c)
	if c.Snapshot().State != Failed {
		t.Fatal("start failure did not publish failed")
	}
	if err := c.Start(context.Background(), testDevice(t), testOptions()); err == nil {
		t.Fatal("missing engine started on retry")
	}
	waitSession(t, c)
}

func TestCredentialAndMediaLifecycle(t *testing.T) {
	c := helperController(t, "pairing", nil)
	device := testDevice(t)
	if err := c.SubmitCredential("1234"); err == nil {
		t.Fatal("accepted an unsolicited credential")
	}
	if err := c.Start(context.Background(), device, testOptions()); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(context.Background(), device, testOptions()); err == nil {
		t.Fatal("started a second active session")
	}
	event := waitSnapshot(t, c, func(event Event) bool { return event.Type == "credential" })
	if event.State != Pairing || event.CredentialKind != "pin" || event.DeviceID != device.ID || event.DeviceName != device.Name {
		t.Fatalf("bad credential request: %#v", event)
	}
	if c.SubmitCredential("not-a-pin") == nil {
		t.Fatal("accepted an invalid PIN")
	}
	if err := c.SubmitCredential("1234"); err != nil {
		t.Fatal(err)
	}
	if c.SubmitCredential("1234") == nil {
		t.Fatal("accepted a duplicate credential")
	}
	event = waitSnapshot(t, c, func(event Event) bool { return event.State == Streaming && event.Frames == 100 })
	if event.Width != 1920 || event.Height != 1080 || event.Codec != "h264" || event.Bytes != 5000 {
		t.Fatalf("missing negotiated media metadata: %#v", event)
	}
	if strings.Contains(event.Message, "1234") {
		t.Fatal("credential leaked into a public event")
	}
	if err := c.Stop(); err != nil {
		t.Fatal(err)
	}
	waitSession(t, c)
	if got := c.Snapshot(); got.State != Idle || got.CredentialKind != "" {
		t.Fatalf("stop did not reach idle: %#v", got)
	}
	if c.SubmitCredential("1234") == nil {
		t.Fatal("accepted a credential after stop")
	}
}

func TestNoStreamingWithoutMediaEvent(t *testing.T) {
	c := helperController(t, "not-streaming", nil)
	if err := c.Start(context.Background(), testDevice(t), testOptions()); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(event Event) bool { return event.State == Starting })
	time.Sleep(40 * time.Millisecond)
	if got := c.Snapshot(); got.State != Starting || got.Frames != 0 || got.Bytes != 0 {
		t.Fatalf("stats claimed streaming: %#v", got)
	}
}

func TestUnsafeEngineOutput(t *testing.T) {
	for _, mode := range []string{"secrets", "oversized", "malformed", "exit"} {
		t.Run(mode, func(t *testing.T) {
			c := helperController(t, mode, nil)
			if err := c.Start(context.Background(), testDevice(t), testOptions()); err != nil {
				t.Fatal(err)
			}
			waitSession(t, c)
			event := c.Snapshot()
			encoded, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			if event.State != Failed || len(encoded) > 2048 || strings.Contains(string(encoded), "87654321") || strings.Contains(string(encoded), "PRIVATE KEY") || strings.Contains(string(encoded), "unsafe-password") {
				t.Fatalf("unsafe terminal event: state=%q size=%d", event.State, len(encoded))
			}
		})
	}
}

func TestStopIsAsyncAndForcesUnresponsiveEngine(t *testing.T) {
	c := helperController(t, "hang", nil)
	if err := c.Start(context.Background(), testDevice(t), testOptions()); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(event Event) bool { return event.State == Starting })
	before := time.Now()
	if err := c.Stop(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(before); elapsed > 100*time.Millisecond {
		t.Fatalf("Stop blocked for %v", elapsed)
	}
	if err := c.SubmitCredential("1234"); err == nil {
		t.Fatal("accepted a credential while stopping")
	}
	waitSession(t, c)
	if c.Snapshot().State != Idle || time.Since(before) > closeTimeout {
		t.Fatalf("forced stop did not complete: state=%q elapsed=%v", c.Snapshot().State, time.Since(before))
	}
}

func TestContextCancellationStopsEngine(t *testing.T) {
	c := helperController(t, "hang", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.Start(ctx, testDevice(t), testOptions()); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(event Event) bool { return event.State == Starting })
	cancel()
	waitSession(t, c)
	if c.Snapshot().State != Idle {
		t.Fatalf("cancellation ended in %q", c.Snapshot().State)
	}
}

func TestBlockedEngineInputDoesNotBlockClose(t *testing.T) {
	c := helperController(t, "never-read", nil)
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s := &session{generation: 1, stop: make(chan struct{}), done: make(chan struct{}), credentials: make(chan credential, 1)}
	c.active, c.generation = s, 1
	c.current = Event{Type: "state", State: Connecting}
	command := startCommand{Command: "start"}
	base, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	command.Host = strings.Repeat("h", maxProtocolLine-len(base))
	started := make(chan error, 1)
	go c.run(context.Background(), s, path, command, started)
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(event Event) bool { return event.State == Starting })
	before := time.Now()
	c.Stop()
	if time.Since(before) > 100*time.Millisecond {
		t.Fatal("a full engine input pipe blocked Stop")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if c.Snapshot().State != Idle {
		t.Fatal("closing a blocked input pipe did not reap the engine")
	}
}

func TestCloseAndSnapshotsAreConcurrent(t *testing.T) {
	c := helperController(t, "not-streaming", nil)
	if err := c.Start(context.Background(), testDevice(t), testOptions()); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 24 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 50 {
				c.Snapshot()
				c.SubmitCredential("1234")
				c.Stop()
			}
			if err := c.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	if c.Available() == nil || c.Start(context.Background(), testDevice(t), testOptions()) == nil {
		t.Fatal("closed controller remained available")
	}
}

func TestCallbackCanStopAndClose(t *testing.T) {
	called := make(chan struct{})
	var c *Controller
	var once sync.Once
	c = helperController(t, "not-streaming", func(event Event) {
		if event.State == Starting {
			once.Do(func() {
				c.Snapshot()
				c.Stop()
				c.Close()
				close(called)
			})
		}
	})
	if err := c.Start(context.Background(), testDevice(t), testOptions()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
	case <-time.After(closeTimeout + time.Second):
		t.Fatal("callback deadlocked on the controller")
	}
}

func TestSlowCallbackDoesNotBlockShutdown(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	c := helperController(t, "not-streaming", func(Event) {
		once.Do(func() {
			close(entered)
			<-release
		})
	})
	defer close(release)
	if err := c.Start(context.Background(), testDevice(t), testOptions()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("callback was not invoked")
	}
	before := time.Now()
	c.Stop()
	if time.Since(before) > 100*time.Millisecond {
		t.Fatal("a blocked callback blocked Stop")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCoalescedStatsDoNotHideStreamingState(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	streaming := make(chan Event, 1)
	first := true
	c := helperController(t, "pairing", func(event Event) {
		if first {
			first = false
			close(entered)
			<-release
		}
		if event.State == Streaming {
			select {
			case streaming <- event:
			default:
			}
		}
	})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	if err := c.Start(context.Background(), testDevice(t), testOptions()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("callback was not invoked")
	}
	waitSnapshot(t, c, func(event Event) bool { return event.Type == "credential" })
	if err := c.SubmitCredential("1234"); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(event Event) bool {
		return event.Type == "stats" && event.State == Streaming && event.Frames == 100
	})
	releaseOnce.Do(func() { close(release) })
	select {
	case event := <-streaming:
		if event.Type != "state" || event.DeviceID == "" || event.Frames != 100 {
			t.Fatalf("coalesced stats hid the state change: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("coalesced streaming event was not delivered")
	}
}

func TestBundledExecutableLifecycle(t *testing.T) {
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "bundle with spaces 電")
	if err := os.Mkdir(bundle, 0700); err != nil {
		t.Fatal(err)
	}
	extension := ""
	if runtime.GOOS == "windows" {
		extension = ".exe"
	}
	gui := filepath.Join(bundle, "aircast"+extension)
	for _, path := range []string{gui, filepath.Join(bundle, "aircast-engine"+extension)} {
		input, err := os.Open(source)
		if err != nil {
			t.Fatal(err)
		}
		output, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
		if err != nil {
			input.Close()
			t.Fatal(err)
		}
		_, err = io.Copy(output, input)
		input.Close()
		output.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, gui)
	process.Dir = t.TempDir()
	process.Env = replaceEnv(os.Environ(), "AIRCAST_TEST_ENGINE", "bundle-controller")
	if err := process.Run(); err != nil {
		t.Fatalf("bundled supervisor lifecycle failed: %v", err)
	}
}

func bundledControllerHelper() int {
	os.Setenv("AIRCAST_TEST_ENGINE", "pairing")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := New(nil)
	defer c.Close()
	if c.Available() != nil {
		return 30
	}
	device := receiver.Device{Kind: receiver.AirPlay, ID: "bundle-test", Name: "Bundle test", Host: "127.0.0.1", Port: 7000}
	if err := c.Start(ctx, device, testOptions()); err != nil {
		return 31
	}
	submitted := false
	for {
		event := c.Snapshot()
		if event.State == Pairing && event.Type == "credential" && !submitted {
			if c.SubmitCredential("1234") != nil {
				return 32
			}
			submitted = true
		}
		if event.State == Streaming && event.Frames == 100 {
			if event.DeviceID != device.ID || event.DeviceName != device.Name || c.Stop() != nil || c.Close() != nil || c.Snapshot().State != Idle {
				return 33
			}
			return 0
		}
		if event.State == Failed || ctx.Err() != nil {
			return 34
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func engineHelper(mode string) int {
	if mode == "bundle-controller" {
		return bundledControllerHelper()
	}
	if mode == "never-read" {
		json.NewEncoder(os.Stdout).Encode(Event{Type: "state", State: Starting})
		for {
			time.Sleep(time.Second)
		}
	}
	if platformHelper(mode) {
		return 0
	}
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return 2
	}
	var start startCommand
	if json.Unmarshal(scanner.Bytes(), &start) != nil || start.Command != "start" || start.Host != "127.0.0.1" || start.Port != 7000 || start.Quality != BestQuality || start.FPS != 30 || start.Audio || !start.Cursor {
		return 3
	}
	emit := func(event Event) { json.NewEncoder(os.Stdout).Encode(event) }
	switch mode {
	case "exit":
		return 9
	case "oversized":
		fmt.Fprintln(os.Stdout, strings.Repeat("a", maxProtocolLine+1))
		return 0
	case "malformed":
		fmt.Fprintln(os.Stdout, "{bad}")
		return 0
	case "secrets":
		for range 128 {
			fmt.Fprintln(os.Stderr, "PIN=87654321 unsafe-password -----BEGIN PRIVATE KEY-----")
		}
		emit(Event{Type: "state", State: Failed, Message: "PIN=87654321 unsafe-password -----BEGIN PRIVATE KEY-----" + strings.Repeat("x", 20000), CredentialKind: "unsafe-password", Codec: "unsafe-password", DeviceName: "unsafe-password", DeviceID: "unsafe-password"})
		return 0
	case "pairing":
		emit(Event{Type: "state", State: Connecting})
		emit(Event{Type: "credential", State: Pairing, CredentialKind: "pin", Message: "87654321"})
		if !scanner.Scan() {
			return 4
		}
		var command struct {
			Command string `json:"command"`
			Value   string `json:"value"`
		}
		if json.Unmarshal(scanner.Bytes(), &command) != nil || command.Command != "credential" || command.Value != "1234" {
			return 5
		}
		emit(Event{Type: "state", State: Starting, Width: 1920, Height: 1080, Codec: "h264"})
		emit(Event{Type: "state", State: Streaming, Message: "accepted PIN 1234"})
		emit(Event{Type: "stats", Frames: 100, Bytes: 5000})
		emit(Event{Type: "stats", Frames: 1, Bytes: 1})
	case "not-streaming", "hang":
		emit(Event{Type: "state", State: Starting})
		emit(Event{Type: "stats", State: Streaming, Frames: 999, Bytes: 999})
		if mode == "hang" {
			for {
				time.Sleep(time.Second)
			}
		}
	default:
		return 6
	}
	for scanner.Scan() {
		var command struct{ Command string }
		if json.Unmarshal(scanner.Bytes(), &command) != nil {
			return 7
		}
		if command.Command == "stop" {
			emit(Event{Type: "state", State: Idle})
			return 0
		}
	}
	if scanner.Err() != nil && scanner.Err() != io.EOF {
		return 8
	}
	return 0
}
