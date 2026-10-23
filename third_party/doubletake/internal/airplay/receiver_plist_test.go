package airplay

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"

	"howett.net/plist"
)

func binaryReceiverObjects(objects ...[]byte) []byte {
	data := []byte("bplist00")
	var offsets []uint16
	for _, object := range objects {
		offsets = append(offsets, uint16(len(data)))
		data = append(data, object...)
	}
	table := len(data)
	for _, offset := range offsets {
		data = binary.BigEndian.AppendUint16(data, offset)
	}
	trailer := make([]byte, 32)
	trailer[6], trailer[7] = 2, 1
	binary.BigEndian.PutUint64(trailer[8:16], uint64(len(objects)))
	binary.BigEndian.PutUint64(trailer[16:24], uint64(len(objects)-1))
	binary.BigEndian.PutUint64(trailer[24:32], uint64(table))
	return append(data, trailer...)
}

func TestReceiverPlistRejectsOverflowLengthsWithoutPanic(t *testing.T) {
	for _, marker := range []byte{0x4f, 0x5f, 0x6f, 0xaf, 0xdf} {
		object := []byte{marker, 0x13, 255, 255, 255, 255, 255, 255, 255, 255}
		var target map[string]any
		if _, err := unmarshalReceiverPlist(binaryReceiverObjects(object), &target); !errors.Is(err, ErrInvalidReceiverPlist) {
			t.Fatalf("marker %x: error = %v", marker, err)
		}
	}
}

func TestReceiverPlistRejectsCyclesDepthAndExpansion(t *testing.T) {
	var cases [][]byte
	cases = append(cases, binaryReceiverObjects([]byte{0xa1, 0}))
	objects := [][]byte{{0x51, 'x'}}
	for index := 0; index < 70; index++ {
		objects = append(objects, []byte{0xa1, byte(index)})
	}
	cases = append(cases, binaryReceiverObjects(objects...))
	objects = [][]byte{{0x51, 'x'}}
	for index := 0; index < 25; index++ {
		objects = append(objects, []byte{0xa2, byte(index), byte(index)})
	}
	cases = append(cases, binaryReceiverObjects(objects...))
	for _, data := range cases {
		var target any
		if _, err := unmarshalReceiverPlist(data, &target); !errors.Is(err, ErrInvalidReceiverPlist) {
			t.Fatalf("unsafe graph was accepted: %v", err)
		}
	}
}

func TestReceiverPlistAcceptsOrdinaryCapabilities(t *testing.T) {
	data, err := plist.Marshal(map[string]any{
		"name": "receiver", "features": uint64(0x3c177fde4a7fdfd5), "deviceID": "test-device",
		"displays": []any{map[string]any{"widthPixels": 1920, "heightPixels": 1080, "maxFPS": 30}},
		"pk":       make([]byte, 32),
	}, plist.BinaryFormat)
	if err != nil {
		t.Fatal(err)
	}
	var info ReceiverInfo
	if _, err := unmarshalReceiverPlist(data, &info); err != nil {
		t.Fatal(err)
	}
	if width, height := info.MirrorSize(); width != 1920 || height != 1080 || info.Displays[0].MaxFPS != 30 {
		t.Fatalf("capabilities changed: %+v", info)
	}
}

func TestMalformedEventPlistCannotPanicReceiverWorker(t *testing.T) {
	data := binaryReceiverObjects([]byte{0x4f, 0x13, 255, 255, 255, 255, 255, 255, 255, 255})
	err := handleEventRequest(eventRequest{method: "POST", path: "/command", body: data}, &mediaClock{}, time.Now())
	if !errors.Is(err, ErrInvalidReceiverPlist) {
		t.Fatalf("error = %v", err)
	}
}

func TestReceiverXMLPlistDepthBoundary(t *testing.T) {
	valid := []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>name</key><string>receiver</string></dict></plist>`)
	var info ReceiverInfo
	if _, err := unmarshalReceiverPlist(valid, &info); err != nil || info.Name != "receiver" {
		t.Fatalf("XML capabilities failed: name=%q error=%v", info.Name, err)
	}
	deep := []byte("<plist>" + strings.Repeat("<array>", 100) + "<string>x</string>" + strings.Repeat("</array>", 100) + "</plist>")
	if _, err := unmarshalReceiverPlist(deep, &info); !errors.Is(err, ErrInvalidReceiverPlist) {
		t.Fatalf("deep XML error = %v", err)
	}
}

func FuzzReceiverPlistBoundary(f *testing.F) {
	f.Add(binaryReceiverObjects([]byte{0x4f, 0x13, 255, 255, 255, 255, 255, 255, 255, 255}))
	f.Add(binaryReceiverObjects([]byte{0x51, 'x'}))
	f.Fuzz(func(t *testing.T, data []byte) {
		var target any
		_, _ = unmarshalReceiverPlist(data, &target)
	})
}
