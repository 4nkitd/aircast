package casting

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"
)

func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}

func lockProcessThread() func() {
	// Linux ties Pdeathsig to the creating thread, not just its process.
	runtime.LockOSThread()
	return runtime.UnlockOSThread
}

func retainedWaitAvailable() error {
	if err := waitid(os.Getpid(), syscall.WEXITED|syscall.WNOWAIT|syscall.WNOHANG); errors.Is(err, syscall.ECHILD) {
		return nil
	}
	return errors.New("the system cannot safely retain casting engine process identity")
}

func waitForChildExit(pid int) error {
	return waitid(pid, syscall.WEXITED|syscall.WNOWAIT)
}

func waitid(pid, options int) error {
	// Linux siginfo_t is 128 bytes. Its contents are not needed with blocking
	// P_PID waits; keeping the child unreaped is the identity guarantee.
	var info [16]uint64
	return retryEINTR(func() error {
		_, _, errno := syscall.Syscall6(syscall.SYS_WAITID, 1, uintptr(pid), uintptr(unsafe.Pointer(&info[0])), uintptr(options), 0, 0)
		if errno != 0 {
			return errno
		}
		return nil
	})
}
