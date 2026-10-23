package airplay

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestWindowsPrepareRejectsHiddenCursorBeforeRuntime(t *testing.T) {
	_, err := PrepareCapture(context.Background(), CaptureConfig{ShowCursor: false})
	if !errors.Is(err, ErrHiddenCursorUnsupported) {
		t.Fatalf("hidden cursor preparation = %v", err)
	}
	_, err = startPlatformCapture(context.Background(), context.Background(), CaptureConfig{ShowCursor: false}, false, nil)
	if !errors.Is(err, ErrHiddenCursorUnsupported) {
		t.Fatalf("hidden cursor startup = %v", err)
	}
}

func TestWindowsBundledSyntheticCapture(t *testing.T) {
	if os.Getenv("AIRCAST_TEST_BUNDLED_FFMPEG") != "1" {
		t.Skip("opt-in Windows runtime test; place ffmpeg.exe beside this test executable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	preparation, err := PrepareTestCapture(ctx, CaptureConfig{Quality: "quality", FPS: 30, HWAccel: "none"})
	if err != nil {
		t.Fatal(err)
	}
	defer preparation.Close()
	capture, err := preparation.Start(1280, 720)
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Stop()
	if capture.Backend() != "synthetic" {
		t.Fatalf("test capture opened a display backend: %q", capture.Backend())
	}
	var previous time.Time
	for range 20 {
		unit, err := capture.ReadVideoAccessUnit()
		if err != nil {
			t.Fatal(err)
		}
		if len(unit.AnnexB) == 0 || unit.PTS.IsZero() || (!previous.IsZero() && !unit.PTS.After(previous)) {
			t.Fatalf("invalid synthetic access unit: %d bytes at %v after %v", len(unit.AnnexB), unit.PTS, previous)
		}
		previous = unit.PTS
	}
	started := time.Now()
	capture.Stop()
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("synthetic capture cancellation took %v", elapsed)
	}
}
