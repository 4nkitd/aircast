package main

import (
	"context"
	"errors"
	"sync"

	"doubletake/internal/airplay"
)

const maxCredentialAttempts = 3

type engineDependencies struct {
	prepare func(context.Context, airplay.CaptureConfig) (*airplay.CapturePreparation, error)
	keyring func() (airplay.CredentialBackend, error)
}

type sender struct {
	ctx        context.Context
	start      command
	writer     *eventWriter
	deps       engineDependencies
	credential sync.Mutex
	pending    chan string
}

func (s *sender) submitCredential(value string) bool {
	s.credential.Lock()
	defer s.credential.Unlock()
	if s.pending == nil {
		return false
	}
	s.pending <- value
	s.pending = nil
	return true
}

func (s *sender) requestCredential(kind, message string) (string, error) {
	request := make(chan string, 1)
	s.credential.Lock()
	s.pending = request
	s.credential.Unlock()
	defer func() {
		s.credential.Lock()
		if s.pending == request {
			s.pending = nil
		}
		s.credential.Unlock()
	}()
	_ = s.writer.emit(event{Event: "credential", State: "pairing", Kind: kind, Message: message})
	select {
	case <-s.ctx.Done():
		return "", s.ctx.Err()
	case value := <-request:
		return value, nil
	}
}

func (s *sender) run() (err error) {
	defer func() {
		if recover() != nil {
			err = failed("Receiver sent malformed AirPlay protocol data")
		}
	}()
	_ = s.writer.state("connecting", "Connecting to AirPlay receiver")
	backend, err := credentialCall(s.ctx, s.deps.keyring)
	if s.ctx.Err() != nil {
		return s.ctx.Err()
	}
	if err != nil {
		backend = nil
	}
	if backend != nil {
		backend = boundedKeyring{ctx: s.ctx, backend: backend}
	}
	store := newPairingStore(backend, s.writer)
	auth := &authentication{sender: s, store: store}
	defer auth.close()
	if err := auth.connect(); err != nil {
		return err
	}
	if err := auth.pair(); err != nil {
		return err
	}
	if err := auth.fairPlay(); err != nil {
		return err
	}
	return s.mirror(auth)
}

type authentication struct {
	sender   *sender
	store    *pairingStore
	client   *airplay.AirPlayClient
	info     *airplay.ReceiverInfo
	password string
	unwatch  func() bool
}

func (a *authentication) close() {
	if a.unwatch != nil {
		a.unwatch()
		a.unwatch = nil
	}
	if a.client != nil {
		_ = a.client.Close()
		a.client = nil
	}
}

func (a *authentication) connect() error {
	a.close()
	s := a.sender
	client := airplay.NewAirPlayClient(s.start.Host, s.start.Port)
	client.RequireReceiverAuthentication()
	client.SetPassword(a.password)
	if err := client.Connect(s.ctx); err != nil {
		return stageError(err, "Could not connect to the AirPlay receiver")
	}
	a.client = client
	a.unwatch = context.AfterFunc(s.ctx, func() { _ = client.Close() })
	for attempt := 0; attempt < maxCredentialAttempts; attempt++ {
		info, err := client.GetInfo()
		if err == nil {
			a.info = info
			if info.RequiresPassword() && a.password == "" {
				return a.askPassword("Enter the receiver's configured password")
			}
			return nil
		}
		if !credentialRejected(err) || attempt == maxCredentialAttempts-1 {
			return stageError(err, "Could not read receiver capabilities or authenticate")
		}
		if err := a.askPassword("Enter the receiver's configured password or displayed code"); err != nil {
			return err
		}
	}
	return failed("Receiver refused the password")
}

func (a *authentication) askPassword(message string) error {
	value, err := a.sender.requestCredential("password", message)
	if err != nil {
		return err
	}
	a.password = value
	a.client.SetPassword(value)
	return nil
}

func (a *authentication) pair() error {
	s := a.sender
	saved := a.store.lookup(a.info.DeviceID)
	if saved.HasPairingCredentials() {
		_ = s.writer.state("pairing", "Verifying saved pairing")
		err := a.client.RestorePairingCredentials(&saved)
		if err == nil {
			err = a.client.PairVerify(s.ctx)
		}
		if err == nil {
			return nil
		}
		if s.ctx.Err() != nil {
			return s.ctx.Err()
		}
		if errors.Is(err, airplay.ErrReceiverAuthentication) {
			return failed("Saved receiver identity could not be authenticated. Remove its saved pairing before pairing again")
		}
		return failed("Saved pairing could not be verified. Retry, or remove its saved pairing to pair again")
	}
	switch a.info.RequiredPairingCredential() {
	case airplay.PairingCredentialPIN:
		return a.pairWithCredential("pin", "")
	}
	_ = s.writer.state("pairing", "Establishing an encrypted AirPlay session")
	if err := a.client.Pair(s.ctx, ""); err == nil {
		return nil
	} else if s.ctx.Err() != nil {
		return s.ctx.Err()
	} else if errors.Is(err, airplay.ErrReceiverAuthentication) {
		return stageError(err, "Receiver refused pairing")
	}
	if err := a.connect(); err != nil {
		return err
	}
	if a.info.RequiresPassword() {
		return a.pairWithCredential("password", a.password)
	}
	return a.pairWithCredential("pin_or_password", "")
}

func (a *authentication) pairWithCredential(kind, value string) error {
	s := a.sender
	for attempt := 0; attempt < maxCredentialAttempts; attempt++ {
		if value == "" {
			message := "Enter the receiver's configured password"
			if kind != "password" {
				displayErr := a.client.StartPINDisplay()
				if s.ctx.Err() != nil {
					return s.ctx.Err()
				}
				message = "Enter the receiver's pairing PIN or configured password"
				if kind == "pin" && displayErr == nil {
					message = "Enter the PIN shown on the TV"
				}
			}
			if attempt > 0 {
				message = "Receiver refused the credential. " + message
			}
			var err error
			value, err = s.requestCredential(kind, message)
			if err != nil {
				return err
			}
		}
		a.password = value
		if err := a.client.PairWithCredential(s.ctx, value); err == nil {
			a.store.savePairing(a.info.DeviceID, a.client)
			return nil
		} else if s.ctx.Err() != nil {
			return s.ctx.Err()
		} else if errors.Is(err, airplay.ErrReceiverAuthentication) {
			return stageError(err, "Receiver refused pairing")
		}
		value = ""
		if attempt < maxCredentialAttempts-1 {
			if err := a.connect(); err != nil {
				return err
			}
		}
	}
	return failed("Receiver refused pairing after three attempts")
}

func (a *authentication) fairPlay() error {
	for attempt := 0; attempt < maxCredentialAttempts; attempt++ {
		err := a.client.FairPlaySetup(a.sender.ctx)
		if err == nil || errors.Is(err, airplay.ErrFairPlayUnsupported) {
			return nil
		}
		if !credentialRejected(err) || attempt == maxCredentialAttempts-1 || a.sender.ctx.Err() != nil {
			return stageError(err, "Could not establish encrypted media keys")
		}
		if err := a.askPassword("Enter the receiver's configured password or displayed code"); err != nil {
			return err
		}
	}
	return failed("Receiver refused the password")
}

func credentialRejected(err error) bool {
	var status *airplay.HTTPStatusError
	return errors.Is(err, airplay.ErrCredentialsRequired) || errors.As(err, &status) && status.StatusCode == 401
}

func stageError(err error, message string) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, airplay.ErrInvalidReceiverPlist) {
		return err
	}
	return failed(message)
}
