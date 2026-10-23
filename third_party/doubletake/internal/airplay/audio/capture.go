package audio

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"time"
)

const (
	SampleRate     = 44100
	frameBytes     = 4
	maxPacketBytes = 4 * 1024 * 1024
)

type Position struct {
	PTS    time.Time
	Sample uint64
}

type pcmFrame struct {
	pcm      []byte
	position Position
}

type Capture struct {
	ctx          context.Context
	cancel       context.CancelFunc
	frames       chan pcmFrame
	done         chan struct{}
	err          error
	frameSamples int
}

func newCapture(ctx context.Context, frameSamples int) (*Capture, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if frameSamples < 1 || frameSamples > 4096 {
		return nil, fmt.Errorf("audio frame must contain 1..4096 samples")
	}
	ctx, cancel := context.WithCancel(ctx)
	capacity := min(64, max(1, SampleRate/4/frameSamples))
	return &Capture{ctx: ctx, cancel: cancel, frames: make(chan pcmFrame, capacity),
		done: make(chan struct{}), frameSamples: frameSamples}, nil
}

func (c *Capture) Close() {
	c.cancel()
}

func (c *Capture) Wait() error {
	<-c.done
	return c.err
}

func (c *Capture) finish(err error) {
	c.err = err
	close(c.done)
}

func (c *Capture) emit(frame pcmFrame) {
	select {
	case c.frames <- frame:
		return
	default:
	}
	// Capture must keep servicing the device even when the sender stalls.
	select {
	case <-c.frames:
	default:
	}
	select {
	case c.frames <- frame:
	default:
	}
}

func (c *Capture) ReadFrame(dst []byte) (Position, error) {
	if len(dst) != c.frameSamples*frameBytes {
		return Position{}, fmt.Errorf("audio PCM destination is %d bytes, want %d", len(dst), c.frameSamples*frameBytes)
	}
	if err := c.ctx.Err(); err != nil {
		return Position{}, err
	}
	select {
	case <-c.ctx.Done():
		return Position{}, c.ctx.Err()
	case <-c.done:
		if c.err != nil {
			return Position{}, c.err
		}
		return Position{}, io.EOF
	case frame := <-c.frames:
		if err := c.ctx.Err(); err != nil {
			return Position{}, err
		}
		copy(dst, frame.pcm)
		return frame.position, nil
	}
}

func StartTestTone(ctx context.Context, frameSamples int) (*Capture, error) {
	c, err := newCapture(ctx, frameSamples)
	if err != nil {
		return nil, err
	}
	go func() {
		defer func() { c.finish(c.ctx.Err()) }()
		base := time.Now()
		var sample uint64
		timer := time.NewTimer(time.Hour)
		defer timer.Stop()
		for {
			pcm := make([]byte, frameSamples*frameBytes)
			for i := 0; i < frameSamples; i++ {
				phase := float64((sample+uint64(i))%SampleRate) * (2 * math.Pi * 440 / SampleRate)
				value := uint16(int16(math.Round(math.Sin(phase) * 8192)))
				binary.LittleEndian.PutUint16(pcm[i*frameBytes:], value)
				binary.LittleEndian.PutUint16(pcm[i*frameBytes+2:], value)
			}
			pts := base.Add(sampleDuration(sample, SampleRate))
			end := base.Add(sampleDuration(sample+uint64(frameSamples), SampleRate))
			timer.Reset(max(0, time.Until(end)))
			select {
			case <-c.ctx.Done():
				return
			case <-timer.C:
			}
			c.emit(pcmFrame{pcm: pcm, position: Position{PTS: pts, Sample: sample}})
			sample += uint64(frameSamples)
		}
	}()
	return c, nil
}

func sampleDuration(samples uint64, rate int) time.Duration {
	return time.Duration(samples/uint64(rate))*time.Second +
		time.Duration((samples%uint64(rate))*uint64(time.Second)/uint64(rate))
}
