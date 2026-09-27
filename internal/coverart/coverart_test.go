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
