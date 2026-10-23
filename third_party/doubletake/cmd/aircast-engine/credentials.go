package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"doubletake/internal/airplay"
	"github.com/zalando/go-keyring"
)

const keyringService = "aircast"

const keyringTimeout = 3 * time.Second

type credentialResult[T any] struct {
	value T
	err   error
}

// OS keyring APIs do not all support cancellation. Do not let a locked store
// prevent EOF from stopping the engine; the process owns any pending OS call.
func credentialCall[T any](ctx context.Context, call func() (T, error)) (T, error) {
	result := make(chan credentialResult[T], 1)
	go func() {
		value, err := call()
		result <- credentialResult[T]{value: value, err: err}
	}()
	timer := time.NewTimer(keyringTimeout)
	defer timer.Stop()
	select {
	case value := <-result:
		return value.value, value.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case <-timer.C:
		var zero T
		return zero, context.DeadlineExceeded
	}
}

type boundedKeyring struct {
	ctx     context.Context
	backend airplay.CredentialBackend
}

func (b boundedKeyring) Lookup(deviceID string) (*airplay.SavedCredentials, error) {
	return credentialCall(b.ctx, func() (*airplay.SavedCredentials, error) { return b.backend.Lookup(deviceID) })
}

func (b boundedKeyring) Save(deviceID string, saved *airplay.SavedCredentials) error {
	copy := *saved
	copy.Ed25519Public = append([]byte(nil), saved.Ed25519Public...)
	copy.Ed25519Seed = append([]byte(nil), saved.Ed25519Seed...)
	copy.ReceiverPublicKey = append([]byte(nil), saved.ReceiverPublicKey...)
	_, err := credentialCall(b.ctx, func() (struct{}, error) { return struct{}{}, b.backend.Save(deviceID, &copy) })
	return err
}

type osKeyring struct{}

func newOSKeyring() (airplay.CredentialBackend, error) {
	_, err := keyring.Get(keyringService, "__probe__")
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return nil, err
	}
	return osKeyring{}, nil
}

func keyringAccount(deviceID string) string {
	hash := sha256.Sum256([]byte(deviceID))
	return "airplay-" + hex.EncodeToString(hash[:])
}

func (osKeyring) Lookup(deviceID string) (*airplay.SavedCredentials, error) {
	value, err := keyring.Get(keyringService, keyringAccount(deviceID))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var saved airplay.SavedCredentials
	if err := json.Unmarshal([]byte(value), &saved); err != nil {
		return nil, err
	}
	return &saved, nil
}

func (osKeyring) Save(deviceID string, saved *airplay.SavedCredentials) error {
	value, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	return keyring.Set(keyringService, keyringAccount(deviceID), string(value))
}

type pairingStore struct {
	mu      sync.Mutex
	backend airplay.CredentialBackend
	entries map[string]*airplay.SavedCredentials
	writer  *eventWriter
	warned  bool
}

func newPairingStore(backend airplay.CredentialBackend, writer *eventWriter) *pairingStore {
	store := &pairingStore{backend: backend, entries: make(map[string]*airplay.SavedCredentials), writer: writer}
	if backend == nil {
		store.sessionOnly()
	}
	return store
}

func (s *pairingStore) sessionOnly() {
	s.backend = nil
	if !s.warned {
		s.warned = true
		s.writer.warn("OS credential store unavailable. Pairing is session-only; no secrets are saved to disk")
	}
}

func (s *pairingStore) lookupLocked(deviceID string) *airplay.SavedCredentials {
	if saved, ok := s.entries[deviceID]; ok {
		return saved
	}
	var saved *airplay.SavedCredentials
	if s.backend != nil && deviceID != "" {
		var err error
		saved, err = s.backend.Lookup(deviceID)
		if err != nil {
			s.sessionOnly()
		}
	}
	if saved == nil {
		saved = &airplay.SavedCredentials{}
	}
	s.entries[deviceID] = saved
	return saved
}

func (s *pairingStore) lookup(deviceID string) airplay.SavedCredentials {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.lookupLocked(deviceID)
}

func (s *pairingStore) saveLocked(deviceID string, saved *airplay.SavedCredentials) {
	if s.backend == nil {
		return
	}
	if deviceID == "" || s.backend.Save(deviceID, saved) != nil {
		s.sessionOnly()
	}
}

func (s *pairingStore) savePairing(deviceID string, client *airplay.AirPlayClient) {
	keys := client.PairKeys
	if keys == nil || len(keys.Ed25519Private) != ed25519.PrivateKeySize || len(keys.Ed25519Public) != ed25519.PublicKeySize {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	saved := s.lookupLocked(deviceID)
	saved.PairingID = client.PairingID
	saved.Ed25519Public = append([]byte(nil), keys.Ed25519Public...)
	saved.Ed25519Seed = append([]byte(nil), keys.Ed25519Private.Seed()...)
	saved.PairingProtocol = client.PairingProtocol()
	saved.ReceiverPairingID, saved.ReceiverPublicKey = client.ReceiverPairingIdentity()
	s.saveLocked(deviceID, saved)
}

func (s *pairingStore) saveRestoreToken(deviceID, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	saved := s.lookupLocked(deviceID)
	saved.RestoreToken = token
	s.saveLocked(deviceID, saved)
	return nil
}
