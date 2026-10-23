package airplay

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWindowsAudioLoopbackRequiresExplicitOptIn(t *testing.T) {
	t.Setenv("AIRCAST_EXPERIMENTAL_WASAPI_LOOPBACK", "")
	capture, err := StartAudioCapture(context.Background(), false, AudioCodecALAC)
	if err == nil || capture != nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("unverified loopback started without opt-in: capture=%v, err=%v", capture, err)
	}
}

func TestWindowsAudioTestToneWithoutGStreamer(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("AIRCAST_EXPERIMENTAL_WASAPI_LOOPBACK", "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	capture, err := StartAudioCapture(ctx, true, AudioCodecALAC)
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Stop()
	buffer := make([]byte, 8192)
	for i := 0; i < 2; i++ {
		n, position, err := capture.readFramePosition(buffer)
		if err != nil || n <= 0 || position.PTS.IsZero() || !position.HasSourceRTP || position.SourceRTP != uint32(i*352) {
			t.Fatalf("encoded test tone = %d, %+v, %v", n, position, err)
		}
	}
	cancel()
	if _, _, err := capture.ReadFrameAt(buffer); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled test tone read = %v", err)
	}
}

func TestWindowsAudioRejectsUnknownCodec(t *testing.T) {
	if _, err := StartAudioCapture(context.Background(), true, AudioCodec(99)); err == nil {
		t.Fatal("unknown codec was accepted")
	}
}
