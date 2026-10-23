package airplay

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

func withoutTestONVIF(packet testRTPPacket) []byte {
	framed := testRTPPacketBytes(packet)
	rtp := append([]byte(nil), framed[2:14]...)
	rtp[0] &^= 0x10
	rtp = append(rtp, framed[30:]...)
	return frameRFC4571(rtp)
}

func TestFirstFragmentONVIFPreservesLargePicturePTS(t *testing.T) {
	pts := time.Now().Add(-25 * time.Millisecond)
	for _, codec := range []VideoCodec{VideoCodecH264, VideoCodecHEVC} {
		t.Run(string(codec), func(t *testing.T) {
			fragmentHeaders := [][]byte{{0x7c, 0x85}, {0x7c, 0x05}, {0x7c, 0x45}}
			original := []byte{0, 0, 0, 1, 0x65}
			if codec == VideoCodecHEVC {
				fragmentHeaders = [][]byte{{0x62, 1, 0x93}, {0x62, 1, 0x13}, {0x62, 1, 0x53}}
				original = []byte{0, 0, 0, 1, 0x26, 1}
			}
			var stream []byte
			for index, header := range fragmentHeaders {
				fragment := bytes.Repeat([]byte{byte(index + 17)}, 59000)
				original = append(original, fragment...)
				packet := testRTPPacket{uint16(65534 + index), 123, ntpFromTime(pts), index == 2, append(header, fragment...)}
				if index == 0 {
					stream = append(stream, testRTPPacketBytes(packet)...)
				} else {
					stream = append(stream, withoutTestONVIF(packet)...)
				}
			}
			reader := newRTPVideoAccessUnitReaderForCodecWithNow(bytes.NewReader(stream), codec, time.Now)
			unit, err := reader.ReadVideoAccessUnit()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(unit.AnnexB, original) || !unit.PTS.Equal(timeFromNTP(ntpFromTime(pts))) {
				t.Fatalf("large picture or capture PTS changed: bytes=%d pts=%v", len(unit.AnnexB), unit.PTS)
			}
		})
	}
}

func TestFirstFragmentONVIFRejectsInvalidContinuation(t *testing.T) {
	pts := ntpFromTime(time.Now())
	first := testRTPPacket{7, 100, pts, false, []byte{0x7c, 0x85, 0xaa}}
	for _, test := range []struct {
		name   string
		packet testRTPPacket
		want   string
	}{
		{"changed timestamp", testRTPPacket{8, 101, pts, true, []byte{0x7c, 0x45, 0xbb}}, "timestamp changed"},
		{"missing sequence", testRTPPacket{9, 100, pts, true, []byte{0x7c, 0x45, 0xbb}}, "sequence discontinuity"},
		{"early marker", testRTPPacket{8, 100, pts, true, []byte{0x7c, 0x05, 0xbb}}, "marker arrived"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stream := append(testRTPPacketBytes(first), withoutTestONVIF(test.packet)...)
			reader := newRTPVideoAccessUnitReaderWithNow(bytes.NewReader(stream), time.Now)
			if _, err := reader.ReadVideoAccessUnit(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %s", err, test.want)
			}
		})
	}
	stream := append(testRTPPacketBytes(first), withoutTestONVIF(testRTPPacket{8, 100, pts, true, []byte{0x7c, 0x45, 0xbb}})...)
	stream = append(stream, withoutTestONVIF(testRTPPacket{9, 200, pts, true, []byte{0x41, 0xcc}})...)
	reader := newRTPVideoAccessUnitReaderWithNow(bytes.NewReader(stream), time.Now)
	if _, err := reader.ReadVideoAccessUnit(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadVideoAccessUnit(); err == nil || !strings.Contains(err.Error(), "first access-unit packet") {
		t.Fatalf("next picture inherited the old PTS: %v", err)
	}
}

func TestFirstFragmentONVIFRetainsSSRCAndSizeBounds(t *testing.T) {
	pts := ntpFromTime(time.Now())
	first := testRTPPacketBytes(testRTPPacket{1, 100, pts, false, []byte{0x7c, 0x85, 0xaa}})
	last := withoutTestONVIF(testRTPPacket{2, 100, pts, true, []byte{0x7c, 0x45, 0xbb}})
	reader := newRTPVideoAccessUnitReaderWithNow(bytes.NewReader(append(append([]byte(nil), first...), last...)), time.Now)
	reader.maxAccessUnitBytes = 6
	if _, err := reader.ReadVideoAccessUnit(); err == nil || !strings.Contains(err.Error(), "access unit exceeds") {
		t.Fatalf("size bound failed: %v", err)
	}
	binary.BigEndian.PutUint32(last[10:14], testVideoSSRC+1)
	reader = newRTPVideoAccessUnitReaderWithNow(bytes.NewReader(append(first, last...)), time.Now)
	if _, err := reader.ReadVideoAccessUnit(); err == nil || !strings.Contains(err.Error(), "SSRC changed") {
		t.Fatalf("SSRC bound failed: %v", err)
	}
}
