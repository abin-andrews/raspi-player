// Package urlnorm normalizes a submitted URL into a canonical form before
// it's used as a dedup key (search index, bucket cache, favorites/playlists,
// history) — so "HTTP://Example.com/song.mp3" and
// "http://example.com/song.mp3" collapse onto the same track instead of
// being treated as two different ones. Normalization only touches parts of
// the URL that are defined to be case-insensitive or equivalent by RFC 3986
// (scheme, host, default port, empty vs root path) — it never touches path,
// query, or fragment, since those are frequently case-sensitive on the
// serving side (and query strings can carry auth tokens), and altering them
// risks breaking playback rather than just deduping it.
package urlnorm

import (
	"fmt"
	"net/url"
	"strings"

	"pi-streamer/internal/ytdlp"
)

// Normalize trims whitespace and lowercases/canonicalizes the scheme, host,
// and default port of rawURL, returning an error if rawURL is empty or not
// a parseable URL.
//
// A recognized YouTube video URL is a deliberate exception to the "never
// touch path/query" rule above: every shape ytdlp.VideoID recognizes (an
// ordinary watch link, a youtu.be share link, an embed/Shorts/live link,
// each with or without extra query params — a share link's "?si=..."
// tracking token, a timestamp, a playlist reference) identifies the exact
// same video, and a real "copy link" button routinely produces a
// different one of these for what is, for every purpose this URL is used
// for (the search index, the bucket cache, favorites/playlists/history,
// album art, and internal/player's per-URL enrichment cache), the
// identical track. Without collapsing them here, the same video pasted via
// two different link styles — or the same one shared twice, picking up a
// different tracking token each time — would be treated as two unrelated
// tracks throughout the whole app: two bucket-cache entries (so the audio
// gets extracted twice), two Library rows, two enrichment-cache lookups,
// etc. Collapses to "https://www.youtube.com/watch?v=<id>" — the most
// widely recognized form, deliberately dropping every other query
// parameter, since none of them affect the video's own identity.
func Normalize(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", fmt.Errorf("urlnorm: empty URL")
	}

	if id, ok := ytdlp.VideoID(trimmed); ok {
		return "https://www.youtube.com/watch?v=" + id, nil
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("urlnorm: invalid URL %q: %w", trimmed, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("urlnorm: URL %q missing scheme or host", trimmed)
	}

	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)

	switch {
	case u.Scheme == "http" && strings.HasSuffix(u.Host, ":80"):
		u.Host = strings.TrimSuffix(u.Host, ":80")
	case u.Scheme == "https" && strings.HasSuffix(u.Host, ":443"):
		u.Host = strings.TrimSuffix(u.Host, ":443")
	}

	if u.Path == "" {
		u.Path = "/"
	}

	return u.String(), nil
}
