package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"aircast/internal/receiver"
)

func TestSaveLoadReplaceAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "receivers.json")
	d, _ := receiver.Manual("Living room", "192.168.1.40", receiver.AirPlay)
	d.Online, d.Saved = true, true
	if err := Save(path, []receiver.Device{d}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "Online") || strings.Contains(string(data), "Saved") {
		t.Fatal("transient status was persisted")
	}
	loaded, err := Load(path)
	if err != nil || len(loaded) != 1 || loaded[0].Name != "Living room" || loaded[0].Online {
		t.Fatalf("loaded %#v, %v", loaded, err)
	}
	d.Name = "Bedroom"
	if err := Save(path, []receiver.Device{d}); err != nil {
		t.Fatal(err)
	}
	loaded, _ = Load(path)
	if loaded[0].Name != "Bedroom" {
		t.Fatal("replacement did not persist")
	}
	info, _ := os.Stat(path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("permissions %o", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatal("temporary configuration files leaked")
	}
}

func TestLoadMissingAndRejectCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receivers.json")
	if devices, err := Load(path); err != nil || len(devices) != 0 {
		t.Fatalf("missing config %#v %v", devices, err)
	}
	for _, data := range []string{"broken", `{"version":2}`, `{"version":1,"saved":[{"id":"x","name":"TV","kind":"bad"}]}`, strings.Repeat(" ", maxConfigSize+1)} {
		os.WriteFile(path, []byte(data), 0600)
		if _, err := Load(path); err == nil {
			t.Fatal("accepted corrupted or oversized configuration")
		}
	}
}
