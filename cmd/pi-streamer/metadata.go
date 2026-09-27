package main

import (
	"context"

	"pi-streamer/internal/metadata"
	"pi-streamer/internal/ytdlp"
)

// titleFetcher is the narrow slice of *ytdlp.TitleFetcher's behavior
// metadataAdapter needs — matches the "package defines the interface it
// needs, concrete types satisfy it structurally" pattern used throughout
// this codebase (see albumArtFetcher/coverArtFetcher in albumart.go).
type titleFetcher interface {
	FetchTitle(ctx context.Context, youtubeURL string) (string, bool)
}

// audioTagFetcher is the narrow slice of *metadata.Fetcher's behavior
// metadataAdapter needs — same reasoning as titleFetcher above, and lets
// tests fake it instead of making a real network call for the "YouTube
// path is skipped" case.
type audioTagFetcher interface {
	Fetch(ctx context.Context, url string) (metadata.Tags, error)
}

// metadataAdapter satisfies player.MetadataFetcher by wrapping
// internal/metadata.Fetcher, translating its (Tags, error) return into the
// (title, artist, album string, ok bool) shape player.MetadataFetcher
// expects — any fetch/parse error (unreachable URL, no embedded tags, a
// stream format with none) just means ok=false, not something to surface.
//
// A YouTube URL is special-cased ahead of the ordinary ID3/FLAC/Vorbis
// path: internal/metadata.Fetcher does a ranged HTTP GET of the URL and
// parses whatever audio container it finds there, but a youtube.com URL
// is an HTML page, not an audio file at all, so that path would never
// find anything for it. titles (nil-able — skips the YouTube case
// entirely if unset, same as fetcher being nil elsewhere in this
// codebase) instead asks YouTube's own oEmbed endpoint for the video's
// title directly — far cheaper than running yt-dlp just to learn a title,
// and independent of whether audio extraction (see
// cmd/pi-streamer/bucket.go's modeResolver) has happened yet at all.
type metadataAdapter struct {
	fetcher audioTagFetcher
	titles  titleFetcher
}

func (a *metadataAdapter) Fetch(url string) (title, artist, album string, ok bool) {
	if a.titles != nil && ytdlp.IsYouTubeURL(url) {
		title, ok := a.titles.FetchTitle(context.Background(), url)
		return title, "", "", ok
	}
	tags, err := a.fetcher.Fetch(context.Background(), url)
	if err != nil {
		return "", "", "", false
	}
	return tags.Title, tags.Artist, tags.Album, true
}
