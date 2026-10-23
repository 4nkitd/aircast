package audio

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBlockedReadCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c, err := newCapture(ctx, 352)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := c.ReadFrame(make([]byte, 352*frameBytes))
		result <- err
	}()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked read returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not unblock the PCM reader")
	}
	c.finish(ctx.Err())
	if !errors.Is(c.Wait(), context.Canceled) {
		t.Fatal("capture lost its cancellation error")
	}
}

func TestCaptureQueueIsBoundedAndKeepsLatestSamples(t *testing.T) {
	c, err := newCapture(context.Background(), 352)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for i := 0; i < 1000; i++ {
		c.emit(pcmFrame{pcm: make([]byte, 352*frameBytes), position: Position{Sample: uint64(i)}})
	}
	if len(c.frames) != cap(c.frames) || cap(c.frames) > 32 {
		t.Fatalf("unbounded capture queue: %d / %d", len(c.frames), cap(c.frames))
	}
	position, err := c.ReadFrame(make([]byte, 352*frameBytes))
	if err != nil || position.Sample != 1000-uint64(cap(c.frames)) {
		t.Fatalf("oldest retained position = %d, err %v", position.Sample, err)
	}
}

func TestCapturePropagatesTerminalFailure(t *testing.T) {
	c, err := newCapture(context.Background(), 352)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	failure := errors.New("default output unavailable")
	c.finish(failure)
	_, err = c.ReadFrame(make([]byte, 352*frameBytes))
	if !errors.Is(err, failure) || !errors.Is(c.Wait(), failure) {
		t.Fatalf("terminal failure was concealed: %v", err)
	}
}

func TestTestToneWithoutDevice(t *testing.T) {
	for _, spf := range []int{352, 480} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		c, err := StartTestTone(ctx, spf)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		pcm := make([]byte, spf*frameBytes)
		var previous Position
		for frame := 0; frame < 3; frame++ {
			position, err := c.ReadFrame(pcm)
			if err != nil {
				t.Fatal(err)
			}
			if position.PTS.IsZero() || position.Sample != uint64(frame*spf) {
				t.Fatalf("test tone frame position = %+v", position)
			}
			if frame > 0 && position.PTS.Sub(previous.PTS) < sampleDuration(uint64(spf), SampleRate)-time.Nanosecond {
				t.Fatal("test tone timestamp did not advance at sample cadence")
			}
			nonzero := false
			for offset := 0; offset < len(pcm); offset += frameBytes {
				left := int16(binary.LittleEndian.Uint16(pcm[offset:]))
				right := int16(binary.LittleEndian.Uint16(pcm[offset+2:]))
				if left != right {
					t.Fatal("test tone is not dual mono")
				}
				nonzero = nonzero || left != 0
			}
			if !nonzero {
				t.Fatal("test tone contains only silence")
			}
			previous = position
		}
		var closers sync.WaitGroup
		for i := 0; i < 10; i++ {
			closers.Add(1)
			go func() { defer closers.Done(); c.Close() }()
		}
		closers.Wait()
		if !errors.Is(c.Wait(), context.Canceled) {
			t.Fatal("test tone did not stop cleanly")
		}
		cancel()
	}
}

func TestCaptureValidationAndCancelledStart(t *testing.T) {
	for _, size := range []int{0, -1, 4097} {
		if _, err := StartTestTone(context.Background(), size); err == nil {
			t.Fatalf("accepted frame size %d", size)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := StartTestTone(ctx, 352); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled start returned %v", err)
	}
	if _, err := StartLoopback(ctx, 352); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled loopback start returned %v", err)
	}
	c, err := newCapture(context.Background(), 352)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.ReadFrame(make([]byte, 3)); err == nil || !strings.Contains(err.Error(), "destination") {
		t.Fatalf("invalid destination returned %v", err)
	}
}

func TestQPCMappingAndOverflow(t *testing.T) {
	for _, test := range []struct {
		value, anchor uint64
		want          time.Duration
		ok            bool
	}{
		{10000001, 10000000, 100 * time.Nanosecond, true},
		{10000000, 10000001, -100 * time.Nanosecond, true},
		{0, math.MaxUint64, 0, false}, {math.MaxUint64, 0, 0, false},
		{10000000 + uint64(math.MaxInt64/100), 10000000, time.Duration(math.MaxInt64/100) * 100, true},
	} {
		got, ok := qpcDuration(test.value, test.anchor)
		if got != test.want || ok != test.ok {
			t.Fatalf("QPC mapping %d / %d = %s %v, want %s %v", test.value, test.anchor, got, ok, test.want, test.ok)
		}
	}
}
