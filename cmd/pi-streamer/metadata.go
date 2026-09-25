package main

import (
	"context"

	"pi-streamer/internal/metadata"
)

// metadataAdapter satisfies player.MetadataFetcher by wrapping
// internal/metadata.Fetcher, translating its (Tags, error) return into the
// (title, artist, album string, ok bool) shape player.MetadataFetcher
// expects — any fetch/parse error (unreachable URL, no embedded tags, a
// stream format with none) just means ok=false, not something to surface.
type metadataAdapter struct {
	fetcher *metadata.Fetcher
}

func (a *metadataAdapter) Fetch(url string) (title, artist, album string, ok bool) {
	tags, err := a.fetcher.Fetch(context.Background(), url)
	if err != nil {
		return "", "", "", false
	}
	return tags.Title, tags.Artist, tags.Album, true
}
