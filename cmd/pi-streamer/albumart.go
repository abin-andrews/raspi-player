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
	"fmt"
	"io"
	"log"
	"net/http"

	"pi-streamer/internal/api"
	"pi-streamer/internal/artstore"
	"pi-streamer/internal/coverart"
	"pi-streamer/internal/indexer"
	"pi-streamer/internal/jobs"
	"pi-streamer/internal/ytdlp"
)

// albumArtFetcher is the narrow slice of *mpdclient.GompdClient's
// behavior artAdapter needs — matches the "package defines the interface
// it needs, concrete types satisfy it structurally" pattern used
// throughout this codebase.
type albumArtFetcher interface {
	AlbumArt(url string) ([]byte, error)
}

// coverArtFetcher is the narrow slice of *coverart.Fetcher's behavior
// artAdapter needs — the same MusicBrainz/Cover Art Archive client backs
// both the album art fallback (Fetch) and metadata suggestions
// (SearchMetadata), per internal/api.Art's Suggest doc comment.
type coverArtFetcher interface {
	Fetch(ctx context.Context, artist, album string) ([]byte, error)
	SearchMetadata(ctx context.Context, query string, limit int) ([]coverart.MetadataSuggestion, error)
}

// libraryLister is the narrow slice of *player.Player's behavior
// artAdapter's Warm needs.
type libraryLister interface {
	Library(limit, offset int) ([]indexer.Result, error)
}

// artAdapter implements internal/api.Art.
type artAdapter struct {
	mpd        albumArtFetcher
	coverArt   coverArtFetcher // may be nil — MusicBrainz fallback is then skipped entirely
	library    libraryLister
	store      *artstore.Store
	jobs       *jobs.Manager
	httpClient *http.Client // for SetCustomArt's fetch; nil uses http.DefaultClient
}

func (a *artAdapter) client() *http.Client {
	if a.httpClient != nil {
		return a.httpClient
	}
	return http.DefaultClient
}

// resolve is Resolve/Refresh's shared implementation. When force is false
// and url is already known, it returns the cached answer without touching
// mpd or MusicBrainz at all. artist/album are optional hints used both for
// the MusicBrainz fallback when mpd itself has nothing, and for the
// custom album/artist fallback below.
//
// A track's own custom art (SetCustomArt with scope "track") always wins
// and is checked *before* the force/cache gate above — it's never
// silently replaced by auto-resolution, not even via Refresh, which for
// every other entry means "bypass the cache and re-resolve anyway."
// ClearCustomArt is the only way back to auto-resolution for that track.
//
// If auto-resolution (mpd, then YouTube/MusicBrainz as applicable) comes
// up with nothing for the track itself, a custom album- or artist-level
// fallback (SetCustomArt with scope "album"/"artist") is tried before
// finally recording "no art" — this is the "fallback system" a custom
// art request was originally asking for: a track with no art of its own
// still shows *something* meaningful if its album or artist has one set.
func (a *artAdapter) resolve(url, artist, album string, force bool) (path string, ok bool, err error) {
	if a.store.IsCustom(url) {
		if name, hasArt, _ := a.store.Lookup(url); hasArt {
			return "/art/" + name, true, nil
		}
	}

	// A YouTube video's thumbnail is a stable, already-public CDN URL
	// computed directly from its video ID — there's nothing to fetch or
	// cache locally at all, unlike mpd/MusicBrainz art, which only exists
	// anywhere once this package downloads it. Returning it straight away
	// (before the cache/force check below, and skipping fetchAuto entirely)
	// means this never depends on a prior resolve, a disk cache entry, or
	// force/Refresh — the answer is always the same, computed instantly
	// with zero network I/O of our own. Uses the bigger hqdefault.jpg,
	// unlike Query's otherwise-identical shortcut below: resolve backs the
	// few places a single track's art is shown large (the "now playing"
	// view via albumArtUrl, a track's own detail page), where Query backs
	// list views showing many thumbnails at once — see
	// ytdlp.HQThumbnailURL/DefaultThumbnailURL's doc comments.
	if thumb, ok := ytdlp.HQThumbnailURL(url); ok {
		return thumb, true, nil
	}

	if !force {
		if name, hasArt, known := a.store.Lookup(url); known {
			if hasArt {
				return "/art/" + name, true, nil
			}
			if fbPath, fbOK := a.lookupCustomFallback(artist, album); fbOK {
				return fbPath, true, nil
			}
			return "", false, nil
		}
	}

	data, err := a.fetchAuto(url, artist, album)
	if err != nil {
		return "", false, err
	}

	if len(data) == 0 {
		if fbPath, fbOK := a.lookupCustomFallback(artist, album); fbOK {
			// Still record the track's own "no auto-resolved art"
			// result, so a plain Resolve (not Refresh) short-circuits
			// through the cache check above next time rather than
			// re-running the whole auto-resolution chain just to reach
			// the same fallback again.
			_, _, _ = a.store.Put(url, nil)
			return fbPath, true, nil
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

// fetchAuto runs the existing (non-custom) auto-resolution chain — mpd,
// falling back to MusicBrainz — and returns raw image bytes, or nil if
// neither found anything. YouTube URLs never reach here at all: resolve
// returns the video's own thumbnail before calling this, unconditionally.
// A fallback-lookup failure (MusicBrainz unreachable) is swallowed
// (logged, not returned) rather than failing the whole resolve: mpd
// already gave a confirmed answer, and an external service being
// unreachable shouldn't turn that into an error response or (worse)
// prevent caching mpd's own confirmed-no-art result.
func (a *artAdapter) fetchAuto(url, artist, album string) ([]byte, error) {
	data, err := a.mpd.AlbumArt(url)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 && a.coverArt != nil && artist != "" && album != "" {
		if fallback, fbErr := a.coverArt.Fetch(context.Background(), artist, album); fbErr != nil {
			log.Printf("album art: musicbrainz/coverartarchive fallback for %q (%s — %s): %v", url, artist, album, fbErr)
		} else {
			data = fallback
		}
	}
	return data, nil
}

// lookupCustomFallback checks for user-provided custom art at the album
// level, then the artist level (album is the more specific of the two) —
// used when a track has no art of its own, auto-resolved or custom.
func (a *artAdapter) lookupCustomFallback(artist, album string) (string, bool) {
	if album != "" {
		if name, hasArt, known := a.store.Lookup(customArtKeyForAlbum(album)); known && hasArt {
			return "/art/" + name, true
		}
	}
	if artist != "" {
		if name, hasArt, known := a.store.Lookup(customArtKeyForArtist(artist)); known && hasArt {
			return "/art/" + name, true
		}
	}
	return "", false
}

// customArtKeyForAlbum/customArtKeyForArtist build artstore keys for the
// album-/artist-level custom art fallbacks — synthetic keys, never real
// URLs, so they can't collide with a track's own entry in the same
// content-addressed store; distinctly prefixed per scope so an album and
// an artist that happen to share a name don't collide with each other
// either. internal/artstore itself stays unaware of "album"/"artist" as
// concepts — it just hashes whatever key string it's given — so this key
// construction lives here, in the one place that actually cares.
func customArtKeyForAlbum(album string) string   { return "custom-album:" + album }
func customArtKeyForArtist(artist string) string { return "custom-artist:" + artist }

// customArtStoreKey maps a public SetCustomArt/ClearCustomArt scope
// ("track"/"album"/"artist") and its key to the actual artstore key to
// use — a track's own URL as-is, or a synthetic album-/artist-level key.
func customArtStoreKey(scope, key string) (string, error) {
	switch scope {
	case "track":
		return key, nil
	case "album":
		return customArtKeyForAlbum(key), nil
	case "artist":
		return customArtKeyForArtist(key), nil
	default:
		return "", fmt.Errorf("artadapter: unknown custom art scope %q", scope)
	}
}

// SetCustomArt fetches imageURL — any URL that serves an image, not
// necessarily one this package otherwise knows how to resolve art from —
// and records it as scope/key's custom art, per customArtStoreKey. This
// is the explicit "fallback system in case" auto-resolution doesn't find
// the right thing (or anything at all): once set, resolve treats it as
// authoritative for that scope (see resolve's own doc comment) until
// ClearCustomArt reverts it.
func (a *artAdapter) SetCustomArt(scope, key, imageURL string) (api.ArtStatus, error) {
	storeKey, err := customArtStoreKey(scope, key)
	if err != nil {
		return api.ArtStatus{}, err
	}
	data, err := a.fetchImageURL(imageURL)
	if err != nil {
		return api.ArtStatus{}, err
	}
	name, err := a.store.PutCustom(storeKey, data)
	if err != nil {
		return api.ArtStatus{}, err
	}
	return api.ArtStatus{HasArt: true, Path: "/art/" + name}, nil
}

// ClearCustomArt removes scope/key's custom art, reverting it to
// "unknown" — a track's own next Resolve/Refresh call runs the
// auto-resolution chain fresh; an album/artist fallback simply stops
// being offered to tracks with no art of their own. Not an error if
// nothing was set for scope/key in the first place.
func (a *artAdapter) ClearCustomArt(scope, key string) error {
	storeKey, err := customArtStoreKey(scope, key)
	if err != nil {
		return err
	}
	return a.store.Remove(storeKey)
}

// customArtUserAgent stands in for a real browser's UA on outgoing custom-
// art fetches. Verified needed against a real failure: Wikimedia's
// upload.wikimedia.org (among many other image hosts — Imgur, Discord's
// CDN, Google image results, etc., all do the same as basic hotlink
// protection) returns 403 for Go's default "Go-http-client/..." UA. Since
// SetCustomArt is meant to work with "anything" the user points it at (an
// explicit requirement, not a guess), presenting as an ordinary browser is
// the generic fix rather than special-casing any one host.
const customArtUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// fetchImageURL downloads imageURL's raw bytes — deliberately generic (no
// host restriction, no assumption it's YouTube/MusicBrainz/anything else
// this package otherwise knows about): SetCustomArt is meant to work with
// any URL the user happens to have that serves an image.
func (a *artAdapter) fetchImageURL(imageURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("artadapter: build custom art request: %w", err)
	}
	req.Header.Set("User-Agent", customArtUserAgent)
	resp, err := a.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("artadapter: fetch custom art: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artadapter: fetch custom art: status %d", resp.StatusCode)
	}
	// 20MiB cap: plenty of headroom for a cover image, small enough to
	// never be a meaningful memory concern on a 512MB Pi even in the
	// worst case of a misbehaving/oversized response.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, fmt.Errorf("artadapter: read custom art response: %w", err)
	}
	return data, nil
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

// Suggest returns candidate Title/Artist/Album matches for query via the
// same coverArtFetcher Resolve/Refresh already use as an album art
// fallback — "baked into the album art fetch system" rather than a
// separate lookup client, per how this was asked for. Returns (nil, nil)
// if no fallback source is configured at all, the same "gracefully skip"
// behavior resolve's own art fallback has when a.coverArt is nil.
func (a *artAdapter) Suggest(query string) ([]api.MetadataSuggestion, error) {
	if a.coverArt == nil {
		return nil, nil
	}
	results, err := a.coverArt.SearchMetadata(context.Background(), query, 5)
	if err != nil {
		return nil, err
	}
	suggestions := make([]api.MetadataSuggestion, len(results))
	for i, r := range results {
		suggestions[i] = api.MetadataSuggestion{Title: r.Title, Artist: r.Artist, Album: r.Album}
	}
	return suggestions, nil
}

func (a *artAdapter) Query(urls []string) map[string]api.ArtStatus {
	out := make(map[string]api.ArtStatus, len(urls))
	for _, u := range urls {
		name, hasArt, known := a.store.Lookup(u)
		if known {
			status := api.ArtStatus{HasArt: hasArt}
			if hasArt {
				status.Path = "/art/" + name
			}
			out[u] = status
			continue
		}
		// Not yet resolved at all — but a YouTube video's thumbnail needs
		// no resolving in the first place (see resolve's identical
		// shortcut): report it here too, so a freshly queued/played video
		// shows its real thumbnail immediately rather than a placeholder
		// until some later GET /api/albumart request happens to populate
		// the cache. Query backs list views (Library/Queue/search results,
		// batched via useAlbumArtStatus) showing many thumbnails at once,
		// so this deliberately uses the smaller default.jpg — resolve's
		// identical shortcut below uses the bigger hqdefault.jpg for the
		// few places a single track's art is shown large.
		if thumb, ok := ytdlp.DefaultThumbnailURL(u); ok {
			out[u] = api.ArtStatus{HasArt: true, Path: thumb}
		}
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
