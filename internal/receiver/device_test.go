package receiver

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestManual(t *testing.T) {
	for _, tc := range []struct {
		address string
		kind    Kind
		host    string
		port    int
	}{
		{"192.168.1.40", AirPlay, "192.168.1.40", 7000},
		{"TV.local.:7100", AirPlay, "tv.local", 7100},
		{"speaker.local", Audio, "speaker.local", 5000},
		{"tv.local", Cast, "tv.local", 8009},
		{"[2001:db8::1]:7001", AirPlay, "2001:db8::1", 7001},
		{"[2001:db8::1]", AirPlay, "2001:db8::1", 7000},
		{"2001:db8::1", AirPlay, "2001:db8::1", 7000},
		{"[fe80::1%eth0]:7000", AirPlay, "fe80::1%eth0", 7000},
	} {
		t.Run(tc.address, func(t *testing.T) {
			d, err := Manual("", tc.address, tc.kind)
			if err != nil || d.Host != tc.host || d.Port != tc.port {
				t.Fatalf("got %#v, %v", d, err)
			}
		})
	}
}

func TestRejectInvalidAddresses(t *testing.T) {
	for _, address := range []string{"", "https://tv.local", "user@host", "tv.local/path", "tv.local?x=1", "tv.local:0", "tv.local:65536", "tv.local:abc", "0.0.0.0", "224.0.0.251", "[::]", "[fe80::1]", "two words", "-bad.local", "bad..local", "host:\n7000"} {
		t.Run(address, func(t *testing.T) {
			if _, err := Manual("Test", address, AirPlay); err == nil {
				t.Fatalf("accepted %q", address)
			}
		})
	}
	if _, err := Manual("Test", "tv.local", Kind("miracast")); err == nil {
		t.Fatal("accepted invalid protocol")
	}
	if _, err := Manual("Bad\nname", "tv.local", AirPlay); err == nil {
		t.Fatal("accepted a control character in a user-provided name")
	}
}

func TestAirPlayAudioIdentityMatchesVideo(t *testing.T) {
	video, err := FromService("_airplay._tcp", "Living room", "192.168.1.40", 7000, []string{"deviceid=AA:BB:CC:DD:EE:FF", "model=AppleTV14,1"})
	if err != nil {
		t.Fatal(err)
	}
	audio, err := FromService("_raop._tcp", "AABBCCDDEEFF@Living room", "192.168.1.40", 5000, []string{"am=AppleTV14,1"})
	if err != nil || audio.ID != video.ID || audio.Name != "Living room" {
		t.Fatalf("audio %#v, video %#v, err %v", audio, video, err)
	}
}

func TestCastFriendlyNameAndUntrustedText(t *testing.T) {
	d, err := FromService("_googlecast._tcp", "Chromecast-uuid", "192.168.1.41", 8009, []string{"fn=Bedroom\x1b\n\u202e", "md=Google TV", "id=abcd"})
	if err != nil || d.Name != "Bedroom" || d.Model != "Google TV" || d.ID != "cast:abcd" {
		t.Fatalf("got %#v, %v", d, err)
	}
	d, err = FromService("_airplay._tcp", strings.Repeat("電", 200), "192.168.1.40", 7000, nil)
	if err != nil || len(d.Name) > 160 || !utf8.ValidString(d.Name) {
		t.Fatalf("invalid truncated Unicode %#v %v", d, err)
	}
}
