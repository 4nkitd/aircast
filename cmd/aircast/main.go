package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"aircast/internal/config"
	"aircast/internal/ui"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
)

func main() {
	path, err := config.Path()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	a := app.NewWithID("io.aircast.desktop")
	a.Lifecycle().SetOnStopped(cancel)
	p := ui.New(a, path)
	defer p.Close()
	p.Run(ctx, func() {
		cancel()
		a.Quit()
	})
	go func() {
		<-ctx.Done()
		fyne.Do(a.Quit)
	}()
	a.Run()
}
