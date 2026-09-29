// Package api implements the daemon's HTTP command API: submitting a URL to
// play, playback control, and playlist/favorite/history CRUD. Handlers
// depend on a Player-shaped interface (not a concrete type), so they can be
// unit tested against a fake without a real mpd/store behind them.
package api

import (
	"net/http"
	"time"

	"pi-streamer/internal/config"
	"pi-streamer/internal/indexer"
	"pi-streamer/internal/mpdclient"
	"pi-streamer/internal/store"
)

// Player is the subset of internal/player.Player's behavior the HTTP API
// needs.
type Player interface {
	PlayURL(url string) error
	Pause() error
	Resume() error
	Status() (mpdclient.Status, error)
	Seek(seconds float64) error
	Next() error
	Previous() error
	SetVolume(volume int) error
	SeekRelative(seconds float64) error

	AddFavorite(url, title string) error
	RemoveFavorite(url string) error
	Favorites() ([]store.Track, error)

	CreatePlaylist(name string) error
	AddToPlaylist(name, url, title string) error
	Playlist(name string) ([]store.Track, error)
	Playlists() ([]string, error)

	History(limit int) ([]store.Track, error)

	Search(query string, limit int) ([]indexer.Result, error)
	// Library returns every indexed entry, most-recently-indexed first —
	// the "media library" browsing view (no query needed), as opposed to
	// Search's query-driven lookup.
	Library(limit, offset int) ([]indexer.Result, error)
	// AddToLibrary upserts a library entry (add a new URL, or edit an
	// existing one by resubmitting the same URL with changed fields).
	AddToLibrary(url, title, artist, album, tags string) error
	// RemoveFromLibrary deletes url from the search index only — see
	// player.Player.RemoveFromLibrary's doc comment.
	RemoveFromLibrary(url string) error
	// TrackInfo reports the best available title/artist/album for url
	// on demand, without playing or queuing it — see
	// player.Player.TrackInfo's doc comment.
	TrackInfo(url string) (title, artist, album string, err error)

	Queue() ([]mpdclient.QueueTrack, error)
	AddToQueue(url string) error
	RemoveFromQueue(id int) error
	MoveInQueue(id, position int) error
	PlayQueueItem(id int) error
	ClearQueue() error
}

// Config is the subset of internal/config.Store's behavior the HTTP API
// needs — Set persists to disk and applies the change live (e.g.
// reconnecting the OLED display); Reload re-reads the file from disk,
// picking up a hand-edit, and applies it the same way.
type Config interface {
	Get() config.Config
	Set(cfg config.Config) error
	Reload() error
}

// OledStatus reports the live state of the Arduino display's serial
// connection, managed via Config rather than its own separate endpoints —
// this is read-only.
type OledStatus struct {
	Connected bool   `json:"connected"`
	Port      string `json:"port"`
	Baud      int    `json:"baud"`
	Error     string `json:"error,omitempty"`
}

// Oled is the subset of the daemon's OLED connection manager's behavior the
// HTTP API needs.
type Oled interface {
	Status() OledStatus
	ListPorts() ([]string, error)
}

// BucketStatus reports current usage of both the evictable playback cache
// and the permanent favorites archive, for the Settings tab's display.
type BucketStatus struct {
	Mode               string `json:"mode"`
	UsedBytes          int64  `json:"usedBytes"`
	MaxBytes           int64  `json:"maxBytes"`
	FavoritesUsedBytes int64  `json:"favoritesUsedBytes"`
	FavoritesMaxBytes  int64  `json:"favoritesMaxBytes"`
	DiskFreeBytes      int64  `json:"diskFreeBytes"`
	MinFreeBytes       int64  `json:"minFreeBytes"`
}

// BucketEntry describes one file cached in the playback bucket, for the
// Bucket tab's browsing view. URL is empty if this entry predates the
// bucket's URL index (see internal/bucket.Entry).
type BucketEntry struct {
	URL          string    `json:"url"`
	SizeBytes    int64     `json:"sizeBytes"`
	LastAccessed time.Time `json:"lastAccessed"`
}

// Bucket is the subset of the daemon's local audio-file cache's behavior
// the HTTP API needs.
type Bucket interface {
	Status() BucketStatus
	// Query reports, for each of urls, whether it's currently cached in the
	// playback bucket — bulk rather than one endpoint per URL, so a list of
	// tracks needs only one round trip for their "cached" badges.
	Query(urls []string) map[string]bool
	// List returns every entry currently in the evictable playback cache
	// (not the favorites archive — that's already browsable via
	// GET /api/favorites), for the Bucket tab.
	List() ([]BucketEntry, error)
	// Downloads reports every download currently in flight, across both
	// the playback cache and the favorites archive, for a UI to poll and
	// show live progress (on-demand plays, background prefetches, and
	// favorite-archiving all go through this the same way).
	Downloads() []BucketDownload
}

// BucketDownload reports one in-flight download's progress — see
// internal/bucket.Progress, which this mirrors at the API layer.
type BucketDownload struct {
	URL           string `json:"url"`
	ReceivedBytes int64  `json:"receivedBytes"`
	// TotalBytes is 0 if the origin didn't send a Content-Length (common
	// for live/chunked streams) — show a spinner rather than a percentage.
	TotalBytes int64 `json:"totalBytes"`
}

// ArtStatus reports what's known about one track's album art — see Art.
type ArtStatus struct {
	HasArt bool `json:"hasArt"`
	// Path is the static URL to fetch the art from (served by a plain
	// http.FileServer, mounted outside this package — see
	// cmd/pi-streamer/main.go — so repeat requests never reach the Go
	// application at all, not even this router). Empty when HasArt is
	// false.
	Path string `json:"path,omitempty"`
}

// Art is the subset of the daemon's album-art resolution/storage behavior
// the HTTP API needs. The real implementation (cmd/pi-streamer's
// artAdapter) fetches art via mpd on first request and persists it to
// disk (internal/artstore) rather than holding it in the Go process's
// memory — deliberate, given this runs on a 512MB Raspberry Pi Zero 2 W,
// where any in-memory image cache is permanent pressure on an already-tiny
// budget that a disk-backed one plus the OS's own page cache avoids.
type Art interface {
	// Resolve returns the static path to serve url's art from, fetching
	// and persisting it first if url hasn't been checked yet (artist/album
	// are optional hints used only for a MusicBrainz/Cover Art Archive
	// fallback lookup when mpd itself has nothing). ok=false means
	// confirmed no art (the caller should 404, not redirect).
	Resolve(url, artist, album string) (path string, ok bool, err error)
	// Refresh is Resolve but bypassing whatever's already known and
	// always re-resolving — e.g. a file was re-tagged with new art since
	// it was last resolved.
	Refresh(url, artist, album string) (path string, ok bool, err error)
	// Query reports what's already known for each of urls without
	// fetching anything — see Bucket.Query's identical "one request per
	// rendered list" reasoning. A url absent from the result is simply
	// unknown (never resolved yet), not confirmed either way.
	Query(urls []string) map[string]ArtStatus
	// Warm kicks off a background scan (tracked as a Job, see Jobs)
	// pre-resolving art for the whole library, so viewing it for the
	// first time doesn't have to.
	Warm()
	// Suggest returns candidate Title/Artist/Album matches for query,
	// via the same MusicBrainz lookup Resolve/Refresh already use as an
	// album art fallback — surfaced in the Library edit form as
	// suggestions a user can pick from, not applied automatically.
	Suggest(query string) ([]MetadataSuggestion, error)
	// SetCustomArt fetches imageURL (any URL serving an image) and
	// records it as scope's ("track"/"album"/"artist") custom art for
	// key (a track's URL, or an album/artist name) — an explicit
	// fallback for when auto-resolution doesn't find the right thing, or
	// anything at all. Once set, Resolve/Refresh treat it as
	// authoritative for that scope until ClearCustomArt reverts it.
	SetCustomArt(scope, key, imageURL string) (ArtStatus, error)
	// ClearCustomArt removes scope/key's custom art, reverting to
	// whatever auto-resolution finds. Not an error if nothing was set.
	ClearCustomArt(scope, key string) error
}

// MetadataSuggestion is internal/api's own copy of coverart.
// MetadataSuggestion's shape (same "own copy, no direct import" pattern as
// ArtStatus/BucketStatus/Job below) — one candidate Title/Artist/Album
// match for a track being edited.
type MetadataSuggestion struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Album  string `json:"album"`
}

// Job mirrors internal/jobs.Job's shape at the API layer, the same
// "own copy, no direct import" pattern as BucketStatus/BucketEntry above —
// keeps this package decoupled from the concrete job-tracking
// implementation.
type Job struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Total     int       `json:"total,omitempty"`
	Done      int       `json:"done,omitempty"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt,omitempty"`
}

// Jobs is the subset of the daemon's long-running-job tracking
// (internal/jobs.Manager) the HTTP API needs — currently only album art
// warming uses it, but it's a general mechanism for any future background
// task that shouldn't be a silent fire-and-forget goroutine.
type Jobs interface {
	List() []Job
}

// NewRouter builds the HTTP command API, dispatching to p, cfg, o, b, art, and j.
func NewRouter(p Player, cfg Config, o Oled, b Bucket, art Art, j Jobs) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/discover", handleDiscover)
	mux.HandleFunc("POST /api/play", handlePlay(p))
	mux.HandleFunc("POST /api/pause", handlePause(p))
	mux.HandleFunc("POST /api/resume", handleResume(p))
	mux.HandleFunc("GET /api/status", handleStatus(p))
	mux.HandleFunc("POST /api/seek", handleSeek(p))
	mux.HandleFunc("GET /api/albumart", handleAlbumArt(art))
	mux.HandleFunc("POST /api/albumart/query", handleAlbumArtQuery(art))
	mux.HandleFunc("POST /api/albumart/refresh", handleRefreshAlbumArt(art))
	mux.HandleFunc("POST /api/albumart/warm", handleWarmAlbumArt(art))
	mux.HandleFunc("POST /api/albumart/suggest", handleSuggestMetadata(art))
	mux.HandleFunc("POST /api/albumart/custom", handleSetCustomArt(art))
	mux.HandleFunc("DELETE /api/albumart/custom", handleClearCustomArt(art))
	mux.HandleFunc("GET /api/jobs", handleListJobs(j))
	mux.HandleFunc("POST /api/next", handleNext(p))
	mux.HandleFunc("POST /api/previous", handlePrevious(p))
	mux.HandleFunc("POST /api/volume", handleVolume(p))
	mux.HandleFunc("POST /api/seek/relative", handleSeekRelative(p))

	mux.HandleFunc("GET /api/favorites", handleListFavorites(p))
	mux.HandleFunc("POST /api/favorites", handleAddFavorite(p))
	mux.HandleFunc("DELETE /api/favorites", handleRemoveFavorite(p))

	mux.HandleFunc("GET /api/playlists", handleListPlaylists(p))
	mux.HandleFunc("POST /api/playlists", handleCreatePlaylist(p))
	mux.HandleFunc("GET /api/playlists/{name}", handleGetPlaylist(p))
	mux.HandleFunc("POST /api/playlists/{name}", handleAddToPlaylist(p))

	mux.HandleFunc("GET /api/history", handleHistory(p))

	mux.HandleFunc("GET /api/search", handleSearch(p))
	mux.HandleFunc("GET /api/library", handleLibrary(p))
	mux.HandleFunc("GET /api/track", handleTrackInfo(p))
	mux.HandleFunc("POST /api/library", handleAddToLibrary(p))
	mux.HandleFunc("DELETE /api/library", handleRemoveFromLibrary(p))

	mux.HandleFunc("GET /api/queue", handleGetQueue(p))
	mux.HandleFunc("POST /api/queue", handleAddToQueue(p))
	mux.HandleFunc("DELETE /api/queue/{id}", handleRemoveFromQueue(p))
	mux.HandleFunc("POST /api/queue/{id}/move", handleMoveInQueue(p))
	mux.HandleFunc("POST /api/queue/{id}/play", handlePlayQueueItem(p))
	mux.HandleFunc("DELETE /api/queue", handleClearQueue(p))

	mux.HandleFunc("GET /api/config", handleGetConfig(cfg))
	mux.HandleFunc("PUT /api/config", handleSetConfig(cfg, o))
	mux.HandleFunc("POST /api/config/reload", handleReloadConfig(cfg))

	mux.HandleFunc("GET /api/oled/status", handleOledStatus(o))
	mux.HandleFunc("GET /api/oled/ports", handleOledPorts(o))
	mux.HandleFunc("GET /api/oled/bauds", handleOledBauds)

	mux.HandleFunc("GET /api/bucket/status", handleBucketStatus(b))
	mux.HandleFunc("POST /api/bucket/query", handleBucketQuery(b))
	mux.HandleFunc("GET /api/bucket/list", handleBucketList(b))
	mux.HandleFunc("GET /api/bucket/downloads", handleBucketDownloads(b))

	return mux
}
