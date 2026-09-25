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
)

// Normalize trims whitespace and lowercases/canonicalizes the scheme, host,
// and default port of rawURL, returning an error if rawURL is empty or not
// a parseable URL.
func Normalize(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", fmt.Errorf("urlnorm: empty URL")
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
