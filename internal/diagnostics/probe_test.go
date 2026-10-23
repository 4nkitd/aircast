package diagnostics

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aircast/internal/receiver"
	"howett.net/plist"
)

func fixtureDevice(t *testing.T, server *httptest.Server) receiver.Device {
	t.Helper()
	d, err := receiver.Manual("Test receiver", strings.TrimPrefix(server.URL, "http://"), receiver.AirPlay)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestMalformedBinaryPlistCannotPanicTheApplication(t *testing.T) {
	data := append([]byte("bplist00"), 0x4f, 0x13)
	for range 8 {
		data = append(data, 0xff)
	}
	data = append(data, 8)
	trailer := make([]byte, 32)
	trailer[6], trailer[7] = 1, 1
	binary.BigEndian.PutUint64(trailer[8:16], 1)
	binary.BigEndian.PutUint64(trailer[24:32], 18)
	data = append(data, trailer...)
	if _, err := parseModel(data); err == nil {
		t.Fatal("accepted overflowing binary object length")
	}
	for n := 0; n < len(data); n++ {
		parseModel(data[:n])
	}
}

func TestModelSanitizesUntrustedControlCharacters(t *testing.T) {
	data, _ := plist.Marshal(map[string]string{"model": "TV\x1b\n\u202e"}, plist.BinaryFormat)
	model, err := parseModel(data)
	if err != nil || model != "TV" {
		t.Fatalf("unsafe model %q %v", model, err)
	}
}

func TestUnicodeModelAndDeepXML(t *testing.T) {
	data, _ := plist.Marshal(map[string]string{"model": "電視"}, plist.BinaryFormat)
	model, err := parseModel(data)
	if err != nil || model != "電視" {
		t.Fatalf("UTF-16 model %q %v", model, err)
	}
	if _, err := parseModel([]byte("<plist>" + strings.Repeat("<array>", 100) + strings.Repeat("</array>", 100) + "</plist>")); err == nil {
		t.Fatal("accepted XML beyond the nesting bound")
	}
}

func TestRejectOpenStepRatherThanAutodetectRecursiveFormats(t *testing.T) {
	data := []byte("{model=TV;x=" + strings.Repeat("(", 500000) + strings.Repeat(")", 500000) + ";}")
	if _, err := parseModel(data); err == nil {
		t.Fatal("accepted a non-XML legacy property list")
	}
}

func FuzzParseModel(f *testing.F) {
	f.Add([]byte("bplist00"))
	f.Add([]byte(`<?xml version="1.0"?><plist><dict><key>model</key><string>TV</string></dict></plist>`))
	f.Add([]byte("{model=TV;x=(((("))
	encoded, _ := plist.Marshal(map[string]string{"model": "TV"}, plist.BinaryFormat)
	f.Add(encoded)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) <= 1<<20 {
			parseModel(data)
		}
	})
}

func TestProbeAirPlayBinaryAndXMLPlists(t *testing.T) {
	for _, format := range []int{plist.BinaryFormat, plist.XMLFormat} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/info" || r.Method != http.MethodGet {
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			}
			data, _ := plist.Marshal(map[string]any{"model": "AircastFixture1,1"}, format)
			w.Write(data)
		}))
		result, err := Probe(context.Background(), fixtureDevice(t, server))
		server.Close()
		if err != nil || result.Model != "AircastFixture1,1" || !strings.Contains(result.Detail, "No pairing") {
			t.Fatalf("got %#v %v", result, err)
		}
	}
}

func TestProbeAuthResponseDoesNotInitiatePairing(t *testing.T) {
	for _, code := range []int{401, 403} {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			if r.URL.Path != "/info" {
				t.Errorf("attempted pairing/playback: %s", r.URL.Path)
			}
			w.WriteHeader(code)
		}))
		result, err := Probe(context.Background(), fixtureDevice(t, server))
		server.Close()
		if err != nil || !strings.Contains(result.Summary, "authentication") || !strings.Contains(result.Detail, "did not request a PIN") || requests.Load() != 1 {
			t.Fatalf("got %#v %v", result, err)
		}
	}
}

func TestProbeDoesNotFollowRedirects(t *testing.T) {
	var followed atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed.Store(true) }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer server.Close()
	if _, err := Probe(context.Background(), fixtureDevice(t, server)); err == nil || followed.Load() {
		t.Fatal("redirect was accepted or followed")
	}
}

func TestProbeRejectsInvalidAndOversizedBody(t *testing.T) {
	for _, body := range []string{"not a plist", strings.Repeat("x", (1<<20)+1)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		_, err := Probe(context.Background(), fixtureDevice(t, server))
		server.Close()
		if err == nil {
			t.Fatal("invalid receiver information accepted")
		}
	}
}

func TestProbeCancellationAndTCPOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	d := fixtureDevice(t, server)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := Probe(ctx, d); err == nil || time.Since(start) > time.Second {
		t.Fatal("probe did not cancel promptly")
	}
	d.Kind = receiver.Cast
	result, err := Probe(context.Background(), d)
	if err != nil || result.Summary != "TCP port reachable" || !strings.Contains(result.Detail, "have not been tested") {
		t.Fatalf("TCP check overstated result: %#v %v", result, err)
	}
}
