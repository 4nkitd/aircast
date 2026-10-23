package receiver

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"unicode"
)

type Kind string

const (
	AirPlay Kind = "airplay"
	Audio   Kind = "raop"
	Cast    Kind = "cast"
)

func (k Kind) Label() string {
	switch k {
	case AirPlay:
		return "AirPlay"
	case Audio:
		return "AirPlay audio"
	case Cast:
		return "Google Cast"
	default:
		return "Unknown"
	}
}

func (k Kind) Valid() bool { return k == AirPlay || k == Audio || k == Cast }

func (k Kind) DefaultPort() int {
	switch k {
	case Audio:
		return 5000
	case Cast:
		return 8009
	default:
		return 7000
	}
}

type Device struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   Kind   `json:"kind"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Model  string `json:"model,omitempty"`
	Saved  bool   `json:"-"`
	Online bool   `json:"-"`
}

func (d Device) Endpoint() string { return net.JoinHostPort(d.Host, strconv.Itoa(d.Port)) }

func (d Device) Validate() error {
	if !d.Kind.Valid() {
		return errors.New("choose AirPlay, AirPlay audio, or Google Cast")
	}
	if d.Port < 1 || d.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if !validHost(d.Host) {
		return errors.New("enter an IP address or hostname, not a URL")
	}
	if strings.TrimSpace(d.Name) == "" || len(d.Name) > 160 || clean(d.Name, 160) != d.Name {
		return errors.New("receiver name must contain 1 to 160 printable characters")
	}
	if d.ID == "" || len(d.ID) > 320 || clean(d.ID, 320) != d.ID {
		return errors.New("invalid receiver identity")
	}
	return nil
}

func Manual(name, address string, kind Kind) (Device, error) {
	if !kind.Valid() {
		return Device{}, errors.New("invalid receiver type")
	}
	address = strings.TrimSpace(address)
	host, port := address, kind.DefaultPort()
	if h, p, err := net.SplitHostPort(address); err == nil {
		host = h
		parsed, err := strconv.Atoi(p)
		if err != nil {
			return Device{}, errors.New("port must be a number")
		}
		port = parsed
	} else if strings.HasPrefix(address, "[") && strings.HasSuffix(address, "]") {
		host = address[1 : len(address)-1]
	} else if strings.Contains(address, ":") {
		if _, err := netip.ParseAddr(address); err != nil {
			return Device{}, errors.New("use host:port or [IPv6]:port")
		}
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		host = ip.Unmap().String()
	} else {
		host = strings.ToLower(strings.TrimSuffix(host, "."))
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = host
	}
	d := Device{Name: name, Host: host, Port: port, Kind: kind}
	d.ID = string(kind) + ":" + d.Endpoint()
	return d, d.Validate()
}

func validHost(host string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		return !ip.IsUnspecified() && !ip.IsMulticast() && (!ip.IsLinkLocalUnicast() || !ip.Is6() || ip.Zone() != "")
	}
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func clean(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return -1
		}
		return r
	}, value)
	var result strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if result.Len()+len(string(r)) > limit {
			break
		}
		result.WriteRune(r)
	}
	return result.String()
}

func FromService(service, instance, host string, port int, fields []string) (Device, error) {
	kind := map[string]Kind{"_airplay._tcp": AirPlay, "_raop._tcp": Audio, "_googlecast._tcp": Cast}[service]
	if !kind.Valid() {
		return Device{}, errors.New("unrecognized discovery service")
	}
	txt := make(map[string]string)
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if ok && len(key) <= 32 && len(value) <= 512 {
			txt[strings.ToLower(key)] = value
		}
	}
	name := clean(instance, 160)
	model, identity := txt["model"], txt["deviceid"]
	if kind == Audio {
		if mac, rest, ok := strings.Cut(instance, "@"); ok {
			identity = mac
			name = clean(rest, 160)
		}
		model = txt["am"]
	}
	if kind == Cast {
		if friendly := clean(txt["fn"], 160); friendly != "" {
			name = friendly
		}
		model, identity = txt["md"], txt["id"]
	}
	d, err := Manual(name, net.JoinHostPort(host, strconv.Itoa(port)), kind)
	if err != nil {
		return Device{}, err
	}
	d.Model = clean(model, 120)
	identity = clean(identity, 128)
	if kind == AirPlay || kind == Audio {
		mac := strings.NewReplacer(":", "", "-", "").Replace(identity)
		if len(mac) == 12 {
			if _, err := strconv.ParseUint(mac, 16, 48); err == nil {
				d.ID = "airplay:" + strings.ToLower(mac)
			}
		}
	} else if identity != "" {
		d.ID = fmt.Sprintf("cast:%s", identity)
	}
	return d, d.Validate()
}
