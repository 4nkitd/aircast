package airplay

import (
	"os/exec"
	"runtime"
	"syscall"
)

func startGStreamerCommand(cmd *exec.Cmd) (<-chan error, error) {
	started := make(chan error, 1)
	waitResult := make(chan error, 1)
	go func() {
		// Linux ties Pdeathsig to the creating thread, not the Go process.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if cmd.SysProcAttr == nil {
			cmd.SysProcAttr = &syscall.SysProcAttr{}
		}
		cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
		err := cmd.Start()
		started <- err
		if err == nil {
			waitResult <- cmd.Wait()
		}
		close(waitResult)
	}()
	if err := <-started; err != nil {
		return nil, err
	}
	return waitResult, nil
}
