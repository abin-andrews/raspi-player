// Package api implements the daemon's HTTP command API: submitting a URL to
// play, playback control, and playlist/favorite/history CRUD. Handlers
// depend on a Player-shaped interface (not a concrete type), so they can be
// unit tested against a fake without a real mpd/store behind them.
package api

import (
	"net/http"

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
	AlbumArt(url string) ([]byte, error)
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

	Queue() ([]mpdclient.QueueTrack, error)
	AddToQueue(url string) error
	RemoveFromQueue(id int) error
	MoveInQueue(id, position int) error
	PlayQueueItem(id int) error
	ClearQueue() error
}

// NewRouter builds the HTTP command API, dispatching to p.
func NewRouter(p Player) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/play", handlePlay(p))
	mux.HandleFunc("POST /api/pause", handlePause(p))
	mux.HandleFunc("POST /api/resume", handleResume(p))
	mux.HandleFunc("GET /api/status", handleStatus(p))
	mux.HandleFunc("POST /api/seek", handleSeek(p))
	mux.HandleFunc("GET /api/albumart", handleAlbumArt(p))
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

	mux.HandleFunc("GET /api/queue", handleGetQueue(p))
	mux.HandleFunc("POST /api/queue", handleAddToQueue(p))
	mux.HandleFunc("DELETE /api/queue/{id}", handleRemoveFromQueue(p))
	mux.HandleFunc("POST /api/queue/{id}/move", handleMoveInQueue(p))
	mux.HandleFunc("POST /api/queue/{id}/play", handlePlayQueueItem(p))
	mux.HandleFunc("DELETE /api/queue", handleClearQueue(p))

	return mux
}
