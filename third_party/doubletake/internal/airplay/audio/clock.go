package audio

import (
	"fmt"
	"math"
	"time"
)

type captureClock struct {
	qpc            uint64
	pts            time.Time
	rate           int
	expected       uint64
	devicePosition uint64
	devicePTS      time.Time
}

func (c *captureClock) stamp(packet sourcePacket, qpc uint64, timestampValid bool, received time.Time) (sourcePacket, error) {
	if packet.frames < 1 || packet.frames > c.rate/4 {
		return sourcePacket{}, fmt.Errorf("WASAPI loopback returned an invalid packet length")
	}
	if timestampValid && qpc != 0 {
		delta, ok := qpcDuration(qpc, c.qpc)
		if !ok {
			return sourcePacket{}, fmt.Errorf("WASAPI loopback returned an invalid device timestamp")
		}
		packet.pts = c.pts.Add(delta)
		age := received.Sub(packet.pts)
		if age < -time.Second || age > 5*time.Second {
			return sourcePacket{}, fmt.Errorf("WASAPI loopback device timestamp is outside its capture window: %s", age)
		}
		c.devicePosition, c.devicePTS = packet.position, packet.pts
	} else {
		if packet.discontinuity {
			return sourcePacket{}, fmt.Errorf("WASAPI loopback discontinuity has no valid device timestamp")
		}
		packet.position = c.expected
		if c.devicePTS.IsZero() {
			c.devicePosition = packet.position
			c.devicePTS = received.Add(-sampleDuration(uint64(packet.frames), c.rate))
		}
		if packet.position < c.devicePosition {
			return sourcePacket{}, fmt.Errorf("WASAPI loopback lost its device-clock position")
		}
		packet.pts = c.devicePTS.Add(sampleDuration(packet.position-c.devicePosition, c.rate))
	}
	if packet.position > math.MaxUint64-uint64(packet.frames) {
		return sourcePacket{}, fmt.Errorf("WASAPI loopback device position overflow")
	}
	c.expected = packet.position + uint64(packet.frames)
	return packet, nil
}

func qpcDuration(value, anchor uint64) (time.Duration, bool) {
	if value >= anchor {
		if value-anchor > math.MaxInt64/100 {
			return 0, false
		}
		return time.Duration(value-anchor) * 100, true
	}
	if anchor-value > math.MaxInt64/100 {
		return 0, false
	}
	return -time.Duration(anchor-value) * 100, true
}
