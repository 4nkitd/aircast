package discovery

import (
	"net"
	"testing"

	"aircast/internal/receiver"
	"github.com/hashicorp/mdns"
)

func TestFilterUnrelatedAnnouncements(t *testing.T) {
	entry := &mdns.ServiceEntry{Name: "Printer._ipp._tcp.local.", AddrV4: net.ParseIP("192.168.1.40"), Port: 631}
	if _, err := fromEntry("_airplay._tcp", entry); err == nil {
		t.Fatal("accepted unrelated multicast response")
	}
}

func TestScopedIPv6AndServiceParsing(t *testing.T) {
	entry := &mdns.ServiceEntry{
		Name: "Living room._airplay._tcp.local.", Port: 7000,
		AddrV6IPAddr: &net.IPAddr{IP: net.ParseIP("fe80::1"), Zone: "eth0"},
		InfoFields:   []string{"model=AppleTV14,1"},
	}
	d, err := fromEntry("_airplay._tcp", entry)
	if err != nil || d.Name != "Living room" || d.Kind != receiver.AirPlay || d.Host != "fe80::1%eth0" {
		t.Fatalf("got %#v %v", d, err)
	}
}

func TestUnscopedLinkLocalAAAAIsNotAssignedAnUnverifiedInterface(t *testing.T) {
	entry := &mdns.ServiceEntry{Name: "TV._airplay._tcp.local.", Port: 7000, AddrV6IPAddr: &net.IPAddr{IP: net.ParseIP("fe80::1")}}
	if _, err := fromEntry("_airplay._tcp", entry); err == nil {
		t.Fatal("accepted a link-local address without known ingress scope")
	}
	if entry.AddrV6IPAddr.Zone != "" {
		t.Fatal("mutated library-owned service entry")
	}
}

func TestIgnoreInterfacesWithoutUsableIPv4BeforeApplyingInterfaceLimit(t *testing.T) {
	for _, addresses := range [][]net.Addr{nil, {&net.IPNet{IP: net.ParseIP("fe80::1")}}, {&net.IPAddr{IP: net.ParseIP("127.0.0.1")}}, {&net.IPAddr{IP: net.IPv4zero}}} {
		if hasIPv4(addresses) {
			t.Fatalf("selected an IPv4-unusable interface: %#v", addresses)
		}
	}
	if !hasIPv4([]net.Addr{&net.IPNet{IP: net.ParseIP("192.168.1.40")}}) {
		t.Fatal("did not select the real LAN interface")
	}
}

func TestServiceInstanceDNSPresentationEscapesAreDecoded(t *testing.T) {
	for _, test := range []struct{ label, name string }{
		{`Living\ room`, "Living room"},
		{`Living\032room`, "Living room"},
		{`TV\.one`, "TV.one"},
		{`TV\\one`, `TV\one`},
	} {
		entry := &mdns.ServiceEntry{Name: test.label + "._airplay._tcp.local.", AddrV4: net.ParseIP("192.168.1.40"), Port: 7000}
		device, err := fromEntry("_airplay._tcp", entry)
		if err != nil || device.Name != test.name {
			t.Fatalf("label %q became %q: %v", test.label, device.Name, err)
		}
	}
	entry := &mdns.ServiceEntry{Name: "extra.label._airplay._tcp.local.", AddrV4: net.ParseIP("192.168.1.40"), Port: 7000}
	if _, err := fromEntry("_airplay._tcp", entry); err == nil {
		t.Fatal("accepted a service instance spanning multiple DNS labels")
	}
}
