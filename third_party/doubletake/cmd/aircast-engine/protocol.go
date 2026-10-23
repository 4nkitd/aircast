package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"strings"
	"sync"

	"doubletake/internal/airplay"
)

const maxCommandBytes = 64 << 10

const gdiCaptureNotice = "Using GDI compatibility capture instead of DXGI"

type command struct {
	Command string `json:"command"`
	Host    string `json:"host,omitempty"`
	Port    int    `json:"port,omitempty"`
	Quality string `json:"quality,omitempty"`
	FPS     int    `json:"fps,omitempty"`
	Audio   bool   `json:"audio,omitempty"`
	Cursor  *bool  `json:"cursor,omitempty"`
	Value   string `json:"value,omitempty"`
}

type event struct {
	Event   string `json:"event"`
	State   string `json:"state,omitempty"`
	Message string `json:"message,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
	Codec   string `json:"codec,omitempty"`
	Frames  uint64 `json:"frames,omitempty"`
	Bytes   uint64 `json:"bytes,omitempty"`
}

type eventWriter struct {
	mu       sync.Mutex
	encoder  *json.Encoder
	cancel   context.CancelFunc
	current  string
	err      error
	warnings []string
}

func newEventWriter(output io.Writer, cancel context.CancelFunc) *eventWriter {
	return &eventWriter{encoder: json.NewEncoder(output), cancel: cancel, current: "connecting"}
}

func (w *eventWriter) emit(value event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if value.State != "" {
		if w.current == "stopping" && value.State != "idle" && value.State != "failed" && value.State != "stopping" {
			return nil
		}
		w.current = value.State
		if (value.State == "starting" || value.State == "streaming") && len(w.warnings) > 0 {
			if value.Message != "" {
				value.Message += ". "
			}
			value.Message += strings.Join(w.warnings, ". ")
		}
	}
	w.err = w.encoder.Encode(value)
	if w.err != nil {
		w.cancel()
	}
	return w.err
}

func (w *eventWriter) state(state, message string) error {
	return w.emit(event{Event: "state", State: state, Message: message})
}

func (w *eventWriter) notice(message string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err == nil && w.current != "stopping" {
		w.err = w.encoder.Encode(event{Event: "state", State: w.current, Message: message})
		if w.err != nil {
			w.cancel()
		}
	}
}

func (w *eventWriter) warn(message string) {
	w.mu.Lock()
	for _, previous := range w.warnings {
		if previous == message {
			w.mu.Unlock()
			return
		}
	}
	w.warnings = append(w.warnings, message)
	warnings := strings.Join(w.warnings, ". ")
	w.mu.Unlock()
	w.notice(warnings)
}

type inputResult struct {
	command command
	err     error
}

func readCommands(ctx context.Context, input io.Reader, results chan<- inputResult) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), maxCommandBytes+2)
	for scanner.Scan() {
		line := scanner.Bytes()
		result := inputResult{}
		if len(line) > maxCommandBytes {
			result.err = errors.New("command too large")
		} else {
			decoder := json.NewDecoder(bytes.NewReader(line))
			decoder.DisallowUnknownFields()
			result.err = decoder.Decode(&result.command)
			if result.err == nil && decoder.Decode(new(any)) != io.EOF {
				result.err = errors.New("invalid command")
			}
		}
		select {
		case results <- result:
		case <-ctx.Done():
			return
		}
		if result.err != nil {
			return
		}
	}
	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	select {
	case results <- inputResult{err: err}:
	case <-ctx.Done():
	}
}

func normalizeStart(value command) (command, error) {
	if value.Command != "start" || value.Value != "" || !validHost(value.Host) {
		return value, errors.New("invalid start")
	}
	if value.Port == 0 {
		value.Port = 7000
	}
	if value.Quality == "" {
		value.Quality = "quality"
	}
	if value.FPS == 0 {
		value.FPS = 30
	}
	if value.Cursor == nil {
		show := true
		value.Cursor = &show
	}
	if value.Port < 1 || value.Port > 65535 || value.FPS < 1 || value.FPS > 60 || value.Quality != "quality" && value.Quality != "balanced" {
		return value, errors.New("invalid start options")
	}
	return value, nil
}

func validHost(host string) bool {
	if len(host) == 0 || len(host) > 253 || strings.TrimSpace(host) != host {
		return false
	}
	for _, character := range host {
		if character < 33 || character > 126 {
			return false
		}
	}
	if address, err := netip.ParseAddr(host); err == nil {
		for _, character := range address.Zone() {
			if character != '-' && character != '_' && character != '.' && (character < '0' || character > '9') && (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') {
				return false
			}
		}
		return true
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if character != '-' && (character < '0' || character > '9') && (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') {
				return false
			}
		}
	}
	return true
}

func serve(parent context.Context, input io.ReadCloser, output io.Writer, deps engineDependencies) (code int) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer input.Close()
	writer := newEventWriter(output, cancel)
	defer func() {
		if recover() != nil {
			_ = writer.state("failed", "AirPlay engine stopped after invalid protocol data")
			code = 1
		}
	}()
	inputs := make(chan inputResult)
	go readCommands(ctx, input, inputs)
	var active *sender
	var done chan error
	finish := func(message string) int {
		cancel()
		if done != nil {
			_ = writer.state("stopping", "Stopping screen sharing")
			<-done
		}
		if message != "" {
			_ = writer.state("failed", message)
			return 1
		}
		_ = writer.state("idle", "Screen sharing stopped")
		return 0
	}
	for {
		select {
		case <-ctx.Done():
			return finish("")
		case err := <-done:
			if ctx.Err() != nil {
				_ = writer.state("idle", "Screen sharing stopped")
				return 0
			}
			_ = writer.state("failed", publicMessage(err))
			return 1
		case result := <-inputs:
			if errors.Is(result.err, io.EOF) {
				return finish("")
			}
			if result.err != nil {
				return finish("Invalid engine command; use one JSON object per line, at most 64 KiB")
			}
			value := result.command
			switch value.Command {
			case "start":
				if active != nil {
					return finish("A casting session is already running")
				}
				value, err := normalizeStart(value)
				if err != nil {
					return finish("Invalid receiver or casting options")
				}
				active = &sender{ctx: ctx, start: value, writer: writer, deps: deps}
				done = make(chan error, 1)
				go func() { done <- active.run() }()
			case "credential":
				if active == nil || len(value.Value) == 0 || len(value.Value) > 4096 || !active.submitCredential(value.Value) {
					return finish("No credential is requested, or the credential is empty or too long")
				}
			case "stop":
				return finish("")
			default:
				return finish("Unknown engine command")
			}
		}
	}
}

type engineError struct{ message string }

func (e *engineError) Error() string { return e.message }

func failed(message string) error { return &engineError{message: message} }

func publicMessage(err error) string {
	if errors.Is(err, airplay.ErrInvalidReceiverPlist) {
		return "Receiver sent an invalid property list"
	}
	var public *engineError
	if errors.As(err, &public) {
		return public.message
	}
	return "AirPlay session failed"
}
