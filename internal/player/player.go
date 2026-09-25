// Package player contains the daemon's playback business logic. It depends
// only on the mpdclient.Client and store.Store interfaces (plus the small
// Indexer/Resolver/FavoriteArchiver interfaces below), so it is fully
// unit-testable without a real mpd server or persistent storage.
package player

import (
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"time"

	"pi-streamer/internal/indexer"
	"pi-streamer/internal/mpdclient"
	"pi-streamer/internal/store"
	"pi-streamer/internal/urlnorm"
)

// Indexer is the search-indexing dependency a Player can optionally use to
// keep played/favorited/playlisted URLs searchable. Decoupled behind an
// interface (rather than depending on *indexer.Client directly) so it can be
// faked in tests, matching the mpdclient.Client/store.Store pattern.
type Indexer interface {
	IndexURL(url, title, artist, album, tags string) error
	Search(query string, limit int) ([]indexer.Result, error)
	// List returns every indexed entry, most-recently-indexed first — the
	// "media library" view (browse everything ever played/favorited/
	// playlisted, no query needed), as opposed to Search's query-driven
	// lookup.
	List(limit, offset int) ([]indexer.Result, error)
	// Get looks up a URL's existing indexed entry by exact match, if any —
	// used to check whether a URL is already known before doing avoidable
	// duplicate work (re-indexing over a good existing title, or
	// re-fetching metadata for a track that's already fully tagged).
	Get(url string) (indexer.Result, bool, error)
}

// MetadataFetcher extracts real title/artist/album tags directly from a
// URL's own file data (e.g. ID3/FLAC/Vorbis tags), independent of mpd ever
// playing it — this is what lets a freshly-added URL show up in the Library
// with real Artist/Album grouping right away, rather than only once mpd
// happens to play it. Decoupled behind an interface (rather than depending
// on internal/metadata.Fetcher directly) so it can be faked in tests and so
// the network-fetch/tag-parsing dependency stays out of this package.
// Real implementation: internal/metadata.Fetcher, wrapped in cmd/pi-streamer.
type MetadataFetcher interface {
	// Fetch returns ok=false if no usable metadata could be extracted
	// (unreachable URL, no embedded tags, a stream format with none, etc)
	// — callers should treat that as "nothing more to add" rather than an
	// error to surface.
	Fetch(url string) (title, artist, album string, ok bool)
}

// Resolver turns a submitted URL into the URI mpd should actually be given,
// verifying it's reachable/streamable in the process — mpd itself has no
// good way to report "the remote server never responded" back through
// Status(), so PlayURL/AddToQueue call this before mpd ever sees the
// original URL, and reject up front with a clear error if it fails. The
// original url (not whatever Resolve returns) is still what's recorded in
// history/favorites/the search index — Resolve's result is only ever
// handed to mpd.Add.
//
// Real implementation: cmd/pi-streamer's mode-aware resolver, which either
// does a cheap reachability check and passes the URL straight through
// (config.ModeStream — wraps internal/urlcheck.Checker), or downloads it
// into the local bucket cache and hands mpd a URL onto a local file server
// instead (config.ModeBucket — wraps internal/bucket.Store), depending on
// the daemon's live-configured playback mode.
type Resolver interface {
	Resolve(url string) (string, error)

	// Unresolve reverses Resolve for a URI read back from mpd (Queue,
	// Status) — mpd only ever sees whatever Resolve returned, never the
	// original URL. Without this, a bucket-mode local file-server URL
	// would get mistaken for the track's real identity by anything
	// reading it back out (the Queue tab, or worse, prefetching "the next
	// URL" and re-downloading the daemon's own served copy under a
	// nonsense key). Implementations that never rewrite URLs (stream mode)
	// just return uri unchanged.
	Unresolve(uri string) string
}

// FavoriteArchiver permanently saves a favorited URL to disk, independent
// of the playback bucket's mode/eviction — see internal/bucket's separate,
// non-evictable instance for this, constructed in cmd/pi-streamer. Archive
// is best-effort and asynchronous: implementations should not block the
// caller or surface an error back to it, matching Indexer's philosophy (a
// favorite is recorded in internal/store regardless of whether it can be
// archived to disk right now). Forget is the inverse, called on
// RemoveFavorite to clean up that permanent copy — it may block briefly
// (it's just a local file delete) and its error is likewise not surfaced;
// forgetting a URL that was never archived (e.g. the download failed, or
// never got a chance to run) is not an error.
type FavoriteArchiver interface {
	Archive(url string)
	Forget(url string)
}

// Player ties mpd playback control to Pi-local state tracking (history,
// favorites, playlists) and, optionally, search indexing.
type Player struct {
	mpd      mpdclient.Client
	store    store.Store
	indexer  Indexer
	resolver Resolver
	archiver FavoriteArchiver
	metadata MetadataFetcher
}

// New returns a Player driving mpd through mpd and persisting state via st.
// idx may be nil, in which case indexing is skipped and Search returns an
// error. resolver may also be nil, in which case PlayURL/AddToQueue hand
// mpd the submitted URL as-is with no reachability check (mainly for tests
// — cmd/pi-streamer always wires a real one). archiver may be nil, in
// which case AddFavorite skips permanent archiving. metadata may be nil, in
// which case newly-added URLs only ever get the thin, derived-from-URL
// title (no async artist/album enrichment).
func New(mpd mpdclient.Client, st store.Store, idx Indexer, resolver Resolver, archiver FavoriteArchiver, metadata MetadataFetcher) *Player {
	return &Player{mpd: mpd, store: st, indexer: idx, resolver: resolver, archiver: archiver, metadata: metadata}
}

// normalizeURL canonicalizes url (see internal/urlnorm) so that trivially
// different representations of the same track (scheme/host case, a
// default port, a missing root slash) collapse onto the same dedup key
// everywhere a URL is used as one — the search index, the bucket cache,
// favorites/playlists/history. Returns an error for an empty or
// unparseable URL rather than silently passing it through.
func normalizeURL(url string) (string, error) {
	return urlnorm.Normalize(url)
}

// index best-effort submits url/title to the search indexer, if one is
// configured, and kicks off async metadata enrichment for brand-new URLs.
// Failures are ignored: indexing is a nice-to-have, not a correctness
// requirement for playback/favorites/playlists.
//
// If url is already indexed, its existing entry is reused rather than
// clobbered: a thin add-time call (empty title, as from PlayURL/AddToQueue)
// never overwrites a real title/artist/album already on file, and no
// redundant metadata fetch is kicked off for a URL that's already fully
// tagged — this is what makes adding the same URL twice (e.g. playing a
// favorite again) a no-op instead of duplicate network work.
func (p *Player) index(url, title string) {
	if p.indexer == nil {
		return
	}

	existing, ok, err := p.indexer.Get(url)
	if err == nil && ok {
		if existing.Artist != "" || existing.Album != "" {
			// Already fully tagged — nothing left to do.
			return
		}
		if title == "" {
			title = existing.Title
		}
	}

	_ = p.indexer.IndexURL(url, title, "", "", "")

	if p.metadata == nil {
		return
	}
	if ok && (existing.Artist != "" || existing.Album != "") {
		return
	}
	go p.enrichMetadata(url, title)
}

// enrichMetadata fetches real title/artist/album tags for url in the
// background and, if found, re-indexes url with them — turning a thin
// add-time entry into a fully-tagged library entry without ever blocking
// the caller of PlayURL/AddToQueue/AddFavorite/AddToPlaylist on a network
// fetch. Runs in its own goroutine with its own panic recovery, since
// nothing else in the call chain is guaranteed to catch a panic here.
func (p *Player) enrichMetadata(url, fallbackTitle string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in metadata enrichment for %s: %v\n%s", url, r, debug.Stack())
		}
	}()

	title, artist, album, ok := p.metadata.Fetch(url)
	if !ok {
		return
	}
	if title == "" {
		title = fallbackTitle
	}
	_ = p.indexer.IndexURL(url, title, artist, album, "")
}

// resolve turns url into what mpd should actually be given, if a resolver
// is configured. See Resolver's own doc comment.
func (p *Player) resolve(url string) (string, error) {
	if p.resolver == nil {
		return url, nil
	}
	playURI, err := p.resolver.Resolve(url)
	if err != nil {
		return "", fmt.Errorf("resolve url: %w", err)
	}
	return playURI, nil
}

// unresolve reverses resolve for a URI read back from mpd, if a resolver is
// configured. See Resolver.Unresolve's doc comment.
func (p *Player) unresolve(uri string) string {
	if p.resolver == nil {
		return uri
	}
	return p.resolver.Unresolve(uri)
}

// PlayURL adds url to the mpd playlist, starts playback, and records the
// play in history.
func (p *Player) PlayURL(url string) error {
	url, err := normalizeURL(url)
	if err != nil {
		return err
	}
	playURI, err := p.resolve(url)
	if err != nil {
		return err
	}
	if err := p.mpd.Add(playURI); err != nil {
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

// Library returns every entry in the search index, most-recently-indexed
// first — the "media library" browsing view. Returns an error if no
// indexer is configured, same as Search.
func (p *Player) Library(limit, offset int) ([]indexer.Result, error) {
	if p.indexer == nil {
		return nil, errors.New("player: search indexer not configured")
	}
	return p.indexer.List(limit, offset)
}

// Pause pauses playback.
func (p *Player) Pause() error {
	return p.mpd.Pause()
}

// Resume resumes playback.
func (p *Player) Resume() error {
	return p.mpd.Play()
}

// Status returns the current mpd playback status. If mpd has no Title tag
// for the current track, Title is filled in with a readable name derived
// from the URL (see deriveTitleFromURL) rather than left blank — Song is
// mpdclient's own Title-or-file value, so it's a reasonable source either
// way (already a real title, or the bare URL to derive from).
func (p *Player) Status() (mpdclient.Status, error) {
	status, err := p.mpd.Status()
	if err != nil {
		return status, err
	}
	status.Song = p.unresolve(status.Song)
	if status.Title == "" && status.Song != "" {
		status.Title = deriveTitleFromURL(status.Song)
	}
	return status, nil
}

// AddFavorite marks url (with an optional title) as a favorite, and
// best-effort archives it permanently to disk (see FavoriteArchiver).
func (p *Player) AddFavorite(url, title string) error {
	url, err := normalizeURL(url)
	if err != nil {
		return err
	}
	if err := p.store.AddFavorite(store.Track{URL: url, Title: title}); err != nil {
		return err
	}
	p.index(url, title)
	if p.archiver != nil {
		p.archiver.Archive(url)
	}
	return nil
}

// RemoveFavorite unmarks url as a favorite, and best-effort removes its
// permanent archived copy from disk (see FavoriteArchiver.Forget).
func (p *Player) RemoveFavorite(url string) error {
	url, err := normalizeURL(url)
	if err != nil {
		return err
	}
	if err := p.store.RemoveFavorite(url); err != nil {
		return err
	}
	if p.archiver != nil {
		p.archiver.Forget(url)
	}
	return nil
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
	url, err := normalizeURL(url)
	if err != nil {
		return err
	}
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
	queue, err := p.mpd.Queue()
	if err != nil {
		return nil, err
	}
	for i := range queue {
		queue[i].URL = p.unresolve(queue[i].URL)
		if queue[i].Title == "" {
			queue[i].Title = deriveTitleFromURL(queue[i].URL)
		}
	}
	return queue, nil
}

// AddToQueue adds url to mpd's playback queue without starting playback and
// without recording a history entry (it's not a play event). It does,
// however, best-effort index the URL, same as PlayURL/AddFavorite/
// AddToPlaylist.
func (p *Player) AddToQueue(url string) error {
	url, err := normalizeURL(url)
	if err != nil {
		return err
	}
	playURI, err := p.resolve(url)
	if err != nil {
		return err
	}
	if err := p.mpd.Add(playURI); err != nil {
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
