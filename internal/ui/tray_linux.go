package ui

import (
	"context"
	"time"

	"github.com/godbus/dbus/v5"
)

func hasTrayHost() bool {
	bus, err := dbus.ConnectSessionBus()
	if err != nil {
		return false
	}
	defer bus.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var present bool
	err = bus.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", 0, "org.kde.StatusNotifierWatcher").Store(&present)
	return err == nil && present
}
