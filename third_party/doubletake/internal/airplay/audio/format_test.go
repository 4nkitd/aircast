package audio

import (
	"encoding/binary"
	"math"
	"testing"
)

func testFormat(rate, channels, bits int, floating bool) mixFormat {
	return mixFormat{rate: rate, channels: channels, bits: bits, validBits: bits,
		blockAlign: channels * bits / 8, floating: floating}
}

func TestDecodeMixFormats(t *testing.T) {
	float32PCM := func(values ...float32) []byte {
		data := make([]byte, 4*len(values))
		for i, value := range values {
			binary.LittleEndian.PutUint32(data[4*i:], math.Float32bits(value))
		}
		return data
	}
	float64PCM := func(values ...float64) []byte {
		data := make([]byte, 8*len(values))
		for i, value := range values {
			binary.LittleEndian.PutUint64(data[8*i:], math.Float64bits(value))
		}
		return data
	}
	valid24 := testFormat(44100, 2, 32, false)
	valid24.validBits = 24
	tests := []struct {
		name   string
		format mixFormat
		data   []byte
		want   [2]int16
	}{
		{"unsigned 8-bit", testFormat(44100, 2, 8, false), []byte{0, 255}, [2]int16{-32768, 32512}},
		{"signed 16-bit", testFormat(44100, 2, 16, false), []byte{0, 128, 255, 127}, [2]int16{-32768, 32767}},
		{"packed 24-bit", testFormat(48000, 2, 24, false), []byte{0, 0, 128, 255, 255, 127}, [2]int16{-32768, 32767}},
		{"signed 32-bit", testFormat(96000, 2, 32, false), []byte{0, 0, 0, 128, 255, 255, 255, 127}, [2]int16{-32768, 32767}},
		{"24 valid bits in 32-bit", valid24, []byte{0xff, 0, 0, 128, 0xff, 0xff, 0xff, 127}, [2]int16{-32768, 32767}},
		{"float32", testFormat(48000, 2, 32, true), float32PCM(-0.5, 0.25), [2]int16{-16384, 8192}},
		{"float64", testFormat(44100, 2, 64, true), float64PCM(-0.25, 0.5), [2]int16{-8192, 16384}},
		{"mono", testFormat(44100, 1, 16, false), []byte{0, 64}, [2]int16{16384, 16384}},
		{"nonfinite", testFormat(48000, 2, 32, true), float32PCM(float32(math.NaN()), float32(math.Inf(1))), [2]int16{0, 32767}},
		{"clipping", testFormat(48000, 2, 32, true), float32PCM(-4, 4), [2]int16{-32768, 32767}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoder, err := newSampleDecoder(test.format)
			if err != nil {
				t.Fatal(err)
			}
			sample := decoder.decode(test.data)
			got := [2]int16{int16(pcm16(sample[0])), int16(pcm16(sample[1]))}
			if got != test.want {
				t.Fatalf("decode = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSurroundDownmix(t *testing.T) {
	format := testFormat(48000, 6, 32, true)
	format.channelMask = 0x3f
	decoder, err := newSampleDecoder(format)
	if err != nil {
		t.Fatal(err)
	}
	for channel := 0; channel < 6; channel++ {
		data := make([]byte, format.blockAlign)
		binary.LittleEndian.PutUint32(data[channel*4:], math.Float32bits(1))
		got := decoder.decode(data)
		switch channel {
		case 0, 4:
			if got[0] <= 0 || got[1] != 0 {
				t.Fatalf("left channel %d downmix = %v", channel, got)
			}
		case 1, 5:
			if got[1] <= 0 || got[0] != 0 {
				t.Fatalf("right channel %d downmix = %v", channel, got)
			}
		case 2:
			if got[0] != got[1] || got[0] <= 0 {
				t.Fatalf("center downmix = %v", got)
			}
		case 3:
			if got != (stereoSample{}) {
				t.Fatalf("LFE was mixed into full-range speakers: %v", got)
			}
		}
	}
	data := make([]byte, format.blockAlign)
	for channel := 0; channel < 6; channel++ {
		binary.LittleEndian.PutUint32(data[channel*4:], math.Float32bits(1))
	}
	got := decoder.decode(data)
	if math.Abs(got[0]-1) > 1e-12 || math.Abs(got[1]-1) > 1e-12 {
		t.Fatalf("normalized full-scale downmix = %v", got)
	}
}

func TestRejectUnsupportedMixFormats(t *testing.T) {
	valid := testFormat(48000, 2, 32, true)
	tests := []mixFormat{
		{rate: 0}, testFormat(384000, 2, 32, true), testFormat(48000, 9, 32, true),
		testFormat(48000, 6, 32, true), testFormat(48000, 2, 20, false),
		testFormat(48000, 2, 16, true),
	}
	bad := valid
	bad.blockAlign++
	tests = append(tests, bad)
	bad = valid
	bad.validBits = 33
	tests = append(tests, bad)
	bad = valid
	bad.channelMask = 0x10001
	tests = append(tests, bad)
	bad = valid
	bad.channelMask = 4
	tests = append(tests, bad)
	for _, format := range tests {
		if _, err := newSampleDecoder(format); err == nil {
			t.Errorf("accepted unsupported format %+v", format)
		}
	}
}
