package casting

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"aircast/internal/receiver"
)

func platformHelper(mode string) bool {
	if mode == "supervisor" {
		path, _ := os.Executable()
		os.Setenv("AIRCAST_TEST_ENGINE", "eof-tree")
		c := New(nil)
		c.resolve = func() (string, error) { return path, nil }
		device := receiver.Device{Kind: receiver.AirPlay, ID: "test", Name: "Test", Host: "127.0.0.1", Port: 7000}
		if err := c.Start(context.Background(), device, testOptions()); err != nil {
			os.Exit(22)
		}
		for {
			time.Sleep(time.Second)
		}
	}
	if mode == "leaf" {
		for {
			time.Sleep(time.Second)
		}
	}
	if mode != "tree" && mode != "exit-tree" && mode != "eof-tree" {
		return false
	}
	var signals chan os.Signal
	if mode == "eof-tree" {
		signals = make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM)
	}
	path, _ := os.Executable()
	leaf := exec.Command(path)
	leaf.Env = replaceEnv(os.Environ(), "AIRCAST_TEST_ENGINE", "leaf")
	if err := leaf.Start(); err != nil {
		os.Exit(20)
	}
	if err := os.WriteFile(os.Getenv("AIRCAST_TEST_PID_FILE"), []byte(strconv.Itoa(leaf.Process.Pid)), 0600); err != nil {
		leaf.Process.Kill()
		leaf.Wait()
		os.Exit(21)
	}
	os.WriteFile(os.Getenv("AIRCAST_TEST_PID_FILE")+".engine", []byte(strconv.Itoa(os.Getpid())), 0600)
	json.NewEncoder(os.Stdout).Encode(Event{Type: "state", State: Streaming})
	if mode == "exit-tree" {
		json.NewEncoder(os.Stdout).Encode(Event{Type: "state", State: Idle})
		return true
	}
	if mode == "eof-tree" {
		readDone := make(chan struct{})
		go func() {
			buffer := make([]byte, 4096)
			for {
				if _, err := os.Stdin.Read(buffer); err != nil {
					close(readDone)
					return
				}
			}
		}()
		select {
		case <-readDone:
		case <-signals:
		}
		leaf.Process.Kill()
		leaf.Wait()
		return true
	}
	for {
		time.Sleep(time.Second)
	}
}

func waitNotRunning(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
		if os.IsNotExist(err) || strings.Contains(string(status), ") Z ") {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("descendant %d is still running", pid)
}

func TestProcessTreeStoppedAndReaped(t *testing.T) {
	for _, mode := range []string{"tree", "exit-tree"} {
		t.Run(mode, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "child.pid")
			t.Setenv("AIRCAST_TEST_PID_FILE", pidFile)
			c := helperController(t, mode, nil)
			if err := c.Start(context.Background(), testDevice(t), testOptions()); err != nil {
				t.Fatal(err)
			}
			pid := readHelperPID(t, pidFile)
			t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
			if mode == "tree" {
				c.Stop()
			}
			waitSession(t, c)
			waitNotRunning(t, pid)
			if c.Snapshot().State != Idle {
				t.Fatalf("tree shutdown ended in %q", c.Snapshot().State)
			}
		})
	}
}

func TestEngineCleansUpOnStdinEOF(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("AIRCAST_TEST_PID_FILE", pidFile)
	t.Setenv("AIRCAST_TEST_ENGINE", "eof-tree")
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	unlock := lockProcessThread()
	defer unlock()
	p, err := startEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	defer p.child.close()
	defer p.stdout.Close()
	pid := readHelperPID(t, pidFile)
	t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
	p.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- p.child.wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(closeTimeout):
		p.child.kill()
		t.Fatal("engine did not exit on stdin EOF")
	}
	waitNotRunning(t, pid)
}

func TestAbruptParentExitTriggersEngineCleanup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(path)
	parent.Env = replaceEnv(replaceEnv(os.Environ(), "AIRCAST_TEST_ENGINE", "supervisor"), "AIRCAST_TEST_PID_FILE", pidFile)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { parent.Process.Kill() })
	leaf := readHelperPID(t, pidFile)
	engine := readHelperPID(t, pidFile+".engine")
	t.Cleanup(func() { syscall.Kill(leaf, syscall.SIGKILL); syscall.Kill(engine, syscall.SIGKILL) })
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	parent.Wait()
	waitNotRunning(t, engine)
	waitNotRunning(t, leaf)
}

func TestExitedLeaderIdentityRetainedUntilGroupCleanup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("AIRCAST_TEST_PID_FILE", pidFile)
	t.Setenv("AIRCAST_TEST_ENGINE", "exit-tree")
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	unlock := lockProcessThread()
	defer unlock()
	p, err := startEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	p.stdin.Close()
	defer p.stdout.Close()
	child := p.child.(*unixChild)
	child.mu.Lock()
	released := false
	waited := make(chan error, 1)
	go func() { waited <- child.wait() }()
	defer func() {
		if !released {
			child.mu.Unlock()
		}
		child.close()
	}()
	leaf := readHelperPID(t, pidFile)
	retained := make(chan error, 1)
	go func() { retained <- waitForChildExit(child.command.Process.Pid) }()
	select {
	case err := <-retained:
		if err != nil {
			t.Fatalf("leader identity was reaped before group cleanup: %v", err)
		}
	case <-time.After(closeTimeout):
		t.Fatal("leader did not exit")
	}
	select {
	case <-waited:
		t.Fatal("wait returned while process-group cleanup was blocked")
	case <-time.After(50 * time.Millisecond):
	}
	if err := waitid(child.command.Process.Pid, syscall.WEXITED|syscall.WNOWAIT|syscall.WNOHANG); err != nil {
		t.Fatalf("exited leader no longer reserves its identity: %v", err)
	}
	status, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(leaf), "stat"))
	if err != nil || strings.Contains(string(status), ") Z ") {
		t.Fatal("descendant exited before the held group-cleanup step")
	}
	child.mu.Unlock()
	released = true
	select {
	case err := <-waited:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(closeTimeout):
		t.Fatal("group cleanup and final reap did not complete")
	}
	if err := waitForChildExit(child.command.Process.Pid); !errors.Is(err, syscall.ECHILD) {
		t.Fatalf("leader was not finally reaped: %v", err)
	}
	if !child.killDone || child.kill() != nil || child.close() != nil {
		t.Fatal("group cleanup was not sealed before the leader was reaped")
	}
	waitNotRunning(t, leaf)
}

func TestLostLeaderIdentityDisablesGroupSignals(t *testing.T) {
	t.Setenv("AIRCAST_TEST_ENGINE", "exit")
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	unlock := lockProcessThread()
	defer unlock()
	p, err := startEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	defer p.stdin.Close()
	defer p.stdout.Close()
	if err := writeProtocol(p.stdin, startCommand{Command: "start", Host: "127.0.0.1", Port: 7000, Quality: BestQuality, FPS: 30, Cursor: true}); err != nil {
		t.Fatal(err)
	}
	child := p.child.(*unixChild)
	child.command.Wait()
	if err := child.wait(); !errors.Is(err, syscall.ECHILD) {
		t.Fatalf("unexpected lost-identity result: %v", err)
	}
	if !child.killDone || !errors.Is(child.kill(), syscall.ECHILD) || !errors.Is(child.close(), syscall.ECHILD) {
		t.Fatal("lost leader identity allowed a later numeric group signal")
	}
}

func TestProcessSyscallsRetryEINTR(t *testing.T) {
	for _, result := range []error{nil, syscall.EPERM, syscall.ECHILD} {
		calls := 0
		err := retryEINTR(func() error {
			calls++
			if calls < 3 {
				return syscall.EINTR
			}
			return result
		})
		if calls != 3 || !errors.Is(err, result) {
			t.Fatalf("syscall retry returned %v after %d calls", err, calls)
		}
	}
}
