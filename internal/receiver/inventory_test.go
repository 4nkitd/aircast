package receiver

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func testDevice(t *testing.T, name string) Device {
	t.Helper()
	d, err := Manual(name, "192.168.1.40", AirPlay)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestExpiryPreservesSavedButMarksNotSeen(t *testing.T) {
	now := time.Now()
	i := NewInventory(nil)
	d := testDevice(t, "Living room")
	i.Observe(d, now)
	i.Save(d)
	if list := i.Snapshot(now); len(list) != 1 || !list[0].Online || !list[0].Saved {
		t.Fatalf("not online and saved: %#v", list)
	}
	if list := i.Snapshot(now.Add(DiscoveryLifetime)); len(list) != 1 || list[0].Online {
		t.Fatalf("stale receiver still online: %#v", list)
	}
	i.Forget(d.ID)
	if list := i.Snapshot(now.Add(2 * DiscoveryLifetime)); len(list) != 0 {
		t.Fatalf("stale unsaved receiver retained: %#v", list)
	}
}

func TestManualSaveIsNotDiscoveryOrConnection(t *testing.T) {
	d := testDevice(t, "Manual TV")
	i := NewInventory([]Device{d})
	if list := i.Snapshot(time.Now()); len(list) != 1 || list[0].Online || !list[0].Saved {
		t.Fatalf("loaded receiver advertised as online: %#v", list)
	}
	i.Forget(d.ID)
	if len(i.Saved()) != 0 || len(i.Snapshot(time.Now())) != 0 {
		t.Fatal("manual receiver was not removed")
	}
}

func TestRAOPDoesNotReplaceVideoEndpoint(t *testing.T) {
	now := time.Now()
	video, _ := FromService("_airplay._tcp", "TV", "192.168.1.40", 7000, []string{"deviceid=AA:BB:CC:DD:EE:FF"})
	audio, _ := FromService("_raop._tcp", "AABBCCDDEEFF@TV", "192.168.1.40", 5000, nil)
	for _, order := range [][]Device{{video, audio}, {audio, video}} {
		i := NewInventory(nil)
		for _, d := range order {
			if err := i.Observe(d, now); err != nil {
				t.Fatal(err)
			}
		}
		list := i.Snapshot(now)
		if len(list) != 1 || list[0].Kind != AirPlay || list[0].Port != 7000 {
			t.Fatalf("audio displaced video endpoint: %#v", list)
		}
	}
}

func TestManualTargetMatchesDiscoveredEndpoint(t *testing.T) {
	d := testDevice(t, "Saved TV")
	i := NewInventory([]Device{d})
	discovered, _ := FromService("_airplay._tcp", "TV", d.Host, d.Port, []string{"deviceid=AA:BB:CC:DD:EE:FF"})
	i.Observe(discovered, time.Now())
	list := i.Snapshot(time.Now())
	if len(list) != 1 || !list[0].Saved || !list[0].Online || list[0].ID != discovered.ID || i.ResolveID(d.ID) != discovered.ID {
		t.Fatalf("manual receiver duplicated: %#v", list)
	}
}

func TestDHCPReassignmentDoesNotMergeDifferentHardwareIdentities(t *testing.T) {
	now := time.Now()
	i := NewInventory(nil)
	a, _ := FromService("_airplay._tcp", "A", "192.168.1.40", 7000, []string{"deviceid=AA:BB:CC:DD:EE:01"})
	manual, _ := Manual("Saved A", "192.168.1.40", AirPlay)
	i.Observe(a, now)
	i.Save(manual)
	a.Host = "192.168.1.41"
	i.Observe(a, now.Add(time.Second))
	b, _ := FromService("_airplay._tcp", "B", "192.168.1.40", 7000, []string{"deviceid=AA:BB:CC:DD:EE:02"})
	i.Observe(b, now.Add(2*time.Second))
	i.Save(manual)
	for n := range 3 {
		i.Observe(a, now.Add(time.Duration(n+3)*time.Second))
		i.Observe(b, now.Add(time.Duration(n+3)*time.Second))
	}
	list := i.Snapshot(now.Add(6 * time.Second))
	if len(list) != 2 {
		t.Fatalf("different hardware identities were merged: %#v", list)
	}
	for _, d := range list {
		if d.ID == a.ID && d.Host != "192.168.1.41" || d.ID == b.ID && d.Host != "192.168.1.40" || !d.Saved {
			t.Fatalf("receiver identity or address changed incorrectly: %#v", list)
		}
	}
}

func TestManualEndpointPrefersLiveReceiverOverStaleSavedIdentity(t *testing.T) {
	now := time.Now()
	a, _ := FromService("_airplay._tcp", "Old TV", "192.168.1.40", 7000, []string{"deviceid=AA:BB:CC:DD:EE:01"})
	b, _ := FromService("_airplay._tcp", "New TV", "192.168.1.40", 7000, []string{"deviceid=AA:BB:CC:DD:EE:02"})
	manual, _ := Manual("New TV", "192.168.1.40", AirPlay)
	i := NewInventory([]Device{a})
	i.Observe(a, now.Add(-DiscoveryLifetime-time.Second))
	i.Observe(b, now)
	if err := i.Save(manual); err != nil {
		t.Fatal(err)
	}
	if i.ResolveID(manual.ID) != b.ID {
		t.Fatal("manual endpoint resolved to stale hardware identity")
	}
	list := i.Snapshot(now)
	if len(list) != 2 {
		t.Fatalf("hardware identities merged: %#v", list)
	}
	for _, d := range list {
		if d.ID == b.ID && !d.Saved || d.ID == a.ID && d.Online {
			t.Fatalf("wrong identity saved or marked live: %#v", list)
		}
	}
}

func TestSavingExpiredSnapshotCannotCreatePermanentlyOnlineRecord(t *testing.T) {
	i := NewInventory(nil)
	d := testDevice(t, "TV")
	i.Observe(d, time.Now())
	snapshot := i.Snapshot(time.Now())[0]
	i.mu.Lock()
	r := i.records[d.ID]
	r.seen = time.Now().Add(-DiscoveryLifetime - time.Second)
	i.records[d.ID] = r
	i.mu.Unlock()
	if err := i.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	list := i.Snapshot(time.Now().Add(2 * DiscoveryLifetime))
	if len(list) != 1 || list[0].Online || !list[0].Saved {
		t.Fatalf("stale snapshot became permanently online: %#v", list)
	}
}

func TestSavingDiscoveredEndpointUsesExistingCanonicalIdentity(t *testing.T) {
	now := time.Now()
	i := NewInventory(nil)
	discovered, _ := FromService("_airplay._tcp", "TV", "192.168.1.40", 7000, []string{"deviceid=AA:BB:CC:DD:EE:FF"})
	manual, _ := Manual("My TV", "192.168.1.40", AirPlay)
	i.Observe(discovered, now)
	i.Save(manual)
	list := i.Snapshot(now)
	if len(list) != 1 || !list[0].Saved || !list[0].Online || list[0].ID != discovered.ID || i.ResolveID(manual.ID) != discovered.ID {
		t.Fatalf("duplicate or orphaned saved endpoint: %#v", list)
	}
}

func TestSavedManualAliasesMergeVideoAndAudioInEitherOrder(t *testing.T) {
	now := time.Now()
	video, _ := FromService("_airplay._tcp", "TV", "192.168.1.40", 7000, []string{"deviceid=AA:BB:CC:DD:EE:FF"})
	audio, _ := FromService("_raop._tcp", "AABBCCDDEEFF@TV", "192.168.1.40", 5000, nil)
	for _, kind := range []Kind{AirPlay, Audio} {
		manual, _ := Manual("Saved TV", "192.168.1.40", kind)
		for _, order := range [][]Device{{video, audio}, {audio, video}} {
			i := NewInventory([]Device{manual})
			for n := range 3 {
				for _, d := range order {
					i.Observe(d, now.Add(time.Duration(n)*time.Second))
				}
			}
			list := i.Snapshot(now.Add(3 * time.Second))
			if len(list) != 1 || list[0].Kind != AirPlay || !list[0].Saved || !list[0].Online || i.ResolveID(video.ID) != list[0].ID {
				t.Fatalf("manual %s alias mismatch: %#v", kind, list)
			}
		}
	}
}

func TestInventoryBoundAndConcurrentSnapshots(t *testing.T) {
	i := NewInventory(nil)
	now := time.Now()
	var wg sync.WaitGroup
	for n := range 8 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for k := range 100 {
				d, _ := Manual(fmt.Sprintf("TV %d", n*100+k), fmt.Sprintf("192.168.%d.%d", n, k+1), AirPlay)
				i.Observe(d, now)
				i.Snapshot(now)
			}
		}(n)
	}
	wg.Wait()
	if list := i.Snapshot(now); len(list) != MaxDevices {
		t.Fatalf("inventory bound got %d", len(list))
	}
	if list := i.Snapshot(now.Add(DiscoveryLifetime)); len(list) != 0 {
		t.Fatal("unsaved discovery entries did not expire")
	}
}
