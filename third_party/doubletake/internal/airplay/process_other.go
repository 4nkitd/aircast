//go:build !linux && !windows

package airplay

import "os/exec"

func startGStreamerCommand(cmd *exec.Cmd) (<-chan error, error) {
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
