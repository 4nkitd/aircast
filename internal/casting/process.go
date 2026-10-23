package casting

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type childProcess interface {
	wait() error
	kill() error
	close() error
}

type engineProcess struct {
	child  childProcess
	stdin  *os.File
	stdout *os.File
}

func bundledEngine() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", errors.New("cannot locate the Aircast executable")
	}
	return siblingEngine(path)
}

func siblingEngine(executable string) (string, error) {
	if !filepath.IsAbs(executable) {
		return "", errors.New("the Aircast executable path must be absolute")
	}
	executable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", errors.New("cannot resolve the Aircast executable")
	}
	name := "aircast-engine"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(filepath.Dir(executable), name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("the bundled casting engine is missing; place aircast-engine beside Aircast")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		return "", errors.New("the bundled casting engine is not executable")
	}
	return path, nil
}

func startEngine(path string) (*engineProcess, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("the casting engine path must be absolute")
	}
	inReader, inWriter, err := os.Pipe()
	if err != nil {
		return nil, errors.New("cannot create the casting engine input")
	}
	defer inReader.Close()
	outReader, outWriter, err := os.Pipe()
	if err != nil {
		inWriter.Close()
		return nil, errors.New("cannot create the casting engine output")
	}
	defer outWriter.Close()
	stderr, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		inWriter.Close()
		outReader.Close()
		return nil, errors.New("cannot discard casting engine diagnostics")
	}
	defer stderr.Close()
	child, err := startChild(path, inReader, outWriter, stderr)
	if err != nil {
		inWriter.Close()
		outReader.Close()
		return nil, errors.New("cannot start the bundled casting engine")
	}
	return &engineProcess{child: child, stdin: inWriter, stdout: outReader}, nil
}

func (c *Controller) run(ctx context.Context, s *session, path string, command startCommand, started chan<- error) {
	unlock := lockProcessThread()
	defer unlock()
	defer c.finish(s)
	if err := ctx.Err(); err != nil {
		c.Stop()
		started <- err
		return
	}
	select {
	case <-s.stop:
		started <- errors.New("screen sharing was stopped before the engine started")
		return
	default:
	}
	p, err := startEngine(path)
	if err != nil {
		c.fail(s, "Cannot start the bundled casting engine")
		started <- err
		return
	}
	defer p.child.close()
	defer p.stdin.Close()
	defer p.stdout.Close()
	done := make(chan struct{})
	defer close(done)
	output := make(chan protocolResult, 8)
	writeErrors := make(chan error, 1)
	waited := make(chan error, 1)
	go readProtocol(p.stdout, output, done)
	go c.writeInput(s, p.stdin, command, writeErrors)
	go func() { waited <- p.child.wait() }()
	started <- nil
	stop, cancelled := s.stop, ctx.Done()
	var timer *time.Timer
	var deadline <-chan time.Time
	var exited, outputDone bool
	beginShutdown := func() {
		if timer == nil {
			timer = time.NewTimer(stopGrace)
			deadline = timer.C
		}
	}
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		if exited && outputDone {
			return
		}
		select {
		case <-cancelled:
			cancelled = nil
			c.mu.Lock()
			if c.active == s {
				c.stopLocked(s)
			}
			c.mu.Unlock()
			beginShutdown()
		case <-stop:
			stop = nil
			beginShutdown()
		case result := <-output:
			if result.err != nil {
				outputDone = true
				output = nil
				if result.err != io.EOF {
					c.fail(s, "The casting engine sent an invalid response")
				} else {
					c.mu.Lock()
					unexpected := !s.terminal && !s.stopping
					c.mu.Unlock()
					if unexpected {
						c.fail(s, "The casting engine closed unexpectedly")
					}
				}
				beginShutdown()
			} else {
				c.accept(s, result.event)
			}
		case <-writeErrors:
			writeErrors = nil
			c.fail(s, "The casting engine input closed unexpectedly")
			beginShutdown()
		case <-waited:
			waited = nil
			exited = true
			p.child.kill()
			p.stdin.Close()
			beginShutdown()
		case <-deadline:
			deadline = nil
			// Kill readers before closing a pipe that may have a blocked native write.
			p.child.kill()
			p.stdin.Close()
			p.stdout.Close()
			if exited {
				return
			}
		}
	}
}

func (c *Controller) writeInput(s *session, input *os.File, command startCommand, failures chan<- error) {
	defer input.Close()
	if err := writeProtocol(input, command); err != nil {
		failures <- err
		return
	}
	stop := func() {
		writeProtocol(input, struct {
			Command string `json:"command"`
		}{Command: "stop"})
	}
	for {
		select {
		case <-s.stop:
			stop()
			return
		default:
		}
		select {
		case <-s.stop:
			stop()
			return
		case credential := <-s.credentials:
			c.mu.Lock()
			valid := c.active == s && c.generation == s.generation && !s.stopping && !s.terminal && s.credentialVersion == credential.version && c.current.State == Pairing
			c.mu.Unlock()
			if !valid {
				continue
			}
			err := writeProtocol(input, struct {
				Command string `json:"command"`
				Value   string `json:"value"`
			}{Command: "credential", Value: credential.value})
			if err != nil {
				failures <- err
				return
			}
		}
	}
}
