package casting

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"aircast/internal/receiver"
)

func platformHelper(mode string) bool {
	if mode == "supervisor" {
		path, _ := os.Executable()
		os.Setenv("AIRCAST_TEST_ENGINE", "tree")
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
	if mode != "tree" && mode != "exit-tree" {
		return false
	}
	self, _ := syscall.GetCurrentProcess()
	var inJob int32
	ok, _, _ := kernel32.NewProc("IsProcessInJob").Call(uintptr(self), 0, uintptr(unsafe.Pointer(&inJob)))
	if ok == 0 || inJob == 0 {
		os.Exit(23)
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
	for {
		time.Sleep(time.Second)
	}
}

func waitNotRunning(t *testing.T, pid int) {
	t.Helper()
	process, err := syscall.OpenProcess(0x00100000, false, uint32(pid))
	if err == syscall.Errno(87) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(process)
	result, err := syscall.WaitForSingleObject(process, 5000)
	if err != nil || result != syscall.WAIT_OBJECT_0 {
		t.Fatalf("descendant %d is still running", pid)
	}
}

func TestWindowsJobStopsDescendants(t *testing.T) {
	for _, mode := range []string{"tree", "exit-tree"} {
		t.Run(mode, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "child.pid")
			t.Setenv("AIRCAST_TEST_PID_FILE", pidFile)
			c := helperController(t, mode, nil)
			if err := c.Start(context.Background(), testDevice(t), testOptions()); err != nil {
				t.Fatal(err)
			}
			pid := readHelperPID(t, pidFile)
			if mode == "tree" {
				c.Stop()
			}
			waitSession(t, c)
			waitNotRunning(t, pid)
			if c.Snapshot().State != Idle {
				t.Fatalf("job shutdown ended in %q", c.Snapshot().State)
			}
		})
	}
}

func TestWindowsParentExitClosesJob(t *testing.T) {
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
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	parent.Wait()
	waitNotRunning(t, engine)
	waitNotRunning(t, leaf)
}
