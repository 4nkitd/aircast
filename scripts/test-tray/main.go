package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"
)

type watcher struct {
	bus   *dbus.Conn
	props *prop.Properties
}

func (w *watcher) RegisterStatusNotifierHost(string) *dbus.Error { return nil }

func (w *watcher) RegisterStatusNotifierItem(service string, sender dbus.Sender) *dbus.Error {
	destination, path := service, "/StatusNotifierItem"
	if strings.HasPrefix(service, "/") {
		destination, path = string(sender), service
	}
	item := destination + path
	w.props.SetMust("org.kde.StatusNotifierWatcher", "RegisteredStatusNotifierItems", []string{item})
	w.bus.Emit("/StatusNotifierWatcher", "org.kde.StatusNotifierWatcher.StatusNotifierItemRegistered", item)
	json.NewEncoder(os.Stdout).Encode(map[string]string{"destination": destination, "path": path, "synthetic": "true"})
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	bus, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer bus.Close()
	props, err := prop.Export(bus, "/StatusNotifierWatcher", prop.Map{
		"org.kde.StatusNotifierWatcher": {
			"RegisteredStatusNotifierItems":  {Value: []string{}, Emit: prop.EmitTrue},
			"IsStatusNotifierHostRegistered": {Value: true, Emit: prop.EmitTrue},
			"ProtocolVersion":                {Value: int32(0), Emit: prop.EmitTrue},
		},
	})
	if err != nil {
		return err
	}
	w := &watcher{bus: bus, props: props}
	if err := bus.Export(w, "/StatusNotifierWatcher", "org.kde.StatusNotifierWatcher"); err != nil {
		return err
	}
	reply, err := bus.RequestName("org.kde.StatusNotifierWatcher", dbus.NameFlagDoNotQueue)
	if err != nil {
		return err
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("a real tray host already exists; use a disposable D-Bus session")
	}
	fmt.Fprintln(os.Stderr, "Synthetic tray watcher ready. No tray icon is drawn by this test helper.")
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	<-stop
	return nil
}
