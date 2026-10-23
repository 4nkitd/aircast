package airplay

import (
	"errors"
	"io"
	"os"
	"testing"
)

func TestExtractCapturedRTPAccessUnits(t *testing.T) {
	input, output := os.Getenv("AIRCAST_CAPTURE_TAP"), os.Getenv("AIRCAST_ANNEXB_OUTPUT")
	if input == "" && output == "" {
		t.Skip("requires an owned, completed live capture tap and a new output path")
	}
	if input == "" || output == "" {
		t.Fatal("both capture tap paths are required")
	}
	source, err := os.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 64<<20 {
		t.Fatal("capture tap must be a nonempty regular file no larger than 64 MiB")
	}
	destination, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	reader := newRTPVideoAccessUnitReader(io.LimitReader(source, 64<<20))
	frames := 0
	for frames < 300 {
		unit, err := reader.ReadVideoAccessUnit()
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			// Stopping the live encoder can leave an unfinished pipe packet.
			// Only preceding complete access units belong in the decode probe.
			t.Log("discarded the incomplete capture-stop tail")
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := destination.Write(unit.AnnexB); err != nil {
			t.Fatal(err)
		}
		frames++
	}
	if frames == 0 {
		t.Fatal("capture tap contained no complete video access units")
	}
	t.Logf("wrote %d complete captured access units for picture decoding", frames)
}
