// The daemon's local audio-file caching: a mode-aware player.Resolver
// (stream a URL directly, or download it into a local cache first — see
// internal/config.PlaybackMode), a player.FavoriteArchiver that
// permanently saves favorited tracks to a separate, uncapped-by-eviction
// store, an api.Bucket adapter exposing both stores' usage to the web UI,
// and prefetching the next queued track ahead of time in bucket mode.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"pi-streamer/internal/api"
	"pi-streamer/internal/bucket"
	"pi-streamer/internal/config"
	"pi-streamer/internal/mpdclient"
	"pi-streamer/internal/player"
	"pi-streamer/internal/urlcheck"
)

// downloadTimeout bounds both an on-demand bucket-mode download (in
// modeResolver.Resolve, blocking the play/queue request) and a background
// one (favorite archiving, prefetching) — generous, since it covers a
// potentially large audio file over a possibly slow connection, but still
// finite so a truly stuck download doesn't hang forever.
const downloadTimeout = 2 * time.Minute

// modeResolver implements player.Resolver: depending on the daemon's live-
// configured playback mode, it either verifies a URL is reachable and
// streams it directly (config.ModeStream), or downloads it into the local
// bucket cache first and hands mpd a URL onto streamBaseURL instead
// (config.ModeBucket) — reusing an already-cached copy via Lookup if one
// exists rather than re-downloading.
//
// streamBaseURL points at bucketFileServer (below), not a raw filesystem
// path: mpd only permits `add`-ing a local file to clients connected via
// its Unix domain socket, which this daemon doesn't use (it dials mpd over
// TCP, on both a dev machine and the Pi itself — see CLAUDE.md). Handing
// mpd an ordinary HTTP URL instead sidesteps that restriction entirely,
// the same way the removed Google Drive integration proxied streams mpd
// couldn't fetch on its own.
type modeResolver struct {
	cfg           *config.Store
	checker       *urlcheck.Checker
	cache         *bucket.Store
	streamBaseURL string
}

func (r *modeResolver) Resolve(url string) (string, error) {
	if r.cfg.Get().Bucket.Mode != config.ModeBucket {
		if err := r.checker.Check(url); err != nil {
			return "", fmt.Errorf("unreachable: %w", err)
		}
		return url, nil
	}

	if path, ok := r.cache.Lookup(url); ok {
		return r.streamURL(path), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()
	path, err := r.cache.Download(ctx, url)
	if err != nil {
		return "", fmt.Errorf("download to bucket: %w", err)
	}
	return r.streamURL(path), nil
}

func (r *modeResolver) streamURL(cachedPath string) string {
	return r.streamBaseURL + "/" + filepath.Base(cachedPath)
}

// Unresolve implements player.Resolver — reverses streamURL using the
// cache's own filename->URL index, so a URI read back from mpd's queue/
// status (which only ever sees what Resolve returned) maps back to the
// real source URL. Anything that isn't one of our own local file-server
// URLs (stream mode, or a track queued before bucket mode was ever
// enabled) passes through unchanged.
func (r *modeResolver) Unresolve(uri string) string {
	name, ok := strings.CutPrefix(uri, r.streamBaseURL+"/")
	if !ok {
		return uri
	}
	if original, ok := r.cache.URLForFilename(name); ok {
		return original
	}
	return uri
}

// bucketFileServer serves cached playback-bucket files back out to mpd, on
// a listener bound to 127.0.0.1 only (see main.go's -bucket-stream-addr) —
// not part of the main API's mux, since it's not meant for browsers or the
// LAN, only for mpd running on the same host. http.ServeFile handles Range
// requests (mpd seeking within a track) and Content-Type detection itself.
type bucketFileServer struct {
	cache *bucket.Store
}

func (s *bucketFileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	path, ok := s.cache.FilePath(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, path)
}

// favoriteArchiver implements player.FavoriteArchiver: it permanently
// downloads a favorited URL into the favorites store in the background.
// Failures are logged, never surfaced to the caller — see
// player.FavoriteArchiver's own doc comment on why.
type favoriteArchiver struct {
	favorites *bucket.Store
}

func (a *favoriteArchiver) Archive(url string) {
	safeGo("archive favorite", func() {
		ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
		defer cancel()
		if _, err := a.favorites.Download(ctx, url); err != nil {
			log.Printf("archive favorite %s: %v", url, err)
		}
	})
}

func (a *favoriteArchiver) Forget(url string) {
	if err := a.favorites.Remove(url); err != nil {
		log.Printf("forget favorite %s: %v", url, err)
	}
}

// bucketAdapter implements internal/api.Bucket, exposing both the playback
// cache's and the favorites archive's usage in one status payload.
type bucketAdapter struct {
	cfg       *config.Store
	cache     *bucket.Store
	favorites *bucket.Store
}

func (b *bucketAdapter) Status() api.BucketStatus {
	used, max, diskFree, err := b.cache.Stats()
	if err != nil {
		log.Printf("bucket stats: %v", err)
	}
	favUsed, favMax, _, err := b.favorites.Stats()
	if err != nil {
		log.Printf("favorites stats: %v", err)
	}

	cfg := b.cfg.Get()
	mode := cfg.Bucket.Mode
	if mode == "" {
		mode = config.ModeStream
	}
	minFreeMB := cfg.Bucket.MinFreeMB
	if minFreeMB == 0 {
		minFreeMB = config.DefaultMinFreeMB
	}

	return api.BucketStatus{
		Mode:               string(mode),
		UsedBytes:          used,
		MaxBytes:           max,
		FavoritesUsedBytes: favUsed,
		FavoritesMaxBytes:  favMax,
		DiskFreeBytes:      diskFree,
		MinFreeBytes:       int64(minFreeMB) * 1024 * 1024,
	}
}

// Query reports a URL as cached if it's in either store — from the UI's
// perspective, "is this available locally" doesn't care which one.
func (b *bucketAdapter) Query(urls []string) map[string]bool {
	result := make(map[string]bool, len(urls))
	for _, u := range urls {
		result[u] = b.cache.Contains(u) || b.favorites.Contains(u)
	}
	return result
}

// List implements internal/api.Bucket — the evictable playback cache only
// (not the favorites archive, which is already browsable via the app's own
// GET /api/favorites/internal/store, keyed by title rather than raw URL).
func (b *bucketAdapter) List() ([]api.BucketEntry, error) {
	entries, err := b.cache.List()
	if err != nil {
		return nil, err
	}
	out := make([]api.BucketEntry, len(entries))
	for i, e := range entries {
		out[i] = api.BucketEntry{URL: e.URL, SizeBytes: e.SizeBytes, LastAccessed: e.LastAccessed}
	}
	return out, nil
}

// Downloads implements internal/api.Bucket — every in-flight download
// across both stores (an on-demand bucket-mode play, a background
// prefetch, or a favorite being archived all go through bucket.Store's
// Download the same way, so this one call surfaces all of them).
func (b *bucketAdapter) Downloads() []api.BucketDownload {
	var out []api.BucketDownload
	for _, p := range b.cache.DownloadProgress() {
		out = append(out, api.BucketDownload{URL: p.URL, ReceivedBytes: p.ReceivedBytes, TotalBytes: p.TotalBytes})
	}
	for _, p := range b.favorites.DownloadProgress() {
		out = append(out, api.BucketDownload{URL: p.URL, ReceivedBytes: p.ReceivedBytes, TotalBytes: p.TotalBytes})
	}
	// bucket.Store.DownloadProgress ranges over a map, so its order is
	// randomized per call — sort for a stable order so a WebSocket
	// broadcaster comparing successive snapshots (see main.go) doesn't
	// mistake reordering for a real change, and so the UI's list doesn't
	// visibly jitter between ticks.
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out
}

// prefetchWindow: prefetchNext is called once the current track has this
// much or less time left.
const prefetchWindow = 15 * time.Second

// prefetchNext downloads the next queued track into the bucket cache ahead
// of time, if the daemon is in bucket mode — so switching from the current
// track to the next never has to wait on a download. Best-effort: any
// failure (unreachable URL, disk full, no next track queued) is logged and
// otherwise ignored; it never affects the currently-playing track, only
// how promptly the next one can start.
func prefetchNext(p *player.Player, cache *bucket.Store, cfgStore *config.Store, currentSongID int) {
	if cfgStore.Get().Bucket.Mode != config.ModeBucket {
		return
	}
	queue, err := p.Queue()
	if err != nil {
		log.Printf("prefetch: get queue: %v", err)
		return
	}
	next := nextQueuedURL(queue, currentSongID)
	if next == "" || cache.Contains(next) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()
	if _, err := cache.Download(ctx, next); err != nil {
		log.Printf("prefetch %s: %v", next, err)
		return
	}
	log.Printf("prefetch: cached %s ahead of playback", next)
}

// nextQueuedURL returns the URL of the queue entry immediately after
// currentSongID, or "" if there isn't one (currentSongID not found, or
// it's the last entry).
func nextQueuedURL(queue []mpdclient.QueueTrack, currentSongID int) string {
	for i, t := range queue {
		if t.ID == currentSongID && i+1 < len(queue) {
			return queue[i+1].URL
		}
	}
	return ""
}
