package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"pi-streamer/internal/artstore"
	"pi-streamer/internal/indexer"
	"pi-streamer/internal/jobs"
)

// fakeAlbumArtFetcher is an in-memory albumArtFetcher for tests.
type fakeAlbumArtFetcher struct {
	data  map[string][]byte // url -> art bytes (absent/nil means "confirmed no art")
	err   map[string]error  // url -> error to return instead
	calls []string
}

func (f *fakeAlbumArtFetcher) AlbumArt(url string) ([]byte, error) {
	f.calls = append(f.calls, url)
	if err, ok := f.err[url]; ok {
		return nil, err
	}
	return f.data[url], nil
}

// fakeCoverArtFetcher is an in-memory coverArtFetcher for tests.
type fakeCoverArtFetcher struct {
	data  map[string][]byte // "artist|album" -> art bytes
	calls []string
}

func (f *fakeCoverArtFetcher) Fetch(ctx context.Context, artist, album string) ([]byte, error) {
	key := artist + "|" + album
	f.calls = append(f.calls, key)
	return f.data[key], nil
}

// fakeLibraryLister is an in-memory libraryLister for tests.
type fakeLibraryLister struct {
	results []indexer.Result
}

func (f *fakeLibraryLister) Library(limit, offset int) ([]indexer.Result, error) {
	if offset >= len(f.results) {
		return nil, nil
	}
	end := offset + limit
	if end > len(f.results) {
		end = len(f.results)
	}
	return f.results[offset:end], nil
}

func newTestArtAdapter(t *testing.T) (*artAdapter, *fakeAlbumArtFetcher, *fakeLibraryLister) {
	t.Helper()
	store, err := artstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("artstore.Open: %v", err)
	}
	mpd := &fakeAlbumArtFetcher{data: map[string][]byte{}, err: map[string]error{}}
	lib := &fakeLibraryLister{}
	return &artAdapter{mpd: mpd, library: lib, store: store, jobs: jobs.New(nil, 0)}, mpd, lib
}

func waitForJobDone(t *testing.T, a *artAdapter) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, j := range a.jobs.List() {
			if j.Status != jobs.StatusRunning {
				return j
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the warm job to finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestResolveFetchesAndPersistsOnFirstRequest(t *testing.T) {
	a, mpd, _ := newTestArtAdapter(t)
	mpd.data["http://example.com/a.mp3"] = []byte("\xff\xd8\xff\xe0jpegbytes")

	path, ok, err := a.Resolve("http://example.com/a.mp3", "", "")
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	if !ok || path == "" {
		t.Fatalf("Resolve() = %q, %v, want a non-empty path and ok=true", path, ok)
	}
	if len(mpd.calls) != 1 {
		t.Errorf("mpd.calls = %v, want exactly one fetch", mpd.calls)
	}
}

func TestResolveDoesNotRefetchOnSecondRequest(t *testing.T) {
	a, mpd, _ := newTestArtAdapter(t)
	mpd.data["http://example.com/a.mp3"] = []byte("\xff\xd8\xff\xe0jpegbytes")

	first, _, err := a.Resolve("http://example.com/a.mp3", "", "")
	if err != nil {
		t.Fatal(err)
	}
	second, ok, err := a.Resolve("http://example.com/a.mp3", "", "")
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	if !ok || second != first {
		t.Errorf("second Resolve() = %q, %v, want the same path as the first (%q)", second, ok, first)
	}
	if len(mpd.calls) != 1 {
		t.Errorf("mpd.calls = %v, want exactly one fetch (the second request should hit the store)", mpd.calls)
	}
}

func TestResolveConfirmedNoArtIsAlsoNotRefetched(t *testing.T) {
	a, mpd, _ := newTestArtAdapter(t) // mpd.data has no entry: fetch returns (nil, nil)

	for i := 0; i < 2; i++ {
		path, ok, err := a.Resolve("http://example.com/no-art.mp3", "", "")
		if err != nil {
			t.Fatalf("request %d: Resolve() error = %v, want nil", i, err)
		}
		if ok || path != "" {
			t.Fatalf("request %d: Resolve() = %q, %v, want ok=false and an empty path", i, path, ok)
		}
	}
	if len(mpd.calls) != 1 {
		t.Errorf("mpd.calls = %v, want exactly one fetch (confirmed no-art should be cached too)", mpd.calls)
	}
}

func TestResolveDoesNotCacheTransientError(t *testing.T) {
	a, mpd, _ := newTestArtAdapter(t)
	mpd.err["http://example.com/a.mp3"] = errors.New("mpd unreachable")

	for i := 0; i < 2; i++ {
		_, ok, err := a.Resolve("http://example.com/a.mp3", "", "")
		if err == nil {
			t.Fatalf("request %d: Resolve() error = nil, want the fetch error surfaced", i)
		}
		if ok {
			t.Errorf("request %d: Resolve() ok = true, want false on error", i)
		}
	}
	if len(mpd.calls) != 2 {
		t.Errorf("mpd.calls = %v, want 2 (a transient error must never be cached)", mpd.calls)
	}
}

func TestResolveFallsBackToCoverArtWhenMpdHasNothing(t *testing.T) {
	a, _, _ := newTestArtAdapter(t) // mpd has nothing for this url
	cover := &fakeCoverArtFetcher{data: map[string][]byte{
		"Fleetwood Mac|Rumours": []byte("\xff\xd8\xff\xe0coverartbytes"),
	}}
	a.coverArt = cover

	path, ok, err := a.Resolve("http://example.com/a.mp3", "Fleetwood Mac", "Rumours")
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	if !ok || path == "" {
		t.Fatalf("Resolve() = %q, %v, want a non-empty path and ok=true from the fallback", path, ok)
	}
	if len(cover.calls) != 1 {
		t.Errorf("cover.calls = %v, want exactly one fallback lookup", cover.calls)
	}
}

func TestResolveSkipsCoverArtFallbackWithoutArtistAlbum(t *testing.T) {
	a, _, _ := newTestArtAdapter(t)
	cover := &fakeCoverArtFetcher{data: map[string][]byte{}}
	a.coverArt = cover

	if _, ok, err := a.Resolve("http://example.com/a.mp3", "", ""); err != nil || ok {
		t.Fatalf("Resolve() = ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	if len(cover.calls) != 0 {
		t.Errorf("cover.calls = %v, want none — no artist/album hint to look up", cover.calls)
	}
}

func TestRefreshBypassesExistingResultAndRefetches(t *testing.T) {
	a, mpd, _ := newTestArtAdapter(t)
	mpd.data["http://example.com/a.mp3"] = []byte("\xff\xd8\xff\xe0jpegbytes")

	if _, _, err := a.Resolve("http://example.com/a.mp3", "", ""); err != nil {
		t.Fatal(err)
	}
	if len(mpd.calls) != 1 {
		t.Fatalf("setup: mpd.calls = %v, want 1 before Refresh", mpd.calls)
	}

	if _, _, err := a.Refresh("http://example.com/a.mp3", "", ""); err != nil {
		t.Fatalf("Refresh() error = %v, want nil", err)
	}
	if len(mpd.calls) != 2 {
		t.Errorf("mpd.calls = %v, want 2 — Refresh must re-fetch even though the url was already known", mpd.calls)
	}
}

func TestQueryReportsKnownURLsOnlyWithoutFetching(t *testing.T) {
	a, mpd, _ := newTestArtAdapter(t)
	mpd.data["http://example.com/has-art.mp3"] = []byte("\xff\xd8\xff\xe0jpegbytes")

	if _, _, err := a.Resolve("http://example.com/has-art.mp3", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Resolve("http://example.com/no-art.mp3", "", ""); err != nil {
		t.Fatal(err)
	}
	mpd.calls = nil // reset — Query must not trigger any further fetches

	got := a.Query([]string{
		"http://example.com/has-art.mp3",
		"http://example.com/no-art.mp3",
		"http://example.com/never-seen.mp3",
	})

	if !got["http://example.com/has-art.mp3"].HasArt || got["http://example.com/has-art.mp3"].Path == "" {
		t.Errorf("has-art.mp3 = %+v, want HasArt=true with a non-empty Path", got["http://example.com/has-art.mp3"])
	}
	if status, known := got["http://example.com/no-art.mp3"]; !known || status.HasArt {
		t.Errorf("no-art.mp3 = %+v, known=%v, want HasArt=false, known=true", status, known)
	}
	if _, known := got["http://example.com/never-seen.mp3"]; known {
		t.Error("never-seen.mp3 should be omitted from the result entirely")
	}
	if len(mpd.calls) != 0 {
		t.Errorf("mpd.calls = %v, want none — Query must never fetch", mpd.calls)
	}
}

func TestWarmResolvesEveryUnknownLibraryEntry(t *testing.T) {
	a, mpd, lib := newTestArtAdapter(t)
	lib.results = []indexer.Result{
		{URL: "http://example.com/a.mp3"},
		{URL: "http://example.com/b.mp3"},
	}
	mpd.data["http://example.com/a.mp3"] = []byte("\xff\xd8\xff\xe0jpegbytes")
	// b.mp3 has no entry in mpd.data: confirmed no art, still counts as resolved.

	a.Warm()
	job := waitForJobDone(t, a)
	if job.Status != jobs.StatusDone {
		t.Fatalf("job.Status = %q, want %q (error: %s)", job.Status, jobs.StatusDone, job.Error)
	}
	if job.Done != 2 {
		t.Errorf("job.Done = %d, want 2", job.Done)
	}

	for _, url := range []string{"http://example.com/a.mp3", "http://example.com/b.mp3"} {
		if _, _, known := a.store.Lookup(url); !known {
			t.Errorf("Lookup(%q) known = false, want true after warm", url)
		}
	}
}

func TestWarmSkipsAlreadyKnownEntries(t *testing.T) {
	a, mpd, lib := newTestArtAdapter(t)
	lib.results = []indexer.Result{{URL: "http://example.com/a.mp3"}}
	mpd.data["http://example.com/a.mp3"] = []byte("\xff\xd8\xff\xe0jpegbytes")

	if _, _, err := a.Resolve("http://example.com/a.mp3", "", ""); err != nil {
		t.Fatal(err)
	}
	mpd.calls = nil

	a.Warm()
	waitForJobDone(t, a)

	if len(mpd.calls) != 0 {
		t.Errorf("mpd.calls = %v, want none — warm should skip a URL already resolved", mpd.calls)
	}
}
