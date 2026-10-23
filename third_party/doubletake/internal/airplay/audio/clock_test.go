package audio

import (
	"math"
	"testing"
	"time"
)

func TestDeviceClockUsesQPCAndCountsThroughInvalidTimestamps(t *testing.T) {
	base := time.Now()
	clock := captureClock{qpc: 10000000, pts: base, rate: 48000}
	packet, err := clock.stamp(sourcePacket{frames: 480, position: 4800}, 11000000, true, base.Add(120*time.Millisecond))
	if err != nil || packet.position != 4800 || !packet.pts.Equal(base.Add(100*time.Millisecond)) {
		t.Fatalf("device clock packet = %+v, %v", packet, err)
	}
	packet, err = clock.stamp(sourcePacket{frames: 480, position: math.MaxUint64}, 0, false, base.Add(900*time.Millisecond))
	if err != nil || packet.position != 5280 || !packet.pts.Equal(base.Add(110*time.Millisecond)) {
		t.Fatalf("invalid QPC fallback used read time or invalid position: %+v, %v", packet, err)
	}
	packet, err = clock.stamp(sourcePacket{frames: 480, position: 5760}, 11200100, true, base.Add(140*time.Millisecond))
	if err != nil || packet.position != 5760 || !packet.pts.Equal(base.Add(120010*time.Microsecond)) {
		t.Fatalf("device clock did not recover QPC mapping: %+v, %v", packet, err)
	}
}

func TestInitialTimestampFallbackDoesNotUseInvalidDevicePosition(t *testing.T) {
	base := time.Now()
	clock := captureClock{qpc: 10000000, pts: base, rate: 48000}
	packet, err := clock.stamp(sourcePacket{frames: 480, position: math.MaxUint64}, 0, false, base)
	if err != nil || packet.position != 0 || !packet.pts.Equal(base.Add(-10*time.Millisecond)) {
		t.Fatalf("initial fallback = %+v, %v", packet, err)
	}
}

func TestDeviceClockRejectsUnrecoverableTimestamps(t *testing.T) {
	base := time.Now()
	tests := []struct {
		packet sourcePacket
		qpc    uint64
		valid  bool
	}{
		{sourcePacket{frames: 480, discontinuity: true}, 0, false},
		{sourcePacket{frames: 480, position: math.MaxUint64}, 10000000, true},
		{sourcePacket{frames: 480}, math.MaxUint64, true},
		{sourcePacket{frames: 480}, 10000000 + 20000000, true},
		{sourcePacket{frames: 0}, 10000000, true},
	}
	for _, test := range tests {
		clock := captureClock{qpc: 10000000, pts: base, rate: 48000}
		if _, err := clock.stamp(test.packet, test.qpc, test.valid, base); err == nil {
			t.Errorf("accepted invalid clock packet: %+v", test)
		}
	}
	clock := captureClock{qpc: 10000000, pts: base, rate: 48000}
	if _, err := clock.stamp(sourcePacket{frames: 480}, 10000000, true, base.Add(6*time.Second)); err == nil {
		t.Fatal("accepted a stale device timestamp")
	}
}
