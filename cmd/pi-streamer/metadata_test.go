package main

import (
	"context"
	"errors"
	"testing"

	"pi-streamer/internal/metadata"
)

// fakeTitleFetcher is an in-memory titleFetcher for tests.
type fakeTitleFetcher struct {
	title string
	ok    bool
	calls []string
}

func (f *fakeTitleFetcher) FetchTitle(ctx context.Context, youtubeURL string) (string, bool) {
	f.calls = append(f.calls, youtubeURL)
	return f.title, f.ok
}

// fakeAudioTagFetcher is an in-memory audioTagFetcher for tests — avoids
// metadata.Fetcher's real ranged HTTP GET, which would otherwise make a
// live network call for any test exercising the non-YouTube path.
type fakeAudioTagFetcher struct {
	tags  metadata.Tags
	err   error
	calls []string
}

func (f *fakeAudioTagFetcher) Fetch(ctx context.Context, url string) (metadata.Tags, error) {
	f.calls = append(f.calls, url)
	return f.tags, f.err
}

func TestMetadataAdapterUsesTitleFetcherForYouTubeURLs(t *testing.T) {
	titles := &fakeTitleFetcher{title: "Example Video Title", ok: true}
	fetcher := &fakeAudioTagFetcher{}
	a := &metadataAdapter{fetcher: fetcher, titles: titles}

	title, artist, album, ok := a.Fetch("https://youtu.be/abc123XYZ90")
	if !ok {
		t.Fatal("Fetch: want ok=true")
	}
	if title != "Example Video Title" {
		t.Errorf("title = %q, want %q", title, "Example Video Title")
	}
	if artist != "" || album != "" {
		t.Errorf("artist/album = %q/%q, want both empty for a YouTube URL", artist, album)
	}
	if len(titles.calls) != 1 || titles.calls[0] != "https://youtu.be/abc123XYZ90" {
		t.Errorf("titles.calls = %v, want one entry for the submitted YouTube URL", titles.calls)
	}
	if len(fetcher.calls) != 0 {
		t.Errorf("fetcher.calls = %v, want none — a YouTube URL should never reach the ordinary tag path", fetcher.calls)
	}
}

func TestMetadataAdapterPropagatesTitleFetcherFailure(t *testing.T) {
	titles := &fakeTitleFetcher{ok: false}
	a := &metadataAdapter{fetcher: &fakeAudioTagFetcher{}, titles: titles}

	if _, _, _, ok := a.Fetch("https://youtu.be/abc123XYZ90"); ok {
		t.Error("Fetch: want ok=false when the title fetcher itself fails")
	}
}

func TestMetadataAdapterSkipsYouTubePathWhenTitlesFetcherIsNil(t *testing.T) {
	fetcher := &fakeAudioTagFetcher{err: errors.New("no tags found")}
	a := &metadataAdapter{fetcher: fetcher, titles: nil}

	// No titles fetcher configured — must fall through to the ordinary
	// path (which fails here, since fetcher is stubbed to) rather than
	// panicking on the nil titles field.
	if _, _, _, ok := a.Fetch("https://youtu.be/abc123XYZ90"); ok {
		t.Error("Fetch: want ok=false with no titles fetcher configured")
	}
	if len(fetcher.calls) != 1 {
		t.Errorf("fetcher.calls = %v, want exactly one — the ordinary path should still run", fetcher.calls)
	}
}

func TestMetadataAdapterUsesOrdinaryPathForNonYouTubeURLs(t *testing.T) {
	titles := &fakeTitleFetcher{title: "should never be used", ok: true}
	fetcher := &fakeAudioTagFetcher{tags: metadata.Tags{Title: "Real Tag Title", Artist: "Real Tag Artist"}}
	a := &metadataAdapter{fetcher: fetcher, titles: titles}

	title, artist, _, ok := a.Fetch("https://example.com/track.mp3")
	if !ok {
		t.Fatal("Fetch: want ok=true")
	}
	if title != "Real Tag Title" || artist != "Real Tag Artist" {
		t.Errorf("title/artist = %q/%q, want the ordinary path's tags", title, artist)
	}
	if len(titles.calls) != 0 {
		t.Errorf("titles.calls = %v, want none for a non-YouTube URL", titles.calls)
	}
}
