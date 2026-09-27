// Album art resolution: fetches via mpd on first request (falling back to
// MusicBrainz + the Cover Art Archive if mpd has nothing and artist/album
// are known) and persists to disk (internal/artstore) rather than holding
// it in the Go process's memory — see internal/api.Art's doc comment for
// why that matters on a 512MB Raspberry Pi Zero 2 W. Resolved art is
// served back out by a plain http.FileServer mounted at /art/ in main.go,
// not by this adapter or any other application code, so repeat requests
// never reach the daemon's own logic at all.
package main

import (
	"context"
	"log"

	"pi-streamer/internal/api"
	"pi-streamer/internal/artstore"
	"pi-streamer/internal/indexer"
	"pi-streamer/internal/jobs"
)

// albumArtFetcher is the narrow slice of *mpdclient.GompdClient's
// behavior artAdapter needs — matches the "package defines the interface
// it needs, concrete types satisfy it structurally" pattern used
// throughout this codebase.
type albumArtFetcher interface {
	AlbumArt(url string) ([]byte, error)
}

// coverArtFetcher is the narrow slice of *coverart.Fetcher's behavior
// artAdapter needs.
type coverArtFetcher interface {
	Fetch(ctx context.Context, artist, album string) ([]byte, error)
}

// libraryLister is the narrow slice of *player.Player's behavior
// artAdapter's Warm needs.
type libraryLister interface {
	Library(limit, offset int) ([]indexer.Result, error)
}

// artAdapter implements internal/api.Art.
type artAdapter struct {
	mpd      albumArtFetcher
	coverArt coverArtFetcher // may be nil — MusicBrainz fallback is then skipped entirely
	library  libraryLister
	store    *artstore.Store
	jobs     *jobs.Manager
}

// resolve is Resolve/Refresh's shared implementation. When force is false
// and url is already known, it returns the cached answer without touching
// mpd or MusicBrainz at all. artist/album are optional hints used only for
// the MusicBrainz fallback when mpd itself has nothing.
func (a *artAdapter) resolve(url, artist, album string, force bool) (path string, ok bool, err error) {
	if !force {
		if name, hasArt, known := a.store.Lookup(url); known {
			if !hasArt {
				return "", false, nil
			}
			return "/art/" + name, true, nil
		}
	}

	data, err := a.mpd.AlbumArt(url)
	if err != nil {
		return "", false, err
	}

	if len(data) == 0 && a.coverArt != nil && artist != "" && album != "" {
		// mpd definitively has nothing (see GompdClient.AlbumArt's
		// mpd.Error-unwrapping) — try MusicBrainz + the Cover Art
		// Archive before giving up. A fallback-lookup failure is
		// swallowed (logged, not returned) rather than failing the
		// whole resolve: mpd already gave a confirmed answer, and an
		// external service being unreachable shouldn't turn that into
		// an error response or (worse) prevent caching mpd's own
		// confirmed-no-art result.
		if fallback, fbErr := a.coverArt.Fetch(context.Background(), artist, album); fbErr != nil {
			log.Printf("album art: musicbrainz/coverartarchive fallback for %q (%s — %s): %v", url, artist, album, fbErr)
		} else {
			data = fallback
		}
	}

	name, hasArt, err := a.store.Put(url, data)
	if err != nil {
		return "", false, err
	}
	if !hasArt {
		return "", false, nil
	}
	return "/art/" + name, true, nil
}

func (a *artAdapter) Resolve(url, artist, album string) (string, bool, error) {
	return a.resolve(url, artist, album, false)
}

// Refresh bypasses whatever's already known and always re-resolves —
// e.g. a file was re-tagged with new art since it was last resolved, or a
// MusicBrainz fallback should be retried now that it's configured.
func (a *artAdapter) Refresh(url, artist, album string) (string, bool, error) {
	return a.resolve(url, artist, album, true)
}

func (a *artAdapter) Query(urls []string) map[string]api.ArtStatus {
	out := make(map[string]api.ArtStatus, len(urls))
	for _, u := range urls {
		name, hasArt, known := a.store.Lookup(u)
		if !known {
			continue
		}
		status := api.ArtStatus{HasArt: hasArt}
		if hasArt {
			status.Path = "/art/" + name
		}
		out[u] = status
	}
	return out
}

// Warm walks the whole library resolving anything unknown, tracked as a
// jobs.Job so its progress is visible rather than a silent background
// goroutine — jobs.Manager itself guards against needing our own
// overlap-prevention flag (each call just starts a new tracked job; the
// frontend/Settings UI is what actually prevents piling up duplicate
// warm runs by disabling its button while one is in flight).
func (a *artAdapter) Warm() {
	a.jobs.Start("Warm album art cache", func(h *jobs.Handle) error {
		const pageSize = 100
		offset := 0
		discovered := 0
		for {
			results, err := a.library.Library(pageSize, offset)
			if err != nil {
				return err
			}
			// Library doesn't report a grand total up front, so this
			// grows page by page rather than being exact from the start —
			// still a meaningful "at least this many, so far" denominator
			// for a progress display, and exact once the last page is seen.
			discovered += len(results)
			h.SetTotal(discovered)
			if len(results) == 0 {
				return nil
			}
			for _, res := range results {
				if _, _, known := a.store.Lookup(res.URL); !known {
					if _, _, err := a.resolve(res.URL, res.Artist, res.Album, false); err != nil {
						log.Printf("album art warm: resolve %q: %v", res.URL, err)
					}
				}
				h.Advance(1)
			}
			if len(results) < pageSize {
				return nil
			}
			offset += len(results)
		}
	})
}
