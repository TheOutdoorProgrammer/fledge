package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// plausibleUDID accepts both shapes Apple has used — the 40-character hex form
// from older hardware and the dashed form introduced with the A12 generation —
// and doubles as the guard that keeps a UDID safe to use as a filename.
func plausibleUDID(s string) bool {
	if len(s) < 8 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		hex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !hex && c != '-' {
			return false
		}
	}
	return true
}

// Device is a device that enrolled with Fledge.
type Device struct {
	UDID       string    `json:"udid"`
	Name       string    `json:"name"`
	Product    string    `json:"product,omitempty"`
	OSVersion  string    `json:"os_version,omitempty"`
	Serial     string    `json:"serial,omitempty"`
	Enrolled   time.Time `json:"enrolled"`
	Registered bool      `json:"registered"`
	AppleID    string    `json:"apple_id,omitempty"`
}

// PutDevice records or updates an enrolled device.
func (s *Store) PutDevice(device *Device) error {
	if !plausibleUDID(device.UDID) {
		return errors.New("store: implausible device identifier")
	}
	if device.Enrolled.IsZero() {
		device.Enrolled = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return writeJSON(s.devicePath(device.UDID), device)
}

// Device returns one enrolled device.
func (s *Store) Device(udid string) (*Device, error) {
	if !plausibleUDID(udid) {
		return nil, ErrNotFound
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	raw, err := os.ReadFile(s.devicePath(udid))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	var device Device
	if err := json.Unmarshal(raw, &device); err != nil {
		return nil, err
	}

	return &device, nil
}

// Devices lists every enrolled device, most recent first.
func (s *Store) Devices() ([]*Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(filepath.Join(s.root, "devices"))
	if err != nil {
		return nil, err
	}

	devices := make([]*Device, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.root, "devices", entry.Name()))
		if err != nil {
			continue
		}
		var device Device
		if err := json.Unmarshal(raw, &device); err != nil {
			continue
		}
		devices = append(devices, &device)
	}
	sort.Slice(devices, func(i, j int) bool {
		return devices[i].Enrolled.After(devices[j].Enrolled)
	})

	return devices, nil
}

// DeleteDevice forgets a device locally. It does not free the Apple slot, which
// Apple only reclaims at the start of a membership year.
func (s *Store) DeleteDevice(udid string) error {
	if !plausibleUDID(udid) {
		return ErrNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return os.Remove(s.devicePath(udid))
}

func (s *Store) devicePath(udid string) string {
	return filepath.Join(s.root, "devices", udid+".json")
}
