package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"

	"aircast/internal/diagnostics"
	"aircast/internal/discovery"
	"aircast/internal/receiver"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: aircastctl discover [-timeout 4s] | probe [-kind airplay|raop|cast] address")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	switch os.Args[1] {
	case "discover":
		flags := flag.NewFlagSet("discover", flag.ContinueOnError)
		timeout := flags.Duration("timeout", 4*time.Second, "discovery query duration, at most 15s")
		if err := flags.Parse(os.Args[2:]); err != nil {
			return err
		}
		devices := make(map[string]receiver.Device)
		var mu sync.Mutex
		err := discovery.Scan(ctx, *timeout, func(d receiver.Device) {
			mu.Lock()
			defer mu.Unlock()
			if old, ok := devices[d.ID]; !ok || old.Kind != receiver.AirPlay {
				if len(devices) < receiver.MaxDevices || ok {
					devices[d.ID] = d
				}
			}
		})
		list := make([]receiver.Device, 0, len(devices))
		for _, d := range devices {
			list = append(list, d)
		}
		sort.Slice(list, func(a, b int) bool { return list[a].Name < list[b].Name })
		if encodeErr := enc.Encode(list); encodeErr != nil {
			return encodeErr
		}
		return err
	case "probe":
		flags := flag.NewFlagSet("probe", flag.ContinueOnError)
		kind := flags.String("kind", "airplay", "airplay, raop, or cast")
		if err := flags.Parse(os.Args[2:]); err != nil {
			return err
		}
		if flags.NArg() != 1 {
			return fmt.Errorf("provide one receiver IP address or hostname, optionally with a port")
		}
		d, err := receiver.Manual("", flags.Arg(0), receiver.Kind(*kind))
		if err != nil {
			return err
		}
		result, err := diagnostics.Probe(ctx, d)
		if err != nil {
			return err
		}
		return enc.Encode(result)
	default:
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}
