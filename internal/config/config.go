package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"aircast/internal/receiver"
)

const maxConfigSize = 1 << 20

type file struct {
	Version int               `json:"version"`
	Saved   []receiver.Device `json:"saved"`
}

func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "aircast", "receivers.json"), nil
}

func Load(path string) ([]receiver.Device, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxConfigSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxConfigSize {
		return nil, errors.New("receiver configuration is larger than 1 MiB")
	}
	var stored file
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("read receiver configuration: %w", err)
	}
	if stored.Version != 1 || len(stored.Saved) > receiver.MaxDevices {
		return nil, errors.New("unsupported receiver configuration")
	}
	seen := make(map[string]bool)
	for _, d := range stored.Saved {
		if err := d.Validate(); err != nil {
			return nil, fmt.Errorf("invalid saved receiver: %w", err)
		}
		if seen[d.ID] {
			return nil, errors.New("duplicate receiver identity in configuration")
		}
		seen[d.ID] = true
	}
	return stored.Saved, nil
}

func Save(path string, devices []receiver.Device) (resultErr error) {
	if len(devices) > receiver.MaxDevices {
		return errors.New("too many saved receivers")
	}
	for _, d := range devices {
		if err := d.Validate(); err != nil {
			return err
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".receivers-*")
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		os.Remove(f.Name())
	}()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(file{Version: 1, Saved: devices}); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
