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

// entry is one URL's resolved state. Filename is empty when HasArt is
// false (a confirmed "no art" result) — there's nothing on disk for it.
type entry struct {
	Filename string `json:"filename"`
	HasArt   bool   `json:"hasArt"`
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
