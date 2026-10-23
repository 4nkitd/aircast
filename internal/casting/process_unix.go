//go:build linux

package casting

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
)

type unixChild struct {
	command  *exec.Cmd
	mu       sync.Mutex
	killDone bool
	killErr  error
}

func startChild(path string, stdin, stdout, stderr *os.File) (childProcess, error) {
	if err := retainedWaitAvailable(); err != nil {
		return nil, err
	}
	command := exec.Command(path)
	command.Dir = filepath.Dir(path)
	command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
	configureProcess(command)
	if err := command.Start(); err != nil {
		return nil, err
	}
	return &unixChild{command: command}, nil
}

func (p *unixChild) wait() error {
	err := waitForChildExit(p.command.Process.Pid)
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.killDone, p.killErr = true, err
		if errors.Is(err, syscall.ECHILD) || errors.Is(err, syscall.ESRCH) {
			p.command.Process.Release()
			return err
		}
		killErr := p.command.Process.Kill()
		waitErr := p.command.Wait()
		return errors.Join(err, killErr, waitErr)
	}
	// WNOWAIT reserves the exited leader's PID until group cleanup completes.
	// Serialize the final reap with every numeric process-group signal.
	killErr := p.killLocked()
	waitErr := p.command.Wait()
	return errors.Join(killErr, waitErr)
}

func (p *unixChild) kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killLocked()
}

func (p *unixChild) killLocked() error {
	if p.killDone {
		return p.killErr
	}
	p.killDone = true
	p.killErr = retryEINTR(func() error { return syscall.Kill(-p.command.Process.Pid, syscall.SIGKILL) })
	if errors.Is(p.killErr, syscall.ESRCH) {
		p.killErr = nil
	}
	return p.killErr
}

func (p *unixChild) close() error { return p.kill() }

func retryEINTR(call func() error) error {
	for {
		err := call()
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}
