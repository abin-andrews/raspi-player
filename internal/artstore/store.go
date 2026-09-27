// Package artstore persists resolved album art to disk, content-addressed
// by the track's URL, and tracks which URLs are confirmed to have art and
// which are confirmed not to — a small on-disk index (loaded entirely into
// memory, but it's just filenames and a bool per entry, never image
// bytes). Real image bytes are written once and then served straight from
// disk by a static http.FileServer (see cmd/pi-streamer), never touching
// this package or the Go process's memory again on repeat requests. This
// matters on a 512MB Raspberry Pi Zero 2 W: even a small bounded in-memory
// byte cache is permanent pressure on an already-tiny budget, whereas a
// few KB of index metadata plus whatever the OS's own page cache decides
// to keep hot is not.
package artstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

const indexFileName = ".index.json"

// entry is one key's resolved state. Filename is empty when HasArt is
// false (a confirmed "no art" result) — there's nothing on disk for it.
// Custom marks an entry set via PutCustom (a user-provided image, fetched
// from a URL they gave rather than found via mpd/YouTube/MusicBrainz) —
// callers doing auto-resolution (cmd/pi-streamer's artAdapter) check this
// so a deliberate user choice is never silently overwritten by a later
// Resolve/Refresh/Warm.
type entry struct {
	Filename string `json:"filename"`
	HasArt   bool   `json:"hasArt"`
	Custom   bool   `json:"custom,omitempty"`
}

// Store is safe for concurrent use.
type Store struct {
	dir string

	mu    sync.RWMutex
	index map[string]entry
}

// Open opens (or creates) a Store rooted at dir, loading its persisted
// index if one exists. dir is resolved to an absolute path — a *relative*
// directory handed straight to os.MkdirAll/os.WriteFile would still work
// here (unlike internal/bucket's mpd-facing case), but resolving it up
// front keeps Dir()'s result stable regardless of the process's current
// working directory at call time.
func Open(dir string) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("artstore: resolve dir %q: %w", dir, err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("artstore: create dir %q: %w", abs, err)
	}
	return &Store{dir: abs, index: loadIndex(abs)}, nil
}

// Dir returns the absolute directory this Store persists art files to —
// what a static http.FileServer(http.Dir(...)) should be pointed at.
func (s *Store) Dir() string {
	return s.dir
}

// Lookup reports what's already known about url without fetching
// anything. known is false if url has never been resolved (see Put).
func (s *Store) Lookup(url string) (filename string, hasArt bool, known bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.index[url]
	if !ok {
		return "", false, false
	}
	return e.Filename, e.HasArt, true
}

// Put records the result of resolving url. data is the fetched art bytes;
// an empty/nil data records a confirmed "no art" result (no file is
// written) rather than an error — callers that couldn't determine an
// answer at all (a transient fetch failure) should not call Put, so that
// case stays "unknown" rather than being recorded as either outcome.
func (s *Store) Put(url string, data []byte) (filename string, hasArt bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(data) == 0 {
		s.index[url] = entry{}
		if err := s.saveIndexLocked(); err != nil {
			return "", false, err
		}
		return "", false, nil
	}

	name := keyFor(url) + extFor(data)
	if err := os.WriteFile(filepath.Join(s.dir, name), data, 0o644); err != nil {
		return "", false, fmt.Errorf("artstore: write %q: %w", name, err)
	}
	s.index[url] = entry{Filename: name, HasArt: true}
	if err := s.saveIndexLocked(); err != nil {
		return "", false, err
	}
	return name, true, nil
}

func keyFor(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:])
}

// PutCustom records data as key's art, marked Custom so IsCustom(key)
// reports true for it afterward — a user-provided override (fetched from
// a URL they gave, not found via auto-resolution) that callers doing
// auto-resolution should treat as authoritative and never silently
// replace. Unlike Put, empty data is rejected as an error rather than
// recorded as a confirmed "no art" result: setting custom art is always
// meant to end with real art in place, so an empty fetch is a failure,
// not an answer worth caching.
func (s *Store) PutCustom(key string, data []byte) (filename string, err error) {
	if len(data) == 0 {
		return "", fmt.Errorf("artstore: no image data to set as custom art for %q", key)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	name := keyFor(key) + extFor(data)
	if err := os.WriteFile(filepath.Join(s.dir, name), data, 0o644); err != nil {
		return "", fmt.Errorf("artstore: write %q: %w", name, err)
	}
	s.index[key] = entry{Filename: name, HasArt: true, Custom: true}
	if err := s.saveIndexLocked(); err != nil {
		return "", err
	}
	return name, nil
}

// IsCustom reports whether key's currently-recorded entry (if any) was
// set via PutCustom rather than auto-resolved via Put.
func (s *Store) IsCustom(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.index[key].Custom
}

// Remove deletes key's recorded entry (custom or not) and its file, if
// any, reverting it to "unknown" — a plain Put/PutCustom-less absence,
// not a confirmed "no art" result — so the next Resolve re-runs
// auto-resolution from scratch instead of treating it as already
// answered. Not an error if key was never recorded.
func (s *Store) Remove(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.index[key]
	if !ok {
		return nil
	}
	if e.Filename != "" {
		if err := os.Remove(filepath.Join(s.dir, e.Filename)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("artstore: remove %q: %w", e.Filename, err)
		}
	}
	delete(s.index, key)
	return s.saveIndexLocked()
}

// extFor picks a file extension so the static file server (which maps
// extension -> Content-Type by filename, not by sniffing bytes) serves
// the right Content-Type. Falls back to .jpg for anything undetected or
// unrecognized, since that's overwhelmingly the common case for embedded
// covers and mpd's own albumart fallback alike.
func extFor(data []byte) string {
	switch http.DetectContentType(data) {
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}

func loadIndex(dir string) map[string]entry {
	data, err := os.ReadFile(filepath.Join(dir, indexFileName))
	if err != nil {
		return map[string]entry{}
	}
	var index map[string]entry
	if err := json.Unmarshal(data, &index); err != nil {
		return map[string]entry{}
	}
	return index
}

func (s *Store) saveIndexLocked() error {
	data, err := json.Marshal(s.index)
	if err != nil {
		return fmt.Errorf("artstore: marshal index: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, indexFileName), data, 0o644); err != nil {
		return fmt.Errorf("artstore: write index: %w", err)
	}
	return nil
}
