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
	"sync"
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
	// Delete removes url from the index — used by RemoveFromLibrary.
	Delete(url string) error
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

	// enrichCache caches the search index's answer for every URL Status/
	// Queue have asked about, so neither one makes a fresh indexer.Get
	// call on every single invocation (Status is polled once a second
	// while playing, per cmd/pi-streamer's ticker) — see
	// lookupEnrichment's doc comment. Keyed by URL rather than a single
	// slot (an earlier version only ever cached "the current song," which
	// worked for Status but gave Queue nothing to use for its many
	// simultaneously-displayed tracks) — see lookupEnrichment's own doc
	// comment for the resulting unbounded-growth trade-off.
	enrichMu    sync.Mutex
	enrichCache map[string]enrichedResult
}

// enrichedResult is one lookupEnrichment cache entry: resolved is false
// while a background refreshEnrichment call for this URL is still in
// flight (or hasn't been kicked off by anything other than the very call
// that just added this entry) — result is the zero value until then.
type enrichedResult struct {
	result   indexer.Result
	resolved bool
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
// Runs entirely in the background (its own goroutine) — indexing is a
// nice-to-have, not a correctness requirement for playback/favorites/
// playlists, and PlayURL/AddToQueue/AddFavorite/AddToPlaylist must not
// block their caller (and delay the moment playback actually starts) on
// however long the search-indexer service takes to respond, especially
// since checking for an existing entry (below) means a brand-new URL now
// costs *two* sequential network round-trips, not one.
func (p *Player) index(url, title string) {
	if p.indexer == nil {
		return
	}
	go p.indexAsync(url, title)
}

// indexAsync does the actual work described in index's doc comment. Runs
// in its own goroutine with its own panic recovery, since nothing else in
// the call chain would catch one here.
//
// If url is already indexed, its existing entry is reused rather than
// clobbered: a thin add-time call (empty title, as from PlayURL/AddToQueue)
// never overwrites a real title/artist/album already on file, and no
// redundant metadata fetch is kicked off for a URL that's already fully
// tagged — this is what makes adding the same URL twice (e.g. playing a
// favorite again) a no-op instead of duplicate network work.
func (p *Player) indexAsync(url, title string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in indexing for %s: %v\n%s", url, r, debug.Stack())
		}
	}()

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

// PlayURL adds url to the mpd queue, moves it to the front, and jumps
// playback to it immediately — whatever was already playing stops and this
// track starts right away, rather than just being appended behind it. Uses
// mpd's own addid+moveid+playid rather than add+play: add+play (still used
// by AddToQueue, which deliberately does NOT want this jump-to behavior)
// only ever resumes/starts mpd's existing "current song" pointer, which is
// wherever the queue already was, not the URL just appended to the end of
// it — playid is mpd's actual "play this one now" primitive, and it's what
// makes the switch instant rather than needing an explicit Stop() first.
func (p *Player) PlayURL(url string) error {
	url, err := normalizeURL(url)
	if err != nil {
		return err
	}
	playURI, err := p.resolve(url)
	if err != nil {
		return err
	}
	id, err := p.mpd.AddGetID(playURI)
	if err != nil {
		return err
	}
	if err := p.mpd.MoveInQueue(id, 0); err != nil {
		return err
	}
	if err := p.mpd.PlayQueueItem(id); err != nil {
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

// AddToLibrary upserts url into the search index with the given title,
// artist, album, and tags — a synchronous, user-initiated add/edit action
// (distinct from index/indexAsync's fire-and-forget best-effort path used
// by PlayURL/AddToQueue/AddFavorite/AddToPlaylist). If title, artist, and
// album are all empty and a MetadataFetcher is configured, it tries a
// synchronous metadata fetch first (bounded by MetadataFetcher's own
// timeout) so the entry shows up correctly tagged immediately instead of
// blank — blocking here is fine, unlike in PlayURL, because the caller of
// this method is deliberately waiting on it to complete. Returns an error
// if no indexer is configured, same as Search/Library.
func (p *Player) AddToLibrary(url, title, artist, album, tags string) error {
	if p.indexer == nil {
		return errors.New("player: search indexer not configured")
	}
	url, err := normalizeURL(url)
	if err != nil {
		return err
	}
	if title == "" && artist == "" && album == "" && p.metadata != nil {
		if fTitle, fArtist, fAlbum, ok := p.metadata.Fetch(url); ok {
			title, artist, album = fTitle, fArtist, fAlbum
		}
	}
	if err := p.indexer.IndexURL(url, title, artist, album, tags); err != nil {
		return err
	}
	p.invalidateEnrichment(url)
	return nil
}

// RemoveFromLibrary deletes url from the search index only — it does not
// touch Favorites/History/Queue/Bucket, which are intentionally separate
// stores. If the same URL is played/favorited again later, it's simply
// re-indexed via IndexURL's existing upsert behavior. Returns an error if
// no indexer is configured, same as Search/Library.
func (p *Player) RemoveFromLibrary(url string) error {
	if p.indexer == nil {
		return errors.New("player: search indexer not configured")
	}
	url, err := normalizeURL(url)
	if err != nil {
		return err
	}
	return p.indexer.Delete(url)
}

// Pause pauses playback.
func (p *Player) Pause() error {
	return p.mpd.Pause()
}

// Resume resumes playback.
func (p *Player) Resume() error {
	return p.mpd.Play()
}

// Status returns the current mpd playback status, with Title/Artist/Album
// enriched from the search index (see enrichStatus) so a user-edited entry
// (Player.AddToLibrary) is reflected everywhere "now playing" is shown —
// the web UI, and the OLED display — not just mpd's own embedded file
// tags, which AddToLibrary never touches. If, even after that, there's
// still no Title, one is filled in with a readable name derived from the
// URL (see deriveTitleFromURL) rather than left blank.
func (p *Player) Status() (mpdclient.Status, error) {
	status, err := p.mpd.Status()
	if err != nil {
		return status, err
	}
	status.Song = p.unresolve(status.Song)
	status = p.enrichStatus(status)
	if status.Title == "" && status.Song != "" {
		status.Title = deriveTitleFromURL(status.Song)
	}
	return status, nil
}

// enrichStatus overrides status.Title/Artist/Album with the search index's
// values for status.Song via lookupEnrichment, if the index has a
// non-empty value for that field — a deliberate user edit (AddToLibrary)
// should win over whatever's embedded in the file itself, which is all
// mpd's own Status/CurrentSong can ever report, and which AddToLibrary
// only ever updates in the index, never in the file. See lookupEnrichment
// for why this never blocks Status on the indexer's response time.
func (p *Player) enrichStatus(status mpdclient.Status) mpdclient.Status {
	result, resolved := p.lookupEnrichment(status.Song)
	if !resolved {
		return status
	}
	if result.Title != "" {
		status.Title = result.Title
	}
	if result.Artist != "" {
		status.Artist = result.Artist
	}
	if result.Album != "" {
		status.Album = result.Album
	}
	return status
}

// lookupEnrichment returns the search index's cached answer for url, if
// already known, and kicks off a background lookup the first time url is
// ever asked about — never blocking the caller on the indexer's response
// time. Shared by Status (a single "currently playing" URL, polled once a
// second while playing per cmd/pi-streamer's ticker) and Queue (every
// currently queued URL, potentially many at once) — both need the exact
// same "override mpd's own tag/URL-derived fallback with the index's
// title/artist/album, once known" behavior, and calling Indexer.Get
// directly from either on every invocation would gate that call's
// throughput on the search-indexer service's own latency/availability —
// exactly the class of bug already fixed once for the play-time indexing
// path (see indexAsync's doc comment).
//
// resolved=false (a cache miss, or a lookup still in flight) means the
// caller should keep whatever it already had (mpd's own tag, or a
// URL-derived fallback) — the enriched value shows up on a later call once
// the background lookup finishes, typically well under either caller's
// poll interval for a healthy indexer.
//
// enrichCache is keyed by URL and never pruned, unlike an earlier version
// that only ever cached a single "current song" slot — deliberate: Queue
// can have many simultaneously-displayed tracks, each needing its own
// cached answer, and a personal-scale library's worth of distinct URLs
// accumulated over one daemon run (each entry a few small strings) isn't
// worth adding LRU eviction for.
func (p *Player) lookupEnrichment(url string) (indexer.Result, bool) {
	if p.indexer == nil || url == "" {
		return indexer.Result{}, false
	}

	p.enrichMu.Lock()
	entry, cached := p.enrichCache[url]
	if !cached {
		if p.enrichCache == nil {
			p.enrichCache = make(map[string]enrichedResult)
		}
		p.enrichCache[url] = enrichedResult{}
		p.enrichMu.Unlock()
		go p.refreshEnrichment(url)
		return indexer.Result{}, false
	}
	p.enrichMu.Unlock()
	return entry.result, entry.resolved
}

// refreshEnrichment looks url up in the search index and caches the
// answer for lookupEnrichment.
func (p *Player) refreshEnrichment(url string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in metadata enrichment lookup for %s: %v\n%s", url, r, debug.Stack())
		}
	}()

	result, ok, err := p.indexer.Get(url)
	if err != nil || !ok {
		return
	}

	p.enrichMu.Lock()
	defer p.enrichMu.Unlock()
	p.enrichCache[url] = enrichedResult{result: result, resolved: true}
}

// invalidateEnrichment forces a fresh index lookup for url on its next
// Status()/Queue() call, if url is already cached at all. Needed because
// lookupEnrichment's cache otherwise only ever gets populated the first
// time a URL is seen — editing a track's metadata (AddToLibrary) doesn't
// go through lookupEnrichment itself, so without this, an edit would never
// show up until the process restarted (there's no "song changed" event to
// naturally trigger a re-lookup the way there once was for a single-slot
// cache), which is exactly the bug this whole enrichment mechanism was
// built to fix in the first place.
func (p *Player) invalidateEnrichment(url string) {
	p.enrichMu.Lock()
	_, cached := p.enrichCache[url]
	p.enrichMu.Unlock()
	if cached {
		go p.refreshEnrichment(url)
	}
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
// own saved named playlists in internal/store. Every entry's Title/Artist
// gets the exact same search-index-enrichment treatment Status's Title/
// Artist/Album already gets (see enrichStatus/lookupEnrichment) — a
// deliberate user edit (AddToLibrary) should win over mpd's own tag in the
// Queue view too, not just in "now playing"; only once that has nothing
// does an entry fall back to deriveTitleFromURL, same ordering as Status.
// This matters well beyond metadata edits: mpd's own Title tag is often
// simply empty for a URL it has no embedded tags for, and deriving a
// fallback straight from the URL produces useless results for some
// sources (e.g. a bare YouTube watch link's path is just "/watch") where
// the search index, once its own async enrichment completes, has a real
// title to offer instead.
func (p *Player) Queue() ([]mpdclient.QueueTrack, error) {
	queue, err := p.mpd.Queue()
	if err != nil {
		return nil, err
	}
	for i := range queue {
		queue[i].URL = p.unresolve(queue[i].URL)
		if result, resolved := p.lookupEnrichment(queue[i].URL); resolved {
			if result.Title != "" {
				queue[i].Title = result.Title
			}
			if result.Artist != "" {
				queue[i].Artist = result.Artist
			}
		}
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
