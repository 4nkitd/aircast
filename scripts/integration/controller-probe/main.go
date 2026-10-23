package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"aircast/internal/casting"
	"aircast/internal/receiver"
)

type command struct {
	Command string `json:"command"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Quality string `json:"quality"`
	FPS     int    `json:"fps"`
	Audio   bool   `json:"audio"`
	Cursor  bool   `json:"cursor"`
	Value   string `json:"value"`
}

type output struct {
	mu      sync.Mutex
	encoder *json.Encoder
	cancel  context.CancelFunc
}

func (o *output) emit(value any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.encoder.Encode(value) != nil {
		o.cancel()
	}
}

func main() {
	os.Exit(run())
}

func run() int {
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "the acceptance probe is Linux-only; it is not a Windows runtime test")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	o := &output{encoder: json.NewEncoder(os.Stdout), cancel: stop}
	c := casting.New(func(event casting.Event) { o.emit(event) })
	defer c.Close()
	if err := c.Available(); err != nil {
		o.emit(map[string]any{"probe": "ready", "ok": false, "error": err.Error()})
		return 1
	}
	o.emit(map[string]any{"probe": "ready", "ok": true})
	lines := make(chan []byte)
	readError := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 4096), (64<<10)+2)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
		readError <- scanner.Err()
	}()
	var cancelSession context.CancelFunc
	defer func() {
		if cancelSession != nil {
			cancelSession()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return 0
		case err := <-readError:
			if err != nil {
				return 1
			}
			return 0
		case line := <-lines:
			var input command
			if len(line) > 64<<10 || json.Unmarshal(line, &input) != nil {
				return 2
			}
			started := time.Now()
			var err error
			var snapshot *casting.Event
			switch input.Command {
			case "start":
				if input.Host != "127.0.0.1" || input.Audio {
					err = errors.New("the acceptance probe permits loopback video-only fixtures")
					break
				}
				var device receiver.Device
				device, err = receiver.Manual("Aircast SYNTHETIC LOOPBACK acceptance receiver", fmt.Sprintf("127.0.0.1:%d", input.Port), receiver.AirPlay)
				if err != nil {
					break
				}
				var sessionContext context.Context
				var cancel context.CancelFunc
				sessionContext, cancel = context.WithCancel(ctx)
				err = c.Start(sessionContext, device, casting.Options{Quality: input.Quality, FPS: input.FPS, Audio: false, ShowCursor: input.Cursor})
				if err != nil {
					cancel()
				} else {
					if cancelSession != nil {
						cancelSession()
					}
					cancelSession = cancel
				}
			case "credential":
				err = c.SubmitCredential(input.Value)
			case "stop":
				err = c.Stop()
			case "cancel":
				if cancelSession == nil {
					err = errors.New("no session context exists")
				} else {
					cancelSession()
				}
			case "snapshot":
				value := c.Snapshot()
				snapshot = &value
			case "available":
				err = c.Available()
			case "close":
				err = c.Close()
			default:
				err = errors.New("unknown acceptance probe command")
			}
			reply := map[string]any{"probe": "reply", "command": input.Command, "ok": err == nil, "duration_ms": float64(time.Since(started)) / float64(time.Millisecond)}
			if err != nil {
				reply["error"] = err.Error()
			}
			if snapshot != nil {
				reply["snapshot"] = snapshot
			}
			o.emit(reply)
		}
	}
}
