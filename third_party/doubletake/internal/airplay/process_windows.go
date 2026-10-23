package airplay

import (
	"os/exec"
	"syscall"
)

func startGStreamerCommand(cmd *exec.Cmd) (<-chan error, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// The Aircast supervisor's kill-on-close Job Object owns this process tree.
	cmd.SysProcAttr.HideWindow = true
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	waitResult := make(chan error, 1)
	go func() {
		waitResult <- cmd.Wait()
		close(waitResult)
	}()
	return waitResult, nil
}
