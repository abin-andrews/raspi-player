package coverart

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchRequiresArtistAndAlbum(t *testing.T) {
	f := &Fetcher{}
	tests := []struct{ artist, album string }{
		{"", "Rumours"},
		{"Fleetwood Mac", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if _, err := f.Fetch(context.Background(), tt.artist, tt.album); err == nil {
			t.Errorf("Fetch(%q, %q): want error, got nil", tt.artist, tt.album)
		}
	}
}

func TestFetchFindsAndReturnsCoverArt(t *testing.T) {
	var gotUserAgent string
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"releases":[{"id":"abc-123-mbid"}]}`))
	}))
	defer search.Close()

	archive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/release/abc-123-mbid/front" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("\xff\xd8\xff\xe0fakejpegbytes"))
	}))
	defer archive.Close()

	f := &Fetcher{baseURL: search.URL, archiveURL: archive.URL, minInterval: time.Millisecond}
	data, err := f.Fetch(context.Background(), "Fleetwood Mac", "Rumours")
	if err != nil {
		t.Fatalf("Fetch() error = %v, want nil", err)
	}
	if len(data) == 0 {
		t.Fatal("Fetch() returned no data, want the fake cover bytes")
	}
	if gotUserAgent == "" {
		t.Error("search request had no User-Agent set (MusicBrainz requires one)")
	}
}

func TestFetchReturnsNilWhenReleaseNotFound(t *testing.T) {
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"releases":[]}`))
	}))
	defer search.Close()

	f := &Fetcher{baseURL: search.URL, minInterval: time.Millisecond}
	data, err := f.Fetch(context.Background(), "Some Artist", "Some Album")
	if err != nil {
		t.Fatalf("Fetch() error = %v, want nil", err)
	}
	if data != nil {
		t.Errorf("Fetch() = %v, want nil (no matching release)", data)
	}
}

func TestFetchReturnsNilWhenArchiveHasNoFrontCover(t *testing.T) {
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"releases":[{"id":"abc-123-mbid"}]}`))
	}))
	defer search.Close()

	archive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer archive.Close()

	f := &Fetcher{baseURL: search.URL, archiveURL: archive.URL, minInterval: time.Millisecond}
	data, err := f.Fetch(context.Background(), "Fleetwood Mac", "Rumours")
	if err != nil {
		t.Fatalf("Fetch() error = %v, want nil", err)
	}
	if data != nil {
		t.Errorf("Fetch() = %v, want nil (no front cover archived)", data)
	}
}

func TestFetchPropagatesSearchError(t *testing.T) {
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer search.Close()

	f := &Fetcher{baseURL: search.URL, minInterval: time.Millisecond}
	if _, err := f.Fetch(context.Background(), "Artist", "Album"); err == nil {
		t.Error("Fetch(): want error on a search failure, got nil")
	}
}

func TestFetchPropagatesArchiveError(t *testing.T) {
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"releases":[{"id":"abc-123-mbid"}]}`))
	}))
	defer search.Close()

	archive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer archive.Close()

	f := &Fetcher{baseURL: search.URL, archiveURL: archive.URL, minInterval: time.Millisecond}
	if _, err := f.Fetch(context.Background(), "Artist", "Album"); err == nil {
		t.Error("Fetch(): want error on an archive failure, got nil")
	}
}

func TestSearchMetadataRequiresQuery(t *testing.T) {
	f := &Fetcher{}
	if _, err := f.SearchMetadata(context.Background(), "  ", 5); err == nil {
		t.Error("SearchMetadata(\"  \"): want error for a blank query, got nil")
	}
}

func TestSearchMetadataReturnsCandidates(t *testing.T) {
	var gotQuery string
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"recordings":[
			{"title":"Song One","artist-credit":[{"name":"Artist One"}],"releases":[{"id":"mbid-1","title":"Album One"}]},
			{"title":"Song Two","artist-credit":[{"name":"Artist Two"}],"releases":[]}
		]}`))
	}))
	defer search.Close()

	f := &Fetcher{baseURL: search.URL, minInterval: time.Millisecond}
	got, err := f.SearchMetadata(context.Background(), "song one", 5)
	if err != nil {
		t.Fatalf("SearchMetadata() error = %v, want nil", err)
	}
	if gotQuery != "song one" {
		t.Errorf("search query = %q, want %q", gotQuery, "song one")
	}
	want := []MetadataSuggestion{
		{Title: "Song One", Artist: "Artist One", Album: "Album One"},
		{Title: "Song Two", Artist: "Artist Two", Album: ""},
	}
	if len(got) != len(want) {
		t.Fatalf("SearchMetadata() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("suggestion[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSearchMetadataReturnsEmptyWhenNothingMatches(t *testing.T) {
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"recordings":[]}`))
	}))
	defer search.Close()

	f := &Fetcher{baseURL: search.URL, minInterval: time.Millisecond}
	got, err := f.SearchMetadata(context.Background(), "nothing matches this", 5)
	if err != nil {
		t.Fatalf("SearchMetadata() error = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("SearchMetadata() = %+v, want empty", got)
	}
}

func TestSearchMetadataPropagatesSearchError(t *testing.T) {
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer search.Close()

	f := &Fetcher{baseURL: search.URL, minInterval: time.Millisecond}
	if _, err := f.SearchMetadata(context.Background(), "anything", 5); err == nil {
		t.Error("SearchMetadata(): want error on a search failure, got nil")
	}
}

func TestFetchEnforcesRateLimitBetweenSearchRequests(t *testing.T) {
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"releases":[]}`))
	}))
	defer search.Close()

	f := &Fetcher{baseURL: search.URL, minInterval: 50 * time.Millisecond}

	start := time.Now()
	if _, err := f.Fetch(context.Background(), "A", "B"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Fetch(context.Background(), "C", "D"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Errorf("two Fetch() calls took %v, want at least the configured 50ms rate-limit interval between them", elapsed)
	}
}
