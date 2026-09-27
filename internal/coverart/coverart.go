// Package coverart looks up album art from MusicBrainz + the Cover Art
// Archive when mpd itself has none — a fallback source for tracks with no
// embedded/cover-file art mpd can see (the common case for internet radio
// and many lossily-tagged files), keyed by artist+album rather than the
// track URL, since that's what these services index by.
package coverart

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// MusicBrainz's API usage policy requires a descriptive User-Agent
// identifying the application, and asks that unauthenticated clients stay
// at or below 1 request/second — minRequestInterval below enforces a
// safety margin over that.
const userAgent = "pi-streamer/1.0 (personal self-hosted audio player)"

const minRequestInterval = 1100 * time.Millisecond

// Fetcher looks up cover art via MusicBrainz's search API (to resolve an
// artist+album into a release ID) and then the Cover Art Archive (to fetch
// that release's front cover, if any). baseURL/archiveBaseURL are
// overridable for tests; leave them zero-valued in production to use the
// real services.
type Fetcher struct {
	httpClient  *http.Client
	baseURL     string        // MusicBrainz API, default https://musicbrainz.org/ws/2
	archiveURL  string        // Cover Art Archive, default https://coverartarchive.org
	minInterval time.Duration // rate limit, default minRequestInterval — overridable for tests

	mu          sync.Mutex
	lastRequest time.Time // rate-limits calls to MusicBrainz's own API only
}

func (f *Fetcher) client() *http.Client {
	if f.httpClient != nil {
		return f.httpClient
	}
	return http.DefaultClient
}

func (f *Fetcher) mbBaseURL() string {
	if f.baseURL != "" {
		return f.baseURL
	}
	return "https://musicbrainz.org/ws/2"
}

func (f *Fetcher) coverArtBaseURL() string {
	if f.archiveURL != "" {
		return f.archiveURL
	}
	return "https://coverartarchive.org"
}

// Fetch returns the front cover art for the release matching artist+album,
// or (nil, nil) if none could be found — that's the expected, common
// outcome (not every album is in MusicBrainz, and not every release with
// an entry has cover art archived), not an error. Both artist and album
// are required; a query missing either is rejected up front rather than
// sent to MusicBrainz, since that service's own search would just as
// reliably return nothing useful for it.
func (f *Fetcher) Fetch(ctx context.Context, artist, album string) ([]byte, error) {
	if artist == "" || album == "" {
		return nil, fmt.Errorf("coverart: artist and album are both required")
	}

	mbid, err := f.lookupReleaseID(ctx, artist, album)
	if err != nil {
		return nil, err
	}
	if mbid == "" {
		return nil, nil
	}
	return f.fetchFrontCover(ctx, mbid)
}

func (f *Fetcher) lookupReleaseID(ctx context.Context, artist, album string) (string, error) {
	f.waitForRateLimit()

	values := url.Values{}
	values.Set("query", fmt.Sprintf(`artist:"%s" AND release:"%s"`, artist, album))
	values.Set("fmt", "json")
	values.Set("limit", "1")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.mbBaseURL()+"/release/?"+values.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("coverart: build search request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := f.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("coverart: search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("coverart: search: status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Releases []struct {
			ID string `json:"id"`
		} `json:"releases"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("coverart: decode search response: %w", err)
	}
	if len(result.Releases) == 0 {
		return "", nil
	}
	return result.Releases[0].ID, nil
}

func (f *Fetcher) fetchFrontCover(ctx context.Context, mbid string) ([]byte, error) {
	// Not rate-limited — the Cover Art Archive (archive.org) is a
	// separate service from the MusicBrainz API proper, without the same
	// 1req/s guideline.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.coverArtBaseURL()+"/release/"+mbid+"/front", nil)
	if err != nil {
		return nil, fmt.Errorf("coverart: build archive request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := f.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("coverart: archive request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // this release has no cover art archived — not an error
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("coverart: archive: status %d: %s", resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("coverart: read archive response: %w", err)
	}
	return data, nil
}

func (f *Fetcher) waitForRateLimit() {
	f.mu.Lock()
	defer f.mu.Unlock()
	interval := f.minInterval
	if interval <= 0 {
		interval = minRequestInterval
	}
	if wait := interval - time.Since(f.lastRequest); wait > 0 {
		time.Sleep(wait)
	}
	f.lastRequest = time.Now()
}
