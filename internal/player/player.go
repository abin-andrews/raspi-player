// Package player contains the daemon's playback business logic. It depends
// only on the mpdclient.Client and store.Store interfaces, so it is fully
// unit-testable without a real mpd server or persistent storage.
package player

import (
	"errors"
	"time"

	"pi-streamer/internal/indexer"
	"pi-streamer/internal/mpdclient"
	"pi-streamer/internal/store"
)

// Indexer is the search-indexing dependency a Player can optionally use to
// keep played/favorited/playlisted URLs searchable. Decoupled behind an
// interface (rather than depending on *indexer.Client directly) so it can be
// faked in tests, matching the mpdclient.Client/store.Store pattern.
type Indexer interface {
	IndexURL(url, title, tags string) error
	Search(query string, limit int) ([]indexer.Result, error)
}

// Player ties mpd playback control to Pi-local state tracking (history,
// favorites, playlists) and, optionally, search indexing.
type Player struct {
	mpd     mpdclient.Client
	store   store.Store
	indexer Indexer
}

// New returns a Player driving mpd through mpd and persisting state via st.
// idx may be nil, in which case indexing is skipped and Search returns an
// error.
func New(mpd mpdclient.Client, st store.Store, idx Indexer) *Player {
	return &Player{mpd: mpd, store: st, indexer: idx}
}

// index best-effort submits url/title to the search indexer, if one is
// configured. Failures are ignored: indexing is a nice-to-have, not a
// correctness requirement for playback/favorites/playlists.
func (p *Player) index(url, title string) {
	if p.indexer == nil {
		return
	}
	_ = p.indexer.IndexURL(url, title, "")
}

// PlayURL adds url to the mpd playlist, starts playback, and records the
// play in history.
func (p *Player) PlayURL(url string) error {
	if err := p.mpd.Add(url); err != nil {
		return err
	}
	if err := p.mpd.Play(); err != nil {
		return err
	}
	if err := p.store.AddHistory(store.Track{URL: url, PlayedAt: time.Now()}); err != nil {
		return err
	}
	p.index(url, "")
	return nil
}

// Seek seeks to absolute position seconds within the current song.
func (p *Player) Seek(seconds float64) error {
	return p.mpd.Seek(time.Duration(seconds * float64(time.Second)))
}

// AlbumArt returns album art bytes for the song at url, or nil if none is
// available.
func (p *Player) AlbumArt(url string) ([]byte, error) {
	return p.mpd.AlbumArt(url)
}

// Next skips to the next song in the playlist.
func (p *Player) Next() error {
	return p.mpd.Next()
}

// Previous skips to the previous song in the playlist.
func (p *Player) Previous() error {
	return p.mpd.Previous()
}

// SetVolume sets the output volume, clamped to [0, 100].
func (p *Player) SetVolume(volume int) error {
	if volume < 0 {
		volume = 0
	} else if volume > 100 {
		volume = 100
	}
	return p.mpd.SetVolume(volume)
}

// SeekRelative seeks by relative offset seconds (positive or negative)
// within the current song.
func (p *Player) SeekRelative(seconds float64) error {
	return p.mpd.SeekRelative(time.Duration(seconds * float64(time.Second)))
}

// Search queries the configured search indexer. Returns an error if no
// indexer is configured.
func (p *Player) Search(query string, limit int) ([]indexer.Result, error) {
	if p.indexer == nil {
		return nil, errors.New("player: search indexer not configured")
	}
	return p.indexer.Search(query, limit)
}

// Pause pauses playback.
func (p *Player) Pause() error {
	return p.mpd.Pause()
}

// Resume resumes playback.
func (p *Player) Resume() error {
	return p.mpd.Play()
}

// Status returns the current mpd playback status.
func (p *Player) Status() (mpdclient.Status, error) {
	return p.mpd.Status()
}

// AddFavorite marks url (with an optional title) as a favorite.
func (p *Player) AddFavorite(url, title string) error {
	if err := p.store.AddFavorite(store.Track{URL: url, Title: title}); err != nil {
		return err
	}
	p.index(url, title)
	return nil
}

// RemoveFavorite unmarks url as a favorite.
func (p *Player) RemoveFavorite(url string) error {
	return p.store.RemoveFavorite(url)
}

// Favorites returns all favorited tracks.
func (p *Player) Favorites() ([]store.Track, error) {
	return p.store.Favorites()
}

// CreatePlaylist creates a new, empty named playlist.
func (p *Player) CreatePlaylist(name string) error {
	return p.store.CreatePlaylist(name)
}

// AddToPlaylist appends url (with an optional title) to the named playlist.
func (p *Player) AddToPlaylist(name, url, title string) error {
	if err := p.store.AddToPlaylist(name, store.Track{URL: url, Title: title}); err != nil {
		return err
	}
	p.index(url, title)
	return nil
}

// Playlist returns the tracks in the named playlist.
func (p *Player) Playlist(name string) ([]store.Track, error) {
	return p.store.Playlist(name)
}

// Playlists lists all playlist names.
func (p *Player) Playlists() ([]string, error) {
	return p.store.Playlists()
}

// History returns the most recent plays, most recent first. limit<=0 means
// no cap.
func (p *Player) History(limit int) ([]store.Track, error) {
	return p.store.History(limit)
}

// Queue returns mpd's current live playback queue, distinct from the app's
// own saved named playlists in internal/store.
func (p *Player) Queue() ([]mpdclient.QueueTrack, error) {
	return p.mpd.Queue()
}

// AddToQueue adds url to mpd's playback queue without starting playback and
// without recording a history entry (it's not a play event). It does,
// however, best-effort index the URL, same as PlayURL/AddFavorite/
// AddToPlaylist.
func (p *Player) AddToQueue(url string) error {
	if err := p.mpd.Add(url); err != nil {
		return err
	}
	p.index(url, "")
	return nil
}

// RemoveFromQueue removes the queue entry with the given id.
func (p *Player) RemoveFromQueue(id int) error {
	return p.mpd.RemoveFromQueue(id)
}

// MoveInQueue moves the queue entry with the given id to position.
func (p *Player) MoveInQueue(id, position int) error {
	return p.mpd.MoveInQueue(id, position)
}

// PlayQueueItem starts playback at the queue entry with the given id.
func (p *Player) PlayQueueItem(id int) error {
	return p.mpd.PlayQueueItem(id)
}

// ClearQueue removes all entries from mpd's playback queue.
func (p *Player) ClearQueue() error {
	return p.mpd.ClearQueue()
}
