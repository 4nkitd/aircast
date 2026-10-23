package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hashicorp/mdns"
	"howett.net/plist"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	data, err := plist.Marshal(map[string]any{"model": "AircastFixture1,1", "name": "Aircast test receiver"}, plist.BinaryFormat)
	if err != nil {
		listener.Close()
		return err
	}
	server := &http.Server{ReadHeaderTimeout: 2 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/info" {
			http.Error(w, "test fixture: only GET /info exists", 404)
			return
		}
		w.Header().Set("Content-Type", "application/x-apple-binary-plist")
		w.Write(data)
	})}
	go server.Serve(listener)
	defer server.Close()
	service, err := mdns.NewMDNSService("Aircast test receiver", "_airplay._tcp", "local.", "aircast-fixture.local.", port, []net.IP{net.ParseIP("127.0.0.1")}, []string{"deviceid=02:00:00:00:00:01", "model=AircastFixture1,1"})
	if err != nil {
		return err
	}
	advertisement, err := mdns.NewServer(&mdns.Config{Zone: service})
	if err != nil {
		return err
	}
	defer advertisement.Shutdown()
	json.NewEncoder(os.Stdout).Encode(map[string]any{"synthetic": true, "address": listener.Addr().String(), "name": "Aircast test receiver"})
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	<-ctx.Done()
	return nil
}
