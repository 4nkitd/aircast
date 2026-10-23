package airplay

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"doubletake/internal/airplay/capturewin"
)

type windowsCapturePreparation struct {
	ffmpeg  string
	monitor capturewin.Monitor
}

func windowsBundledFFmpeg() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate capture engine executable: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("resolve capture engine executable: %w", err)
	}
	path := filepath.Join(filepath.Dir(executable), "ffmpeg.exe")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("bundled ffmpeg.exe is missing beside the capture engine at %s", path)
	}
	return path, nil
}

func preparePlatformCapture(ctx context.Context, cfg CaptureConfig, synthetic bool) (*CapturePreparation, bool, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	if err := validateWindowsCaptureCursor(cfg.ShowCursor, synthetic); err != nil {
		return nil, true, err
	}
	if cfg.X11WindowID != 0 || cfg.X11WindowName != "" {
		return nil, true, fmt.Errorf("X11 window capture is unavailable on Windows; capture the primary monitor")
	}
	if cfg.VideoCodec == VideoCodecHEVC {
		return nil, true, fmt.Errorf("Windows capture currently supports SDR H.264 only; HEVC is not enabled")
	}
	prepared := windowsCapturePreparation{}
	var err error
	prepared.ffmpeg, err = windowsBundledFFmpeg()
	if err != nil {
		return nil, true, err
	}
	kind := capturePreparationWindows
	width, height := testCaptureWidth, testCaptureHeight
	if synthetic {
		kind = capturePreparationWindowsTest
	} else {
		prepared.monitor, err = capturewin.Primary()
		if err != nil {
			return nil, true, err
		}
		width, height = prepared.monitor.Width, prepared.monitor.Height
	}
	validation := cfg
	if validation.VideoCodec == VideoCodecAuto {
		validation.VideoCodec = VideoCodecH264
	}
	if _, _, err := windowsFFmpegVideoArgs(validation, width, height); err != nil {
		return nil, true, err
	}
	return &CapturePreparation{ctx: ctx, cfg: cfg, kind: kind, timestampedOutput: true, platform: prepared}, true, nil
}

type windowsRawFrame struct {
	pixels []byte
	pts    time.Time
}

func startPlatformCapture(startupCtx, lifetimeCtx context.Context, cfg CaptureConfig, synthetic bool, platform any) (*ScreenCapture, error) {
	if err := validateWindowsCaptureCursor(cfg.ShowCursor, synthetic); err != nil {
		return nil, err
	}
	prepared, ok := platform.(windowsCapturePreparation)
	if !ok {
		return nil, fmt.Errorf("Windows capture preparation is missing")
	}
	startupCtx, cancelStartup := context.WithTimeout(normalizeContext(startupCtx), captureStartupBudget)
	defer cancelStartup()
	if err := startupCtx.Err(); err != nil {
		return nil, err
	}
	width, height := prepared.monitor.Width, prepared.monitor.Height
	if synthetic {
		width, height = testCaptureWidth, testCaptureHeight
	}
	args, canvas, err := windowsFFmpegVideoArgs(cfg, width, height)
	if err != nil {
		return nil, err
	}
	if int64(width)*int64(height) > 32<<20 {
		return nil, fmt.Errorf("primary monitor exceeds the bounded Windows capture pixel pool")
	}
	ctx, cancel := context.WithCancel(normalizeContext(lifetimeCtx))
	cmd := exec.CommandContext(ctx, prepared.ffmpeg, args...)
	cmd.Dir = filepath.Dir(prepared.ffmpeg)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("FFmpeg screen input pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = stdin.Close()
		return nil, fmt.Errorf("FFmpeg screen output pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("FFmpeg screen diagnostic pipe: %w", err)
	}
	waitResult, err := startGStreamerCommand(cmd)
	if err != nil {
		cancel()
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start bundled FFmpeg: %w", err)
	}
	stderrTail := newCaptureStderrTail()
	go logStderr("SCREEN-FFMPEG", stderr, stderrTail)
	raw := make(chan windowsRawFrame, 1)
	backend := make(chan string, 1)
	pool := make(chan []byte, 3)
	for range cap(pool) {
		pool <- make([]byte, width*height*4)
	}
	timestamps := make(chan time.Time, 4)
	var workers sync.WaitGroup
	var sourceError error
	var errorMu sync.Mutex
	fail := func(err error) {
		errorMu.Lock()
		if sourceError == nil && ctx.Err() == nil {
			sourceError = err
			stderrTail.append(err.Error())
		}
		errorMu.Unlock()
		cancel()
	}
	workers.Add(2)
	go func() {
		defer workers.Done()
		defer close(raw)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		var native *capturewin.Capture
		if synthetic {
			backend <- "synthetic"
		} else {
			var openErr error
			native, openErr = capturewin.Open(prepared.monitor, cfg.ShowCursor)
			if openErr != nil {
				fail(openErr)
				return
			}
			defer native.Close()
			if native.GDI() {
				backend <- "gdi"
				log.Printf("[CAPTURE] DXGI Desktop Duplication could not initialize; using GDI compatibility fallback, not the native quality path")
			} else {
				backend <- "dxgi"
				log.Printf("[CAPTURE] using DXGI Desktop Duplication with CPU readback on primary adapter %d/output %d", prepared.monitor.AdapterIndex, prepared.monitor.OutputIndex)
			}
		}
		fps := cfg.FPS
		if fps <= 0 {
			fps = 30
		}
		ticker := time.NewTicker(time.Second / time.Duration(fps))
		defer ticker.Stop()
		lastMetadata := time.Now()
		var frameID uint64
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if native != nil && time.Since(lastMetadata) >= time.Second {
				current, metadataErr := capturewin.Primary()
				if metadataErr != nil {
					fail(metadataErr)
					return
				}
				if current != prepared.monitor {
					fail(fmt.Errorf("primary monitor topology, size, or SDR state changed; stop and start sharing again"))
					return
				}
				lastMetadata = time.Now()
			}
			var pixels []byte
			select {
			case pixels = <-pool:
			default:
				continue
			}
			pts := time.Now()
			if synthetic {
				renderWindowsTestFrame(pixels, width, height, frameID)
				frameID++
			} else {
				ready, frameErr := native.Frame(pixels)
				if frameErr != nil {
					pool <- pixels
					fail(frameErr)
					return
				}
				if !ready {
					pool <- pixels
					continue
				}
			}
			frame := windowsRawFrame{pixels: pixels, pts: pts}
			select {
			case raw <- frame:
			default:
				select {
				case stale := <-raw:
					pool <- stale.pixels
				default:
				}
				select {
				case raw <- frame:
				case <-ctx.Done():
					pool <- pixels
					return
				}
			}
		}
	}()
	go func() {
		defer workers.Done()
		defer stdin.Close()
		defer close(timestamps)
		for {
			select {
			case <-ctx.Done():
				return
			case frame, ok := <-raw:
				if !ok {
					return
				}
				select {
				case timestamps <- frame.pts:
				case <-ctx.Done():
					pool <- frame.pixels
					return
				}
				_, writeErr := io.Copy(stdin, bytes.NewReader(frame.pixels))
				pool <- frame.pixels
				if writeErr != nil {
					fail(fmt.Errorf("write captured BGRA frame to FFmpeg: %w", writeErr))
					return
				}
			}
		}
	}()
	workersDone := make(chan struct{})
	go func() {
		workers.Wait()
		close(workersDone)
	}()
	capture := &ScreenCapture{
		cmd: cmd, stdout: stdout, cancel: cancel, waitCh: make(chan struct{}), stderr: stderrTail,
		frames: &aviScreenReader{reader: stdout, ctx: ctx, timestamps: timestamps},
		platformStop: func() {
			_ = stdin.Close()
			select {
			case <-workersDone:
			case <-time.After(2 * time.Second):
				log.Printf("[CAPTURE] native capture did not stop within two seconds; supervisor must terminate the engine")
			}
		},
	}
	go func() {
		waitErr := <-waitResult
		cancel()
		errorMu.Lock()
		if sourceError != nil {
			waitErr = sourceError
		}
		errorMu.Unlock()
		capture.waitErr = waitErr
		close(capture.waitCh)
	}()
	probeCtx, cancelProbe := context.WithTimeout(startupCtx, captureStartupProbeTimeout(cfg.FPS))
	err = probeVideoCaptureStartup(probeCtx, capture, VideoCodecH264, true)
	cancelProbe()
	if err != nil {
		capture.Stop()
		return nil, fmt.Errorf("Windows capture failed startup validation: %w; FFmpeg/native diagnostics: %s", err, capture.stderrText())
	}
	capture.backend = <-backend
	profile := captureQualityProfile(CaptureConfig{Quality: cfg.Quality, Bitrate: cfg.Bitrate, MaxWidth: canvas.width, MaxHeight: canvas.height})
	log.Printf("[CAPTURE] physical source %dx%d, active %dx%d at %d,%d in %dx%d; H.264 SDR BT.709 limited 4:2:0, max %d kbit/s, VBV %d ms; receiver presentation remains unverified",
		width, height, canvas.activeWidth, canvas.activeHeight, canvas.left, canvas.top, canvas.width, canvas.height, profile.maxBitrateKbps, profile.vbvMilliseconds)
	return capture, nil
}

func renderWindowsTestFrame(pixels []byte, width, height int, frame uint64) {
	bar := int(frame*8) % width
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			offset := (y*width + x) * 4
			red, green, blue := byte(24), byte(24), byte(24)
			if x%64 == 0 || y%64 == 0 || x == width-1 || y == height-1 {
				red, green, blue = 220, 220, 220
			}
			if y < height/4 {
				switch x * 6 / width {
				case 0:
					red, green, blue = 0, 0, 0
				case 1:
					red, green, blue = 255, 255, 255
				case 2:
					red, green, blue = 255, 0, 0
				case 3:
					red, green, blue = 0, 255, 0
				case 4:
					red, green, blue = 0, 0, 255
				case 5:
					red, green, blue = 128, 128, 128
				}
			}
			if y > height/3 && x >= bar && x < bar+8 {
				red, green, blue = 255, 255, 255
			}
			if y >= height-32 && x < 64*16 && frame&(uint64(1)<<uint(x/16)) != 0 {
				red, green, blue = 255, 255, 255
			}
			pixels[offset], pixels[offset+1], pixels[offset+2], pixels[offset+3] = blue, green, red, 255
		}
	}
}
