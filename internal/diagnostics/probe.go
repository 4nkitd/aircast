package diagnostics

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"aircast/internal/receiver"
)

type Result struct {
	Summary string
	Model   string
	Detail  string
}

func Probe(ctx context.Context, d receiver.Device) (Result, error) {
	if err := d.Validate(); err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if d.Kind != receiver.AirPlay {
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.Endpoint())
		if err != nil {
			return Result{}, fmt.Errorf("receiver did not accept a TCP connection: %w", err)
		}
		conn.Close()
		return Result{Summary: "TCP port reachable", Detail: "Discovery and TCP reachability only. Authentication and streaming have not been tested."}, nil
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 4 * time.Second, MaxResponseHeaderBytes: 16 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	endpoint := url.URL{Scheme: "http", Host: d.Endpoint(), Path: "/info"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", "Aircast/0.1")
	response, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("AirPlay information request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return Result{Summary: "Receiver requires authentication", Detail: "The AirPlay service responded. This check did not request a PIN or start playback."}, nil
	}
	if response.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("AirPlay /info returned HTTP %d; streaming compatibility is unknown", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return Result{}, err
	}
	if len(data) > 1<<20 {
		return Result{}, errors.New("receiver returned more than 1 MiB of information")
	}
	model, err := parseModel(data)
	if err != nil {
		return Result{}, errors.New("receiver answered /info but did not return a valid AirPlay property list")
	}
	return Result{Summary: "AirPlay information received", Model: model, Detail: "The receiver answered /info. No pairing, screen capture, audio, or playback was started."}, nil
}

func parseModel(data []byte) (model string, err error) {
	// The plist decoder can panic on overflowing lengths in untrusted binary
	// objects. Keep that failure inside the network parsing boundary.
	defer func() {
		if recover() != nil {
			model, err = "", errors.New("invalid binary property list")
		}
	}()
	if bytes.HasPrefix(data, []byte("bplist00")) {
		model, err = binaryModel(data)
	} else {
		model, err = xmlModel(data)
	}
	if err != nil {
		return "", err
	}
	if len(model) > 120 {
		return "", nil
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return -1
		}
		return r
	}, model), nil
}
