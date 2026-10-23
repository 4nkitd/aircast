package audio

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

const (
	baseResampleTaps = 192
	maxResampleTaps  = baseResampleTaps * 4
	resamplePhases   = 256
)

type sourcePacket struct {
	data          []byte
	frames        int
	position      uint64
	pts           time.Time
	silent        bool
	discontinuity bool
}

type converter struct {
	decoder      *sampleDecoder
	frameSamples int
	emit         func(pcmFrame)
	filter       []float64
	taps         int
	ring         [maxResampleTaps * 2]stereoSample
	first        stereoSample
	started      bool
	expected     uint64
	baseOutput   uint64
	inputCount   uint64
	outputCount  uint64
	pending      []byte
	filled       int
	position     Position
}

func newConverter(format mixFormat, frameSamples int, emit func(pcmFrame)) (*converter, error) {
	decoder, err := newSampleDecoder(format)
	if err != nil {
		return nil, err
	}
	if frameSamples < 1 || frameSamples > 4096 || emit == nil {
		return nil, fmt.Errorf("invalid audio converter frame size or destination")
	}
	c := &converter{decoder: decoder, frameSamples: frameSamples, emit: emit}
	if format.rate != SampleRate {
		c.taps = baseResampleTaps * max(1, (format.rate+47999)/48000)
		c.filter = make([]float64, (resamplePhases+1)*c.taps)
		half := float64(c.taps / 2)
		cutoff := 0.96 * min(1, float64(SampleRate)/float64(format.rate))
		for phase := 0; phase <= resamplePhases; phase++ {
			row := c.filter[phase*c.taps : (phase+1)*c.taps]
			fraction := float64(phase) / resamplePhases
			var sum float64
			for tap := range row {
				distance := float64(tap) - half + 1 - fraction
				x := math.Pi * cutoff * distance
				sinc := 1.0
				if math.Abs(x) > 1e-12 {
					sinc = math.Sin(x) / x
				}
				window := 0.42 + 0.5*math.Cos(math.Pi*distance/half) +
					0.08*math.Cos(2*math.Pi*distance/half)
				coefficient := cutoff * sinc * window
				row[tap] = coefficient
				sum += coefficient
			}
			for tap := range row {
				row[tap] /= sum
			}
		}
	}
	return c, nil
}

func (c *converter) push(packet sourcePacket) error {
	format := c.decoder.format
	if packet.frames < 1 || packet.frames > format.rate/4 || packet.frames > maxPacketBytes/format.blockAlign ||
		packet.position > math.MaxUint64-uint64(packet.frames) {
		return fmt.Errorf("WASAPI audio packet exceeds its bounded frame count")
	}
	if packet.pts.IsZero() {
		return fmt.Errorf("WASAPI audio packet has no capture timestamp")
	}
	if !packet.silent && len(packet.data) != packet.frames*format.blockAlign {
		return fmt.Errorf("truncated WASAPI audio packet")
	}
	if !c.started || packet.discontinuity || packet.position != c.expected {
		c.started = true
		c.inputCount, c.outputCount = 0, 0
		c.filled, c.pending = 0, nil
		c.baseOutput = scalePosition(packet.position, format.rate)
	}
	packetStart := c.inputCount
	c.expected = packet.position + uint64(packet.frames)
	for i := 0; i < packet.frames; i++ {
		var sample stereoSample
		if !packet.silent {
			sample = c.decoder.decode(packet.data[i*format.blockAlign:])
		}
		if format.rate == SampleRate {
			c.write(sample, packet.pts.Add(sampleDuration(uint64(i), format.rate)))
			c.inputCount++
			continue
		}
		if c.inputCount == 0 {
			c.first = sample
		}
		c.ring[c.inputCount%uint64(len(c.ring))] = sample
		c.inputCount++
		for {
			center := c.outputCount/SampleRate*uint64(format.rate) +
				(c.outputCount%SampleRate)*uint64(format.rate)/SampleRate
			if center+uint64(c.taps/2) >= c.inputCount {
				break
			}
			remainder := (c.outputCount % SampleRate) * uint64(format.rate) % SampleRate
			phase := float64(remainder) * resamplePhases / SampleRate
			index := int(phase)
			blend := phase - float64(index)
			row := c.filter[index*c.taps : (index+1)*c.taps]
			next := c.filter[(index+1)*c.taps : (index+2)*c.taps]
			var output stereoSample
			for tap := 0; tap < c.taps; tap++ {
				input := c.first
				at := int64(center) + int64(tap-c.taps/2+1)
				if at >= 0 {
					input = c.ring[uint64(at)%uint64(len(c.ring))]
				}
				weight := row[tap] + (next[tap]-row[tap])*blend
				output[0] += input[0] * weight
				output[1] += input[1] * weight
			}
			// Use the packet's device/QPC mapping, not its read completion time.
			offset := float64(int64(center)-int64(packetStart)) + float64(remainder)/SampleRate
			pts := packet.pts.Add(time.Duration(math.Round(offset * float64(time.Second) / float64(format.rate))))
			c.write(output, pts)
		}
	}
	return nil
}

func (c *converter) write(sample stereoSample, pts time.Time) {
	if c.filled == 0 {
		c.pending = make([]byte, c.frameSamples*frameBytes)
		c.position = Position{PTS: pts, Sample: c.baseOutput + c.outputCount}
	}
	offset := c.filled * frameBytes
	binary.LittleEndian.PutUint16(c.pending[offset:], pcm16(sample[0]))
	binary.LittleEndian.PutUint16(c.pending[offset+2:], pcm16(sample[1]))
	c.filled++
	c.outputCount++
	if c.filled == c.frameSamples {
		c.emit(pcmFrame{pcm: c.pending, position: c.position})
		c.pending, c.filled = nil, 0
	}
}

func scalePosition(source uint64, rate int) uint64 {
	return source/uint64(rate)*SampleRate + source%uint64(rate)*SampleRate/uint64(rate)
}
