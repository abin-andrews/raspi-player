// Package config is a small on-disk JSON settings store for the daemon's
// runtime-configurable settings — currently just the OLED display's serial
// connection, but structured so more settings can be added to Config later
// without a new mechanism. The file is meant to be hand-editable directly
// on the Pi; Store.Reload re-reads it from disk so an edit takes effect
// without restarting the daemon.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
)

// OLED holds the Arduino display's serial connection settings. An empty
// Port means "not configured" — the daemon simply doesn't drive a display.
type OLED struct {
	Port string `json:"port"`
	Baud int    `json:"baud"`
}

// Config is the full set of daemon settings persisted to disk.
type Config struct {
	OLED OLED `json:"oled"`
}

// AllowedBauds are the serial baud rates the daemon will accept for the
// OLED display — a fixed set rather than free-form input, since a
// mistyped/unsupported rate just produces garbage bytes on the wire with no
// clear error, instead of a rejected request up front. Exposed to the
// frontend via GET /api/oled/bauds so its dropdown can't drift out of sync
// with what the backend actually allows.
var AllowedBauds = []int{300, 1200, 2400, 4800, 9600, 14400, 19200, 28800, 38400, 57600, 115200, 230400}

// IsAllowedBaud reports whether baud is one of AllowedBauds.
func IsAllowedBaud(baud int) bool {
	for _, b := range AllowedBauds {
		if b == baud {
			return true
		}
	}
	return false
}

// Store is a JSON file-backed Config, safe for concurrent use.
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

// Open loads path into a new Store. A missing file is not an error — the
// Store simply starts out at Config{}'s zero value, as if nothing has been
// configured yet; the file is only created on the first Set.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	return s, nil
}

// Get returns the current in-memory config.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Set replaces the config and persists it to disk.
func (s *Store) Set(cfg Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
	return s.writeLocked()
}

// Reload re-reads the config file from disk, picking up any hand-edits made
// to it since it was last loaded.
func (s *Store) Reload() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.mu.Lock()
		s.cfg = Config{}
		s.mu.Unlock()
		return nil
	}
	if err != nil {
		return err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	return nil
}

func (s *Store) writeLocked() error {
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(s.path, data, 0600)
}
