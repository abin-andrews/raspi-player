// Package store holds Pi-local playback state: last played, favorites,
// history, and named playlists. It is decoupled behind the Store interface
// so callers aren't coupled to a storage choice — today it's in-memory,
// later it could become SQLite-backed without changing callers.
package store

import (
	"fmt"
	"sync"
	"time"
)

// Track represents a single playable item and, where relevant, when it was
// played.
type Track struct {
	URL      string    `json:"url"`
	Title    string    `json:"title"`    // best-effort, may be empty
	PlayedAt time.Time `json:"playedAt"` // zero value if not applicable
}

// ErrPlaylistExists is returned by CreatePlaylist when a playlist with the
// given name already exists.
var ErrPlaylistExists = fmt.Errorf("store: playlist already exists")

// ErrPlaylistNotFound is returned by AddToPlaylist and Playlist when no
// playlist with the given name exists.
var ErrPlaylistNotFound = fmt.Errorf("store: playlist not found")

// Store is Pi-local playback state: play history, favorites, and named
// playlists. Implementations must be safe for concurrent use.
type Store interface {
	// AddHistory records a play event.
	AddHistory(track Track) error
	// History returns the most recent plays first, capped at limit.
	// A limit of 0 or negative means no cap.
	History(limit int) ([]Track, error)

	// AddFavorite marks a track as favorite. Idempotent: adding the same
	// URL twice does not duplicate the entry.
	AddFavorite(track Track) error
	// RemoveFavorite unmarks a track as favorite. Removing a URL that
	// isn't a favorite is not an error.
	RemoveFavorite(url string) error
	// Favorites returns all favorited tracks.
	Favorites() ([]Track, error)

	// CreatePlaylist creates an empty named playlist. Returns
	// ErrPlaylistExists if the name is already taken.
	CreatePlaylist(name string) error
	// AddToPlaylist appends a track to a playlist. Returns
	// ErrPlaylistNotFound if the playlist doesn't exist.
	AddToPlaylist(name string, track Track) error
	// Playlist returns a playlist's tracks in insertion order. Returns
	// ErrPlaylistNotFound if the playlist doesn't exist.
	Playlist(name string) ([]Track, error)
	// Playlists lists all playlist names.
	Playlists() ([]string, error)
}

// memoryStore is an in-memory, goroutine-safe implementation of Store.
type memoryStore struct {
	mu sync.RWMutex

	history   []Track // append-only, oldest first
	favorites []Track // insertion order; URL is the dedup key
	playlists map[string][]Track
}

// NewMemoryStore returns a Store backed by in-memory data structures.
func NewMemoryStore() Store {
	return &memoryStore{
		playlists: make(map[string][]Track),
	}
}

func (s *memoryStore) AddHistory(track Track) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.history = append(s.history, track)
	return nil
}

func (s *memoryStore) History(limit int) ([]Track, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n := len(s.history)
	if limit > 0 && limit < n {
		n = limit
	}

	out := make([]Track, n)
	for i := 0; i < n; i++ {
		// Most recent first: reverse of append order.
		out[i] = s.history[len(s.history)-1-i]
	}
	return out, nil
}

func (s *memoryStore) AddFavorite(track Track) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, f := range s.favorites {
		if f.URL == track.URL {
			return nil // already a favorite; idempotent
		}
	}
	s.favorites = append(s.favorites, track)
	return nil
}

func (s *memoryStore) RemoveFavorite(url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, f := range s.favorites {
		if f.URL == url {
			s.favorites = append(s.favorites[:i], s.favorites[i+1:]...)
			return nil
		}
	}
	return nil // removing a non-favorite is not an error
}

func (s *memoryStore) Favorites() ([]Track, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Track, len(s.favorites))
	copy(out, s.favorites)
	return out, nil
}

func (s *memoryStore) CreatePlaylist(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.playlists[name]; ok {
		return fmt.Errorf("%w: %q", ErrPlaylistExists, name)
	}
	s.playlists[name] = []Track{}
	return nil
}

func (s *memoryStore) AddToPlaylist(name string, track Track) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.playlists[name]; !ok {
		return fmt.Errorf("%w: %q", ErrPlaylistNotFound, name)
	}
	s.playlists[name] = append(s.playlists[name], track)
	return nil
}

func (s *memoryStore) Playlist(name string) ([]Track, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tracks, ok := s.playlists[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrPlaylistNotFound, name)
	}
	out := make([]Track, len(tracks))
	copy(out, tracks)
	return out, nil
}

func (s *memoryStore) Playlists() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]string, 0, len(s.playlists))
	for name := range s.playlists {
		out = append(out, name)
	}
	return out, nil
}
