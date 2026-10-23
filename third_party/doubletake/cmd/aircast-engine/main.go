package main

import (
	"context"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"doubletake/internal/airplay"
)

func main() {
	os.Exit(mainRun())
}

func mainRun() int {
	log.SetOutput(io.Discard)
	airplay.SetDebugMode(false)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) != 1 {
		_ = newEventWriter(os.Stdout, func() {}).state("failed", "This process accepts JSON commands on standard input")
		return 1
	}
	return serve(ctx, os.Stdin, os.Stdout, engineDependencies{
		prepare: airplay.PrepareCapture,
		keyring: newOSKeyring,
	})
}
