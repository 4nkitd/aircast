package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"strconv"
	"testing"
	"time"
)

func floatTone(rate, samples int, hz, amplitude float64) []byte {
	data := make([]byte, samples*8)
	for i := 0; i < samples; i++ {
		value := float32(amplitude * math.Sin(2*math.Pi*hz*float64(i)/float64(rate)))
		binary.LittleEndian.PutUint32(data[i*8:], math.Float32bits(value))
		binary.LittleEndian.PutUint32(data[i*8+4:], math.Float32bits(-value))
	}
	return data
}

func convertPackets(t *testing.T, rate int, data []byte, chunk int) []pcmFrame {
	t.Helper()
	var frames []pcmFrame
	converter, err := newConverter(testFormat(rate, 2, 32, true), 352, func(frame pcmFrame) {
		frames = append(frames, frame)
	})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Unix(1700000000, 0)
	for offset := 0; offset < len(data)/8; offset += chunk {
		count := min(chunk, len(data)/8-offset)
		if err := converter.push(sourcePacket{data: data[offset*8 : (offset+count)*8], frames: count,
			position: uint64(rate + offset), pts: base.Add(sampleDuration(uint64(offset), rate))}); err != nil {
			t.Fatal(err)
		}
	}
	return frames
}

func TestResamplingIsPacketIndependentAndTimestamped(t *testing.T) {
	for _, rate := range []int{8000, 32000, 44100, 48000, 96000, 192000} {
		t.Run(stringRate(rate), func(t *testing.T) {
			data := floatTone(rate, rate/8, 1000, 0.5)
			whole := convertPackets(t, rate, data, rate)
			fragmented := convertPackets(t, rate, data, 73)
			if len(whole) != len(fragmented) || len(whole) < 10 {
				t.Fatalf("output frames = %d / %d", len(whole), len(fragmented))
			}
			base := time.Unix(1700000000, 0)
			for i := range whole {
				if !bytes.Equal(whole[i].pcm, fragmented[i].pcm) {
					t.Fatalf("frame %d changes with source packet boundaries", i)
				}
				wantSample := uint64(SampleRate + i*352)
				wantPTS := base.Add(sampleDuration(uint64(i*352), SampleRate))
				if whole[i].position.Sample != wantSample || fragmented[i].position.Sample != wantSample {
					t.Fatalf("frame %d source position = %d / %d, want %d", i, whole[i].position.Sample, fragmented[i].position.Sample, wantSample)
				}
				for _, frame := range []pcmFrame{whole[i], fragmented[i]} {
					delta := frame.position.PTS.Sub(wantPTS)
					if delta < -2*time.Nanosecond || delta > 2*time.Nanosecond {
						t.Fatalf("frame %d PTS error = %s", i, delta)
					}
				}
			}
		})
	}
}

func stringRate(rate int) string {
	return strconv.Itoa(rate)
}

func rmsFrames(frames []pcmFrame) float64 {
	var energy float64
	count := 0
	for _, frame := range frames[1:] {
		for offset := 0; offset < len(frame.pcm); offset += frameBytes {
			value := float64(int16(binary.LittleEndian.Uint16(frame.pcm[offset:])))
			energy += value * value
			count++
		}
	}
	return math.Sqrt(energy / float64(count))
}

func TestResamplingPassbandAndAntialias(t *testing.T) {
	for _, rate := range []int{48000, 96000, 192000} {
		t.Run(stringRate(rate), func(t *testing.T) {
			pass := convertPackets(t, rate, floatTone(rate, rate/4, 1000, 0.5), 480)
			stop := convertPackets(t, rate, floatTone(rate, rate/4, 24000-500, 0.5), 480)
			want := 16384 / math.Sqrt2
			if got := rmsFrames(pass); math.Abs(got/want-1) > 0.002 {
				t.Fatalf("1 kHz passband RMS = %f, want %f", got, want)
			}
			for _, hz := range []float64{10000, 18000, 20000} {
				frames := convertPackets(t, rate, floatTone(rate, rate/4, hz, 0.5), 480)
				if got := rmsFrames(frames); math.Abs(got/want-1) > 0.005 {
					t.Fatalf("%.0f Hz passband RMS = %f, want %f", hz, got, want)
				}
			}
			if attenuation := rmsFrames(stop) / rmsFrames(pass); attenuation > 0.0032 {
				t.Fatalf("23.5 kHz alias attenuation = %.1f dB, want at least 50 dB", 20*math.Log10(attenuation))
			}
		})
	}
}

func TestSilentPacketsAndDiscontinuities(t *testing.T) {
	var frames []pcmFrame
	c, err := newConverter(testFormat(44100, 2, 16, false), 352, func(frame pcmFrame) { frames = append(frames, frame) })
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	if err := c.push(sourcePacket{frames: 100, position: 1000, pts: base, silent: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.push(sourcePacket{frames: 352, position: 4000, pts: base.Add(time.Second), silent: true, discontinuity: true}); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 || frames[0].position.Sample != 4000 || !frames[0].position.PTS.Equal(base.Add(time.Second)) {
		t.Fatalf("partial frame crossed a discontinuity: %+v", frames)
	}
	if !bytes.Equal(frames[0].pcm, make([]byte, 352*frameBytes)) {
		t.Fatal("real silent packet contained nonzero samples")
	}
	if err := c.push(sourcePacket{frames: 352, position: 8000, pts: base.Add(2 * time.Second), silent: true}); err != nil {
		t.Fatal(err)
	}
	if frames[1].position.Sample != 8000 {
		t.Fatal("device-position gap was concealed")
	}
}

func TestConverterRejectsMalformedPackets(t *testing.T) {
	c, err := newConverter(testFormat(48000, 2, 32, true), 352, func(pcmFrame) {})
	if err != nil {
		t.Fatal(err)
	}
	for _, packet := range []sourcePacket{
		{frames: -1, silent: true}, {frames: 0, silent: true},
		{frames: maxPacketBytes, silent: true}, {frames: 2, data: make([]byte, 8), pts: time.Now()},
		{frames: 1, silent: true}, {frames: 2, position: math.MaxUint64 - 1, silent: true, pts: time.Now()},
	} {
		if err := c.push(packet); err == nil {
			t.Errorf("accepted malformed packet: %+v", packet)
		}
	}
}

func TestResamplerUsesNewDeviceTimestamps(t *testing.T) {
	var frames []pcmFrame
	c, err := newConverter(testFormat(48000, 2, 32, true), 352, func(frame pcmFrame) { frames = append(frames, frame) })
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	for i := 0; i < 20; i++ {
		pts := base.Add(time.Duration(i) * 10 * time.Millisecond)
		if i >= 10 {
			pts = pts.Add(time.Millisecond)
		}
		if err := c.push(sourcePacket{frames: 480, position: uint64(i * 480), pts: pts, silent: true}); err != nil {
			t.Fatal(err)
		}
	}
	last := frames[len(frames)-1]
	want := base.Add(sampleDuration(last.position.Sample, SampleRate)).Add(time.Millisecond)
	if delta := last.position.PTS.Sub(want); delta < -2*time.Nanosecond || delta > 2*time.Nanosecond {
		t.Fatalf("QPC timestamp correction was lost: %s", delta)
	}
}
