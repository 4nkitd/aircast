package receiver

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

const MaxDevices = 256
const DiscoveryLifetime = 70 * time.Second

type record struct {
	device Device
	seen   time.Time
}

type Inventory struct {
	mu      sync.Mutex
	records map[string]record
}

func NewInventory(saved []Device) *Inventory {
	i := &Inventory{records: make(map[string]record)}
	for _, d := range saved {
		i.Save(d)
	}
	return i
}

func (i *Inventory) Observe(d Device, now time.Time) error {
	if err := d.Validate(); err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.expire(now)
	key := i.reconcile(d)
	old, found := i.records[key]
	if !found && len(i.records) >= MaxDevices {
		return errors.New("receiver limit reached")
	}
	d.ID, d.Saved, d.Online = key, old.device.Saved, true
	if found && old.device.Kind == AirPlay && d.Kind == Audio {
		d = old.device
		d.Online = true
	}
	i.records[key] = record{device: d, seen: now}
	return nil
}

func (i *Inventory) Save(d Device) error {
	if err := d.Validate(); err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.expire(time.Now())
	key := i.reconcile(d)
	old, found := i.records[key]
	if !found && len(i.records) >= MaxDevices {
		return errors.New("receiver limit reached")
	}
	if found {
		d = old.device
	} else {
		d.Online = false
	}
	d.ID = key
	d.Saved = true
	i.records[key] = record{device: d, seen: old.seen}
	return nil
}

func (i *Inventory) ResolveID(id string) string {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.expire(time.Now())
	return i.resolve(id)
}

func (i *Inventory) resolve(id string) string {
	if key := i.endpointKey(id); key != "" {
		return key
	}
	return id
}

func endpointID(d Device) string { return string(d.Kind) + ":" + d.Endpoint() }

func (i *Inventory) endpointKey(endpoint string) string {
	key := ""
	var chosen record
	for id, r := range i.records {
		if endpointID(r.device) != endpoint {
			continue
		}
		if key == "" || r.device.Online && !chosen.device.Online || r.device.Online == chosen.device.Online && (r.seen.After(chosen.seen) || r.seen.Equal(chosen.seen) && id < key) {
			key, chosen = id, r
		}
	}
	return key
}

func (i *Inventory) reconcile(d Device) string {
	key := d.ID
	if d.ID == endpointID(d) {
		if current := i.endpointKey(d.ID); current != "" {
			key = current
		}
	}
	for id, old := range i.records {
		if old.device.Host != d.Host || old.device.Port != d.Port || old.device.Kind != d.Kind || id == key {
			continue
		}
		// A reused DHCP address is not evidence that two hardware identities
		// are the same device. Only absorb address-only records here.
		if key != endpointID(d) && id != endpointID(old.device) {
			continue
		}
		keep, exists := i.records[key]
		if !exists {
			keep = old
			keep.device.ID = key
		}
		discard := old
		saved := keep.device.Saved || discard.device.Saved
		online := keep.device.Online || discard.device.Online
		if discard.device.Kind == AirPlay && keep.device.Kind == Audio {
			keep.device = discard.device
			keep.device.ID = key
		}
		keep.device.Saved = saved
		keep.device.Online = online
		if discard.seen.After(keep.seen) {
			keep.seen = discard.seen
		}
		i.records[key] = keep
		delete(i.records, id)
	}
	return key
}

func (i *Inventory) remove(id string) {
	delete(i.records, id)
}

func (i *Inventory) Forget(id string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	id = i.resolve(id)
	old, found := i.records[id]
	if !found {
		return
	}
	old.device.Saved = false
	if old.seen.IsZero() {
		i.remove(id)
	} else {
		i.records[id] = old
	}
}

func (i *Inventory) Snapshot(now time.Time) []Device {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.expire(now)
	out := make([]Device, 0, len(i.records))
	for _, r := range i.records {
		out = append(out, r.device)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Saved != out[b].Saved {
			return out[a].Saved
		}
		if out[a].Online != out[b].Online {
			return out[a].Online
		}
		if strings.EqualFold(out[a].Name, out[b].Name) {
			return out[a].ID < out[b].ID
		}
		return strings.ToLower(out[a].Name) < strings.ToLower(out[b].Name)
	})
	return out
}

func (i *Inventory) Saved() []Device {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]Device, 0)
	for _, r := range i.records {
		if r.device.Saved {
			out = append(out, r.device)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out
}

func (i *Inventory) expire(now time.Time) {
	for id, r := range i.records {
		if r.seen.IsZero() || now.Sub(r.seen) < DiscoveryLifetime {
			continue
		}
		if !r.device.Saved {
			i.remove(id)
			continue
		}
		r.device.Online = false
		i.records[id] = r
	}
}
