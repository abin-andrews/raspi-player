// Package metadata extracts audio tags (title/artist/album) directly from a
// URL's own file data, so a track gets real library metadata as soon as it's
// added — not just once mpd happens to play it and report decoded tags back.
package metadata

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dhowden/tag"
)

const (
	defaultTimeout = 10 * time.Second
	// fetchBytes caps how much of the file is downloaded before giving up
	// on finding tags. Covers ID3v2/FLAC/Vorbis-comment headers, which sit
	// at the front of the file, without downloading the whole track.
	fetchBytes = 1 << 20 // 1 MiB
)

// Tags is the metadata extracted from a track's own file data.
type Tags struct {
	Title  string
	Artist string
	Album  string
}

// Fetcher extracts Tags from a URL's own audio data via a ranged HTTP GET.
type Fetcher struct {
	// Timeout bounds the whole fetch+parse operation. Defaults to 10
	// seconds if zero.
	Timeout time.Duration
}

// Fetch downloads the first chunk of url's bytes and parses embedded audio
// tags out of it. Only http(s) URLs are supported — anything else (e.g. a
// local file path) errors immediately. A stream with no embedded tags (most
// internet radio) or a format whose tags live outside the fetched prefix
// (e.g. ID3v1-only, at the end of the file) is reported as an error too —
// callers should treat any error as "no metadata available" and fall back
// to their own title derivation, not as something to surface to the user.
func (f *Fetcher) Fetch(ctx context.Context, url string) (Tags, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return Tags{}, fmt.Errorf("metadata: unsupported URL scheme: %s", url)
	}

	timeout := f.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Tags{}, fmt.Errorf("metadata: build request: %w", err)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", fetchBytes-1))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Tags{}, fmt.Errorf("metadata: fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return Tags{}, fmt.Errorf("metadata: fetch: status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, fetchBytes))
	if err != nil {
		return Tags{}, fmt.Errorf("metadata: read body: %w", err)
	}

	m, err := tag.ReadFrom(bytes.NewReader(data))
	if err != nil {
		return Tags{}, fmt.Errorf("metadata: no readable tags: %w", err)
	}

	t := Tags{
		Title:  strings.TrimSpace(m.Title()),
		Artist: strings.TrimSpace(m.Artist()),
		Album:  strings.TrimSpace(m.Album()),
	}
	if t.Title == "" && t.Artist == "" && t.Album == "" {
		return Tags{}, fmt.Errorf("metadata: tags present but empty")
	}
	return t, nil
}
