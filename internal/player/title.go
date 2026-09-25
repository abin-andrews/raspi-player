package player

import (
	"net/url"
	"path"
	"strings"
)

// deriveTitleFromURL builds a readable fallback title from a track's URL,
// for entries mpd hasn't read (or doesn't have) real tags for — Queue and
// Status call this only when mpd's own Title is empty, so it never
// overrides real tag data; it just gives a "raw mpd data" queue/status
// listing something better than a blank field or the bare URL to show,
// transforming e.g. ".../02-Chandamaama%20%28D%29.mp3" into
// "02 Chandamaama (D)".
func deriveTitleFromURL(rawURL string) string {
	name := rawURL
	if u, err := url.Parse(rawURL); err == nil {
		if base := path.Base(u.Path); base != "" && base != "." && base != "/" {
			name = base
		}
	}
	if decoded, err := url.QueryUnescape(name); err == nil {
		name = decoded
	}
	if ext := path.Ext(name); ext != "" && len(ext) <= 6 {
		name = strings.TrimSuffix(name, ext)
	}
	name = strings.NewReplacer("_", " ", "-", " ").Replace(name)
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return rawURL
	}
	return name
}
