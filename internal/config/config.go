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

// DefaultElapsedUpdateIntervalSeconds is applied whenever
// OLED.ElapsedUpdateIntervalSeconds is zero (an unconfigured Config, or
// one predating that field) — matches the original hardcoded behavior of
// pushing a fresh elapsed-time update to the display every second while
// playing.
const DefaultElapsedUpdateIntervalSeconds = 1

// OLED holds the Arduino display's serial connection settings. An empty
// Port means "not configured" — the daemon simply doesn't drive a display.
type OLED struct {
	Port string `json:"port"`
	Baud int    `json:"baud"`
	// ElapsedUpdateIntervalSeconds controls how often (while playing) the
	// daemon pushes a fresh elapsed-time update to the display — the
	// display's own progress bar/countdown otherwise only moves once per
	// push, not continuously, so a larger interval trades off how
	// frequently it visibly advances against how much serial traffic it
	// takes to do so. Zero means DefaultElapsedUpdateIntervalSeconds.
	ElapsedUpdateIntervalSeconds int `json:"elapsedUpdateIntervalSeconds"`
}

// PlaybackMode selects how a submitted URL is actually handed to mpd.
type PlaybackMode string

const (
	// ModeStream is the original behavior: mpd streams the remote URL
	// directly, after a cheap reachability check (internal/urlcheck).
	ModeStream PlaybackMode = "stream"
	// ModeBucket downloads the URL into the local bucket cache first
	// (internal/bucket) and hands mpd the resulting local file path —
	// more robust against a flaky remote server mid-playback, at the
	// cost of a startup delay while the download completes.
	ModeBucket PlaybackMode = "bucket"
)

// AllowedModes are the only valid values for Bucket.Mode. The empty string
// (an unconfigured Config's zero value) is treated as ModeStream, not
// listed here since it's never what a client should explicitly send.
var AllowedModes = []PlaybackMode{ModeStream, ModeBucket}

// IsAllowedMode reports whether mode is one of AllowedModes.
func IsAllowedMode(mode PlaybackMode) bool {
	for _, m := range AllowedModes {
		if m == mode {
			return true
		}
	}
	return false
}

// Defaults applied whenever the corresponding Bucket field is zero (an
// unconfigured Config, or one predating that field).
const (
	DefaultBucketMaxSizeMB    = 512  // the evictable playback cache
	DefaultFavoritesMaxSizeMB = 1024 // the permanent favorites archive
)

// Bucket holds the local audio-file cache's settings — see internal/bucket.
// Two independent stores share this section: the evictable playback cache
// (Mode/MaxSizeMB) and the permanent favorites archive (FavoritesMaxSizeMB)
// — favoriting a track never counts against the playback cache's cap, but
// it does have its own ceiling, since "permanent" still shouldn't mean
// "unbounded": once it's full, saving a new favorite fails outright rather
// than evicting an existing one (see internal/bucket.Store's evictable
// flag). There used to also be a MinFreeMB disk-space safety margin shared
// by both stores — removed as redundant once both had their own size caps;
// see internal/bucket.Store's history if a hard SD-card-space floor turns
// out to be needed again independent of either cap.
type Bucket struct {
	Mode      PlaybackMode `json:"mode"`
	MaxSizeMB int          `json:"maxSizeMb"`
	// FavoritesMaxSizeMB caps the permanent favorites archive. Zero means
	// DefaultFavoritesMaxSizeMB.
	FavoritesMaxSizeMB int `json:"favoritesMaxSizeMb"`
}

// UI holds settings for the web frontend's own behavior — distinct from
// OLED/Bucket, which are daemon-side subsystems; UI's fields are simply
// read back by the frontend (GET /api/config, same as the others) and
// applied client-side, the daemon itself does nothing with them.
type UI struct {
	// HideVolumeControl hides the volume slider in PlayerBar/
	// NowPlayingScreen — e.g. for a setup that always runs at a fixed
	// system/amp volume, where the on-screen control just gets in the
	// way. Named so the zero value (false, an unconfigured Config, or
	// one predating this field) preserves the original behavior: shown
	// by default.
	HideVolumeControl bool `json:"hideVolumeControl"`
}

// Config is the full set of daemon settings persisted to disk.
type Config struct {
	OLED   OLED   `json:"oled"`
	Bucket Bucket `json:"bucket"`
	UI     UI     `json:"ui"`
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
