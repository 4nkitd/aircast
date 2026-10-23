package airplay

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

type liveWindowsEncoderFixture struct {
	ctx        context.Context
	ffmpeg     string
	input      io.WriteCloser
	frames     *aviScreenReader
	timestamps chan time.Time
	stderr     *captureStderrTail
	pixels     []byte
}

func startLiveWindowsEncoder(t *testing.T, fps int) *liveWindowsEncoderFixture {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Linux live encoder gate; not a Windows desktop-capture test")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("FFmpeg is required for the live capture regression gate")
	}
	args, _, err := windowsFFmpegVideoArgs(CaptureConfig{FPS: fps, HWAccel: "none", MaxWidth: 640, MaxHeight: 480}, 640, 480)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = input.Close()
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		_ = input.Close()
		_ = output.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = input.Close()
		_ = output.Close()
		_ = stderr.Close()
		_ = cmd.Wait()
	})
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tail := newCaptureStderrTail()
	go logStderr("FFMPEG-LIVE-TEST", stderr, tail)
	timestamps := make(chan time.Time, 4)
	return &liveWindowsEncoderFixture{
		ctx: ctx, ffmpeg: ffmpeg, input: input, timestamps: timestamps, stderr: tail,
		frames: &aviScreenReader{reader: output, ctx: ctx, timestamps: timestamps},
		pixels: bytes.Repeat([]byte{255, 255, 255, 255}, 640*480),
	}
}

func (f *liveWindowsEncoderFixture) submit(pts time.Time) error {
	select {
	case f.timestamps <- pts:
	case <-f.ctx.Done():
		return f.ctx.Err()
	}
	_, err := io.Copy(f.input, bytes.NewReader(f.pixels))
	return err
}

func (f *liveWindowsEncoderFixture) read(t *testing.T, timeout time.Duration) VideoAccessUnit {
	t.Helper()
	type result struct {
		unit VideoAccessUnit
		err  error
	}
	ready := make(chan result, 1)
	go func() {
		unit, err := f.frames.ReadVideoAccessUnit()
		ready <- result{unit: unit, err: err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-ready:
		if result.err != nil {
			t.Fatalf("read live AVI packet: %v; FFmpeg: %s", result.err, f.stderr.String())
		}
		return result.unit
	case <-timer.C:
		t.Fatalf("no complete AU within %v with encoder stdin still open; FFmpeg: %s", timeout, f.stderr.String())
	case <-f.ctx.Done():
		t.Fatalf("live encoder context: %v; FFmpeg: %s", f.ctx.Err(), f.stderr.String())
	}
	return VideoAccessUnit{}
}

func (f *liveWindowsEncoderFixture) decode(t *testing.T, units []VideoAccessUnit) {
	t.Helper()
	var stream []byte
	for _, unit := range units {
		stream = append(stream, unit.AnnexB...)
	}
	decode := exec.CommandContext(f.ctx, f.ffmpeg, "-v", "error", "-xerror", "-f", "h264", "-i", "pipe:0", "-pix_fmt", "yuv420p", "-f", "rawvideo", "pipe:1")
	decode.Stdin = bytes.NewReader(stream)
	var stderr bytes.Buffer
	decode.Stderr = &stderr
	pixels, err := decode.Output()
	if err != nil || len(pixels) != len(units)*640*480*3/2 {
		t.Fatalf("decode demuxed live AUs: %d bytes, %v; %s", len(pixels), err, stderr.String())
	}
}

func TestFFmpegWindowsLiveFourFrameBudget(t *testing.T) {
	fixture := startLiveWindowsEncoder(t, 4)
	submitted := make(chan time.Time, 4)
	writerDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(time.Second / 4)
		defer ticker.Stop()
		for frame := 0; frame < cap(fixture.timestamps); frame++ {
			if frame != 0 {
				select {
				case <-ticker.C:
				case <-fixture.ctx.Done():
					writerDone <- fixture.ctx.Err()
					return
				}
			}
			pts := time.Now()
			submitted <- pts
			if err := fixture.submit(pts); err != nil {
				writerDone <- err
				return
			}
		}
		writerDone <- nil
	}()
	var units []VideoAccessUnit
	for frame := 0; frame < 4; frame++ {
		unit := fixture.read(t, 2*time.Second)
		pts := <-submitted
		if unit.PTS != pts {
			t.Fatalf("AU %d source PTS = %v, want %v", frame, unit.PTS, pts)
		}
		units = append(units, unit)
	}
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
	fixture.decode(t, units)
	t.Log("four raw frames produced four independently decoded AUs at 640x480/4 fps; no fifth frame or stdin EOF was supplied")
}

func TestFFmpegWindowsLowFPSImmediatePacket(t *testing.T) {
	fixture := startLiveWindowsEncoder(t, 1)
	var units []VideoAccessUnit
	var next time.Time
	for frame := 0; frame < 3; frame++ {
		if !next.IsZero() {
			timer := time.NewTimer(time.Until(next))
			select {
			case <-timer.C:
			case <-fixture.ctx.Done():
				timer.Stop()
				t.Fatal(fixture.ctx.Err())
			}
		}
		pts := time.Now()
		next = pts.Add(time.Second)
		written := make(chan error, 1)
		go func() { written <- fixture.submit(pts) }()
		unit := fixture.read(t, 500*time.Millisecond)
		if err := <-written; err != nil {
			t.Fatal(err)
		}
		age := time.Since(pts)
		if unit.PTS != pts || age >= 500*time.Millisecond {
			t.Fatalf("1 fps AU %d arrived after %v with source PTS %v, want unchanged %v", frame, age, unit.PTS, pts)
		}
		t.Logf("1 fps AU %d arrived %v after submission, before the next source frame", frame, age)
		units = append(units, unit)
	}
	fixture.decode(t, units)
}

func TestWindowsNativeHiddenCursorRequestIsRejected(t *testing.T) {
	if err := validateWindowsCaptureCursor(false, false); err != ErrHiddenCursorUnsupported {
		t.Fatalf("native hidden cursor request = %v", err)
	}
	for _, test := range [][2]bool{{true, false}, {true, true}, {false, true}} {
		if err := validateWindowsCaptureCursor(test[0], test[1]); err != nil {
			t.Fatal(fmt.Errorf("cursor=%t synthetic=%t: %w", test[0], test[1], err))
		}
	}
}
