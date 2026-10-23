package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"doubletake/internal/airplay"
)

type blockedKeyring struct {
	started chan struct{}
	release chan struct{}
}

func (b blockedKeyring) Lookup(string) (*airplay.SavedCredentials, error) {
	close(b.started)
	<-b.release
	return nil, errors.New("locked")
}

func (blockedKeyring) Save(string, *airplay.SavedCredentials) error { return nil }

func TestEOFCancelsBlockedKeyringLookup(t *testing.T) {
	server := testReceiver(t, airplay.ReceiverProfileModern, airplay.ReceiverAuthNone, "")
	backend := blockedKeyring{started: make(chan struct{}), release: make(chan struct{})}
	defer close(backend.release)
	h := newTestEngine(t, backend)
	h.send(startReceiver(server))
	select {
	case <-backend.started:
	case <-time.After(5 * time.Second):
		t.Fatal("engine did not read the keyring")
	}
	h.stop(true)
}

func TestCredentialCallCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started, release, returned := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	defer close(release)
	go func() {
		_, err := credentialCall(ctx, func() (int, error) {
			close(started)
			<-release
			return 0, nil
		})
		returned <- err
	}()
	<-started
	cancel()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("OS credential call blocked cancellation")
	}
}
