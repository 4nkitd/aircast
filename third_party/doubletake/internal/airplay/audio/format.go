package audio

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"
)

type mixFormat struct {
	rate        int
	channels    int
	bits        int
	validBits   int
	blockAlign  int
	floating    bool
	channelMask uint32
}

type stereoSample [2]float64

type sampleDecoder struct {
	format  mixFormat
	weights [8]stereoSample
}

func newSampleDecoder(format mixFormat) (*sampleDecoder, error) {
	if format.rate < 8000 || format.rate > 192000 || format.channels < 1 || format.channels > 8 {
		return nil, fmt.Errorf("unsupported WASAPI mix rate/channels: %d Hz, %d channels", format.rate, format.channels)
	}
	if format.validBits < 1 || format.validBits > format.bits ||
		format.blockAlign != format.channels*(format.bits/8) {
		return nil, fmt.Errorf("invalid WASAPI PCM alignment or valid-bit count")
	}
	if format.floating {
		if (format.bits != 32 && format.bits != 64) || format.validBits != format.bits {
			return nil, fmt.Errorf("unsupported WASAPI IEEE float format: %d/%d bits", format.validBits, format.bits)
		}
	} else if format.bits != 8 && format.bits != 16 && format.bits != 24 && format.bits != 32 {
		return nil, fmt.Errorf("unsupported WASAPI PCM format: %d bits", format.bits)
	}
	if format.bits == 8 && format.validBits != 8 {
		return nil, fmt.Errorf("unsupported WASAPI unsigned PCM format: %d valid bits", format.validBits)
	}
	decoder := &sampleDecoder{format: format}
	if format.channels == 1 && (format.channelMask == 0 || format.channelMask == 4) {
		decoder.weights[0] = stereoSample{1, 1}
		return decoder, nil
	}
	if format.channels == 2 && format.channelMask == 0 {
		decoder.weights[0][0], decoder.weights[1][1] = 1, 1
		return decoder, nil
	}
	if bits.OnesCount32(format.channelMask) != format.channels || format.channelMask & ^uint32(0x7ff) != 0 {
		return nil, fmt.Errorf("unsupported or ambiguous WASAPI speaker mask %#x for %d channels", format.channelMask, format.channels)
	}
	weights := map[uint32]stereoSample{
		0x1: {1, 0}, 0x2: {0, 1}, 0x4: {math.Sqrt2 / 2, math.Sqrt2 / 2},
		0x8: {0, 0}, 0x10: {math.Sqrt2 / 2, 0}, 0x20: {0, math.Sqrt2 / 2},
		0x40: {math.Sqrt2 / 2, 0}, 0x80: {0, math.Sqrt2 / 2}, 0x100: {0.5, 0.5},
		0x200: {math.Sqrt2 / 2, 0}, 0x400: {0, math.Sqrt2 / 2},
	}
	channel := 0
	var totals stereoSample
	for bit := uint32(1); bit <= 0x400; bit <<= 1 {
		if format.channelMask&bit == 0 {
			continue
		}
		decoder.weights[channel] = weights[bit]
		for side := range totals {
			totals[side] += decoder.weights[channel][side]
		}
		channel++
	}
	for i := 0; i < format.channels; i++ {
		for side := range totals {
			decoder.weights[i][side] /= max(1, totals[side])
		}
	}
	return decoder, nil
}

func (d *sampleDecoder) decode(frame []byte) stereoSample {
	var stereo stereoSample
	bytesPerSample := d.format.bits / 8
	for channel := 0; channel < d.format.channels; channel++ {
		data := frame[channel*bytesPerSample:]
		var value float64
		if d.format.floating {
			if d.format.bits == 32 {
				value = float64(math.Float32frombits(binary.LittleEndian.Uint32(data)))
			} else {
				value = math.Float64frombits(binary.LittleEndian.Uint64(data))
			}
			if math.IsNaN(value) {
				value = 0
			}
			value = max(-1, min(1, value))
		} else if d.format.bits == 8 {
			value = float64(int(data[0])-128) / 128
		} else {
			var raw uint32
			for i := 0; i < bytesPerSample; i++ {
				raw |= uint32(data[i]) << (8 * i)
			}
			signed := int32(raw<<(32-d.format.bits)) >> (32 - d.format.validBits)
			value = float64(signed) / math.Ldexp(1, d.format.validBits-1)
		}
		stereo[0] += value * d.weights[channel][0]
		stereo[1] += value * d.weights[channel][1]
	}
	return stereo
}

func pcm16(value float64) uint16 {
	return uint16(int16(max(-32768, min(32767, math.Round(value*32768)))))
}
