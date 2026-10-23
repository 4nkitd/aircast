package discovery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"aircast/internal/receiver"
	"github.com/hashicorp/mdns"
	"github.com/miekg/dns"
)

var Services = []string{"_airplay._tcp", "_raop._tcp", "_googlecast._tcp"}

func Scan(ctx context.Context, timeout time.Duration, observe func(receiver.Device)) error {
	if timeout <= 0 || timeout > 15*time.Second {
		timeout = 4 * time.Second
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	var active []net.Interface
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil || !hasIPv4(addresses) {
			continue
		}
		active = append(active, iface)
		if len(active) == 8 {
			break
		}
	}
	if len(active) == 0 {
		return errors.New("no active IPv4 multicast network interface; connect to Wi-Fi or Ethernet")
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []error
	failedInterfaces := make(map[string]bool)
	for _, iface := range active {
		for _, service := range Services {
			wg.Add(1)
			go func(iface net.Interface, service string) {
				defer wg.Done()
				entries := make(chan *mdns.ServiceEntry, 512)
				params := mdns.DefaultParams(service)
				params.Interface, params.Entries, params.Timeout = &iface, entries, timeout
				params.WantUnicastResponse = true
				params.Logger = log.New(io.Discard, "", 0)
				// Querying over IPv4 still resolves IPv6 records. Avoid the library's
				// partial IPv6 socket initialization leaks on IPv4-only desktops.
				params.DisableIPv6 = true
				err := mdns.QueryContext(ctx, params)
				close(entries)
				mu.Lock()
				defer mu.Unlock()
				if err != nil && !failedInterfaces[iface.Name] {
					failures = append(failures, fmt.Errorf("%s: %w", iface.Name, err))
					failedInterfaces[iface.Name] = true
				}
				// The library mutates entries until QueryContext returns. Consume
				// only now so discovery never races its DNS assembly goroutine.
				for entry := range entries {
					if d, err := fromEntry(service, entry); err == nil {
						observe(d)
					}
				}
			}(iface, service)
		}
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.Join(failures...)
}

func hasIPv4(addresses []net.Addr) bool {
	for _, address := range addresses {
		var ip net.IP
		switch value := address.(type) {
		case *net.IPNet:
			ip = value.IP
		case *net.IPAddr:
			ip = value.IP
		}
		if ip.To4() != nil && !ip.IsUnspecified() && !ip.IsLoopback() && !ip.IsMulticast() {
			return true
		}
	}
	return false
}

func fromEntry(service string, entry *mdns.ServiceEntry) (receiver.Device, error) {
	suffix := "." + service + ".local."
	if !strings.HasSuffix(strings.ToLower(entry.Name), suffix) {
		return receiver.Device{}, errors.New("unrelated service announcement")
	}
	name := entry.Name[:len(entry.Name)-len(suffix)]
	var label [256]byte
	length, err := dns.PackDomainName(name+".", label[:], 0, nil, false)
	if err != nil || length < 2 || int(label[0])+2 != length {
		return receiver.Device{}, errors.New("invalid service instance label")
	}
	name = string(label[1 : length-1])
	host := ""
	if entry.AddrV4 != nil {
		host = entry.AddrV4.String()
	} else if entry.AddrV6IPAddr != nil {
		host = entry.AddrV6IPAddr.String()
	}
	return receiver.FromService(service, name, host, entry.Port, entry.InfoFields)
}
