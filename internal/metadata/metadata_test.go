package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// sample.mp3 is a small ID3v2.4-tagged file (Title="Test Title",
// Artist="Test Artist", Album="Test Album") borrowed from
// github.com/dhowden/tag's own test fixtures.
const sampleMP3Path = "testdata/sample.mp3"

func serveFile(t *testing.T, path string) *httptest.Server {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, path, time.Now(), strings.NewReader(string(data)))
	}))
}

func TestFetchExtractsTagsFromID3v2MP3(t *testing.T) {
	srv := serveFile(t, sampleMP3Path)
	defer srv.Close()

	f := &Fetcher{}
	tags, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch() error = %v, want nil", err)
	}
	if tags.Title != "Test Title" {
		t.Errorf("Title = %q, want %q", tags.Title, "Test Title")
	}
	if tags.Artist != "Test Artist" {
		t.Errorf("Artist = %q, want %q", tags.Artist, "Test Artist")
	}
	if tags.Album != "Test Album" {
		t.Errorf("Album = %q, want %q", tags.Album, "Test Album")
	}
}

func TestFetchRejectsNonHTTPURL(t *testing.T) {
	f := &Fetcher{}
	if _, err := f.Fetch(context.Background(), "file:///some/local/path.mp3"); err == nil {
		t.Error("Fetch on a non-http(s) URL: want error, got nil")
	}
}

func TestFetchErrorsOnUnreachableHost(t *testing.T) {
	f := &Fetcher{Timeout: 200 * time.Millisecond}
	if _, err := f.Fetch(context.Background(), "http://127.0.0.1:0/stream.mp3"); err == nil {
		t.Error("Fetch on an unreachable host: want error, got nil")
	}
}

func TestFetchErrorsOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	f := &Fetcher{}
	if _, err := f.Fetch(context.Background(), srv.URL); err == nil {
		t.Error("Fetch on a 404: want error, got nil")
	}
}

func TestFetchErrorsOnDataWithNoReadableTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A response that isn't a recognizable audio/tag container at all
		// (mimicking a bare internet radio stream with no embedded tags).
		w.Write([]byte("not an audio file, just some bytes"))
	}))
	defer srv.Close()

	f := &Fetcher{}
	if _, err := f.Fetch(context.Background(), srv.URL); err == nil {
		t.Error("Fetch on untagged data: want error, got nil")
	}
}

func TestFetchRespectsContextCancellation(t *testing.T) {
	unblock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-unblock
	}))
	defer srv.Close()
	defer close(unblock)

	f := &Fetcher{Timeout: 100 * time.Millisecond}
	start := time.Now()
	_, err := f.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Error("Fetch: want a timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Fetch took %v, want it bounded by the configured timeout", elapsed)
	}
}
