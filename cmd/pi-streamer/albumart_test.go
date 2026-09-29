package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pi-streamer/internal/api"
	"pi-streamer/internal/artstore"
	"pi-streamer/internal/coverart"
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

	suggestions  map[string][]coverart.MetadataSuggestion // query -> results
	suggestErr   error
	suggestCalls []string
}

func (f *fakeCoverArtFetcher) Fetch(ctx context.Context, artist, album string) ([]byte, error) {
	key := artist + "|" + album
	f.calls = append(f.calls, key)
	return f.data[key], nil
}

func (f *fakeCoverArtFetcher) SearchMetadata(ctx context.Context, query string, limit int) ([]coverart.MetadataSuggestion, error) {
	f.suggestCalls = append(f.suggestCalls, query)
	if f.suggestErr != nil {
		return nil, f.suggestErr
	}
	return f.suggestions[query], nil
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

func TestResolveReturnsDirectYouTubeThumbnailURLInsteadOfMPD(t *testing.T) {
	a, mpd, _ := newTestArtAdapter(t)

	path, ok, err := a.Resolve("https://youtu.be/abc123XYZ90", "", "")
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	want := "https://i.ytimg.com/vi/abc123XYZ90/hqdefault.jpg"
	if !ok || path != want {
		t.Fatalf("Resolve() = %q, %v, want (%q, true)", path, ok, want)
	}
	if len(mpd.calls) != 0 {
		t.Errorf("mpd.calls = %v, want none — a YouTube URL should never reach mpd.AlbumArt", mpd.calls)
	}
}

func TestResolveYouTubeThumbnailNeedsNoNetworkOrCache(t *testing.T) {
	a, mpd, _ := newTestArtAdapter(t)

	// Calling twice (the second via Refresh, force=true) must behave
	// identically either way, and never touch mpd or persist anything —
	// the answer is a pure function of the URL's video ID.
	first, ok1, err1 := a.Resolve("https://youtu.be/abc123XYZ90", "", "")
	second, ok2, err2 := a.Refresh("https://youtu.be/abc123XYZ90", "", "")
	if err1 != nil || err2 != nil || !ok1 || !ok2 || first != second {
		t.Fatalf("Resolve/Refresh = (%q,%v,%v)/(%q,%v,%v), want identical ok results",
			first, ok1, err1, second, ok2, err2)
	}
	if len(mpd.calls) != 0 {
		t.Errorf("mpd.calls = %v, want none", mpd.calls)
	}
	if _, _, known := a.store.Lookup("https://youtu.be/abc123XYZ90"); known {
		t.Error("store.Lookup: want unknown — a YouTube thumbnail is never persisted to the artstore")
	}
}

func TestResolveYouTubeCustomArtStillWins(t *testing.T) {
	a, _, _ := newTestArtAdapter(t)
	srv := fakeImageServer(t, []byte("\xff\xd8\xff\xe0custom"))
	if _, err := a.SetCustomArt("track", "https://youtu.be/abc123XYZ90", srv.URL); err != nil {
		t.Fatalf("SetCustomArt: %v", err)
	}

	path, ok, err := a.Resolve("https://youtu.be/abc123XYZ90", "", "")
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	if !ok || path == "https://i.ytimg.com/vi/abc123XYZ90/hqdefault.jpg" {
		t.Errorf("Resolve() = %q, %v, want the custom art path, not the YouTube thumbnail", path, ok)
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

func TestQueryReportsYouTubeThumbnailForAnUnresolvedURL(t *testing.T) {
	a, mpd, _ := newTestArtAdapter(t)

	got := a.Query([]string{"https://youtu.be/abc123XYZ90"})

	// Deliberately the small default.jpg, not hqdefault.jpg — Query backs
	// list views showing many thumbnails at once (see resolve's own test,
	// TestResolveReturnsDirectYouTubeThumbnailURLInsteadOfMPD, for the
	// bigger size used for a single prominently-displayed track).
	want := "https://i.ytimg.com/vi/abc123XYZ90/default.jpg"
	if status := got["https://youtu.be/abc123XYZ90"]; !status.HasArt || status.Path != want {
		t.Errorf("got %+v, want HasArt=true, Path=%q", status, want)
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

func TestSuggestReturnsCandidatesFromCoverArtFetcher(t *testing.T) {
	a, _, _ := newTestArtAdapter(t)
	cover := &fakeCoverArtFetcher{suggestions: map[string][]coverart.MetadataSuggestion{
		"some query": {{Title: "Track One", Artist: "Some Artist", Album: "Some Album"}},
	}}
	a.coverArt = cover

	got, err := a.Suggest("some query")
	if err != nil {
		t.Fatalf("Suggest() error = %v, want nil", err)
	}
	want := []api.MetadataSuggestion{{Title: "Track One", Artist: "Some Artist", Album: "Some Album"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("Suggest() = %+v, want %+v", got, want)
	}
	if len(cover.suggestCalls) != 1 || cover.suggestCalls[0] != "some query" {
		t.Errorf("suggestCalls = %v, want one entry for the submitted query", cover.suggestCalls)
	}
}

func TestSuggestReturnsNilWithoutACoverArtFetcherConfigured(t *testing.T) {
	a, _, _ := newTestArtAdapter(t) // a.coverArt left nil, as newTestArtAdapter leaves it

	got, err := a.Suggest("anything")
	if err != nil {
		t.Fatalf("Suggest() error = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("Suggest() = %v, want nil with no fallback source configured", got)
	}
}

func TestSuggestPropagatesFetcherError(t *testing.T) {
	a, _, _ := newTestArtAdapter(t)
	cover := &fakeCoverArtFetcher{suggestErr: errors.New("musicbrainz unreachable")}
	a.coverArt = cover

	if _, err := a.Suggest("anything"); err == nil {
		t.Error("Suggest(): want error when the fetcher fails, got nil")
	}
}

func fakeImageServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSetCustomArtForTrackScope(t *testing.T) {
	a, _, _ := newTestArtAdapter(t)
	srv := fakeImageServer(t, []byte("\xff\xd8\xff\xe0customjpeg"))

	status, err := a.SetCustomArt("track", "http://example.com/a.mp3", srv.URL)
	if err != nil {
		t.Fatalf("SetCustomArt() error = %v, want nil", err)
	}
	if !status.HasArt || status.Path == "" {
		t.Fatalf("SetCustomArt() = %+v, want HasArt=true and a non-empty path", status)
	}
	if !a.store.IsCustom("http://example.com/a.mp3") {
		t.Error("IsCustom() = false for the track key, want true after SetCustomArt")
	}
}

func TestSetCustomArtRejectsUnknownScope(t *testing.T) {
	a, _, _ := newTestArtAdapter(t)
	srv := fakeImageServer(t, []byte("\xff\xd8\xff\xe0customjpeg"))

	if _, err := a.SetCustomArt("song", "whatever", srv.URL); err == nil {
		t.Error("SetCustomArt(\"song\", ...): want error for an unknown scope, got nil")
	}
}

func TestSetCustomArtPropagatesFetchError(t *testing.T) {
	a, _, _ := newTestArtAdapter(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := a.SetCustomArt("track", "http://example.com/a.mp3", srv.URL); err == nil {
		t.Error("SetCustomArt(): want error on a non-200 fetch, got nil")
	}
}

func TestResolveTrackCustomArtWinsEvenOnRefresh(t *testing.T) {
	a, mpd, _ := newTestArtAdapter(t)
	srv := fakeImageServer(t, []byte("\xff\xd8\xff\xe0customjpeg"))
	mpd.data["http://example.com/a.mp3"] = []byte("\xff\xd8\xff\xe0mpdjpeg")

	if _, err := a.SetCustomArt("track", "http://example.com/a.mp3", srv.URL); err != nil {
		t.Fatalf("SetCustomArt: %v", err)
	}
	mpd.calls = nil

	path1, ok, err := a.Resolve("http://example.com/a.mp3", "", "")
	if err != nil || !ok {
		t.Fatalf("Resolve() = %q, %v, %v, want the custom path with no error", path1, ok, err)
	}
	// Refresh (force=true) would normally always bypass the cache and
	// re-resolve — custom art must be the one exception.
	path2, ok, err := a.Refresh("http://example.com/a.mp3", "", "")
	if err != nil || !ok || path2 != path1 {
		t.Fatalf("Refresh() = %q, %v, %v, want the same custom path (%q) unchanged", path2, ok, err, path1)
	}
	if len(mpd.calls) != 0 {
		t.Errorf("mpd.calls = %v, want none — mpd should never be consulted once custom art is set", mpd.calls)
	}
}

func TestResolveFallsBackToCustomAlbumArtWhenTrackHasNone(t *testing.T) {
	a, mpd, _ := newTestArtAdapter(t) // mpd has nothing for this url, no coverArt configured
	srv := fakeImageServer(t, []byte("\xff\xd8\xff\xe0albumjpeg"))
	if _, err := a.SetCustomArt("album", "Rumours", srv.URL); err != nil {
		t.Fatalf("SetCustomArt: %v", err)
	}

	path, ok, err := a.Resolve("http://example.com/a.mp3", "Fleetwood Mac", "Rumours")
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	if !ok || path == "" {
		t.Fatalf("Resolve() = %q, %v, want the custom album fallback used", path, ok)
	}
	if len(mpd.calls) != 1 {
		t.Errorf("mpd.calls = %v, want exactly 1 — auto-resolution should still be tried first", mpd.calls)
	}
}

func TestResolveFallsBackToCustomArtistArtWhenNoAlbumFallback(t *testing.T) {
	a, _, _ := newTestArtAdapter(t)
	srv := fakeImageServer(t, []byte("\xff\xd8\xff\xe0artistjpeg"))
	if _, err := a.SetCustomArt("artist", "Fleetwood Mac", srv.URL); err != nil {
		t.Fatalf("SetCustomArt: %v", err)
	}

	path, ok, err := a.Resolve("http://example.com/a.mp3", "Fleetwood Mac", "Rumours")
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	if !ok || path == "" {
		t.Fatalf("Resolve() = %q, %v, want the custom artist fallback used", path, ok)
	}
}

func TestResolveAlbumFallbackTakesPriorityOverArtistFallback(t *testing.T) {
	a, _, _ := newTestArtAdapter(t)
	albumSrv := fakeImageServer(t, []byte("\xff\xd8\xff\xe0albumjpeg"))
	artistSrv := fakeImageServer(t, []byte("\xff\xd8\xff\xe0artistjpeg"))
	if _, err := a.SetCustomArt("album", "Rumours", albumSrv.URL); err != nil {
		t.Fatalf("SetCustomArt album: %v", err)
	}
	if _, err := a.SetCustomArt("artist", "Fleetwood Mac", artistSrv.URL); err != nil {
		t.Fatalf("SetCustomArt artist: %v", err)
	}

	path, ok, err := a.Resolve("http://example.com/a.mp3", "Fleetwood Mac", "Rumours")
	if err != nil || !ok {
		t.Fatalf("Resolve() = %q, %v, %v, want a resolved path with no error", path, ok, err)
	}

	albumPath, _, _ := a.Resolve("http://example.com/b.mp3", "", "Rumours")
	if path != albumPath {
		t.Errorf("Resolve() path = %q, want the album fallback's path (%q), not the artist one", path, albumPath)
	}
}

func TestResolveWithNoCustomFallbackStillRecordsConfirmedNoArt(t *testing.T) {
	a, mpd, _ := newTestArtAdapter(t) // mpd has nothing, no coverArt, no custom fallback set
	_ = mpd

	path, ok, err := a.Resolve("http://example.com/a.mp3", "Fleetwood Mac", "Rumours")
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	if ok || path != "" {
		t.Errorf("Resolve() = %q, %v, want ok=false with nothing to fall back to", path, ok)
	}
}

func TestClearCustomArtRevertsTrackToAutoResolution(t *testing.T) {
	a, mpd, _ := newTestArtAdapter(t)
	srv := fakeImageServer(t, []byte("\xff\xd8\xff\xe0customjpeg"))
	mpd.data["http://example.com/a.mp3"] = []byte("\xff\xd8\xff\xe0mpdjpeg")

	if _, err := a.SetCustomArt("track", "http://example.com/a.mp3", srv.URL); err != nil {
		t.Fatalf("SetCustomArt: %v", err)
	}
	if err := a.ClearCustomArt("track", "http://example.com/a.mp3"); err != nil {
		t.Fatalf("ClearCustomArt: %v", err)
	}

	if a.store.IsCustom("http://example.com/a.mp3") {
		t.Error("IsCustom() = true after ClearCustomArt, want false")
	}

	path, ok, err := a.Resolve("http://example.com/a.mp3", "", "")
	if err != nil || !ok || path == "" {
		t.Fatalf("Resolve() after clear = %q, %v, %v, want mpd's own art resolved", path, ok, err)
	}
	if len(mpd.calls) != 1 {
		t.Errorf("mpd.calls = %v, want exactly 1 — auto-resolution should run again after clearing", mpd.calls)
	}
}

func TestClearCustomArtOnUnsetKeyIsNotAnError(t *testing.T) {
	a, _, _ := newTestArtAdapter(t)
	if err := a.ClearCustomArt("album", "Never Set"); err != nil {
		t.Errorf("ClearCustomArt() on an unset key: error = %v, want nil", err)
	}
}
