package casting

import (
	"context"
	"errors"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"aircast/internal/receiver"
)

const stopGrace = 1500 * time.Millisecond
const closeTimeout = 5 * time.Second

type Controller struct {
	mu         sync.Mutex
	onEvent    func(Event)
	resolve    func() (string, error)
	current    Event
	active     *session
	generation uint64
	closed     bool
	closeErr   error
	changed    chan struct{}
	closedCh   chan struct{}
	closeDone  chan struct{}
}

type session struct {
	generation        uint64
	stop              chan struct{}
	done              chan struct{}
	credentials       chan credential
	stopping          bool
	terminal          bool
	credentialKind    string
	credentialVersion uint64
	credentialPending bool
	sessionOnly       bool
	audioWarning      string
	captureWarning    string
}

type credential struct {
	value   string
	version uint64
}

func New(onEvent func(Event)) *Controller {
	c := &Controller{
		onEvent:   onEvent,
		resolve:   bundledEngine,
		current:   Event{Type: "state", State: Idle, Message: "Screen sharing stopped"},
		changed:   make(chan struct{}, 1),
		closedCh:  make(chan struct{}),
		closeDone: make(chan struct{}),
	}
	if onEvent != nil {
		go c.dispatch()
	}
	return c
}

func (c *Controller) Available() error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return errors.New("the casting controller is closed")
	}
	_, err := c.resolve()
	return err
}

func (c *Controller) Start(ctx context.Context, device receiver.Device, options Options) error {
	if ctx == nil {
		return errors.New("a casting context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if device.Kind != receiver.AirPlay {
		return errors.New("screen sharing currently supports AirPlay receivers only")
	}
	if err := device.Validate(); err != nil {
		return err
	}
	if options.Quality != BestQuality && options.Quality != Balanced {
		return errors.New("choose the quality or balanced casting profile")
	}
	if options.FPS < 1 || options.FPS > 60 {
		return errors.New("casting frame rate must be between 1 and 60")
	}
	path, err := c.resolve()
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("the casting controller is closed")
	}
	if c.active != nil {
		c.mu.Unlock()
		return errors.New("screen sharing is already active or stopping")
	}
	c.generation++
	s := &session{
		generation:  c.generation,
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
		credentials: make(chan credential, 1),
	}
	c.active = s
	c.current = Event{Type: "state", State: Connecting, Message: "Connecting to AirPlay receiver", DeviceID: device.ID, DeviceName: device.Name}
	c.notifyLocked()
	c.mu.Unlock()
	started := make(chan error, 1)
	go c.run(ctx, s, path, startCommand{
		Command: "start", Host: device.Host, Port: device.Port,
		Quality: options.Quality, FPS: options.FPS, Audio: options.Audio, Cursor: options.ShowCursor,
	}, started)
	return <-started
}

func (c *Controller) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active != nil {
		c.stopLocked(c.active)
	}
	return nil
}

func (c *Controller) SubmitCredential(value string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.active
	if c.closed || s == nil || s.stopping || s.terminal || !s.credentialPending || c.current.State != Pairing {
		return errors.New("the receiver is not requesting a credential")
	}
	if !validCredential(value, s.credentialKind) {
		return errors.New("enter a valid receiver credential")
	}
	select {
	case s.credentials <- credential{value: value, version: s.credentialVersion}:
		s.credentialPending = false
		return nil
	default:
		return errors.New("a receiver credential is already queued")
	}
}

func (c *Controller) Snapshot() Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

func (c *Controller) Close() error {
	c.mu.Lock()
	if c.closed {
		done := c.closeDone
		c.mu.Unlock()
		<-done
		c.mu.Lock()
		err := c.closeErr
		c.mu.Unlock()
		return err
	}
	c.closed = true
	s := c.active
	if s != nil {
		c.stopLocked(s)
	}
	c.mu.Unlock()
	var err error
	if s != nil {
		timer := time.NewTimer(closeTimeout)
		select {
		case <-s.done:
		case <-timer.C:
			err = errors.New("the casting engine did not exit within the shutdown time limit")
		}
		timer.Stop()
	}
	c.mu.Lock()
	c.closeErr = err
	close(c.closedCh)
	close(c.closeDone)
	c.mu.Unlock()
	return err
}

func (c *Controller) stopLocked(s *session) {
	if s.stopping {
		return
	}
	s.stopping = true
	s.credentialPending = false
	s.credentialKind = ""
	close(s.stop)
	if !s.terminal {
		c.current.Type, c.current.State = "state", Stopping
		c.current.Message, c.current.CredentialKind = "Stopping screen sharing", ""
		c.notifyLocked()
	}
}

func (c *Controller) fail(s *session, message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active != s || c.generation != s.generation || s.terminal || s.stopping {
		return
	}
	s.terminal = true
	c.current.Type, c.current.State = "state", Failed
	c.current.Message, c.current.CredentialKind = message, ""
	c.notifyLocked()
	c.stopLocked(s)
}

func (c *Controller) finish(s *session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == s && c.generation == s.generation {
		if !s.terminal {
			c.current.Type, c.current.CredentialKind = "state", ""
			if s.stopping {
				c.current.State, c.current.Message = Idle, "Screen sharing stopped"
			} else {
				c.current.State, c.current.Message = Failed, "The casting engine exited unexpectedly"
			}
		}
		c.active = nil
		c.notifyLocked()
	}
	close(s.done)
}

func (c *Controller) notifyLocked() {
	select {
	case c.changed <- struct{}{}:
	default:
	}
}

func (c *Controller) dispatch() {
	var generation uint64
	var state State
	for {
		select {
		case <-c.closedCh:
			return
		case <-c.changed:
			select {
			case <-c.closedCh:
				return
			default:
			}
			c.mu.Lock()
			event, currentGeneration := c.current, c.generation
			c.mu.Unlock()
			// Coalescing stats must not hide the media state change from consumers.
			if event.Type == "stats" && (event.State != state || currentGeneration != generation) {
				event.Type = "state"
			}
			generation, state = currentGeneration, event.State
			c.onEvent(event)
		}
	}
}

func validCredential(value, kind string) bool {
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) {
		return false
	}
	if kind == "pin" {
		if len(value) > 16 {
			return false
		}
		for _, r := range value {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	if kind != "password" {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return false
		}
	}
	return true
}
