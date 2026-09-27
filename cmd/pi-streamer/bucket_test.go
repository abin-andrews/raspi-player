package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"pi-streamer/internal/bucket"
	"pi-streamer/internal/config"
	"pi-streamer/internal/urlcheck"
	"pi-streamer/internal/ytdlp"
)

// fakeYtdlpScript writes a shell script standing in for the real yt-dlp
// binary — mirrors internal/ytdlp's own test helper, duplicated here
// rather than exported across packages for a one-off test helper. It
// never contacts a real YouTube URL.
func fakeYtdlpScript(t *testing.T, ext string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake yt-dlp script is a shell script; not run on windows")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "yt-dlp")
	content := "#!/bin/sh\n" +
		"prev=\"\"\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$prev\" = \"-o\" ]; then\n" +
		"    base=$(echo \"$arg\" | sed 's/\\.%(ext)s$//')\n" +
		"    touch \"$base." + ext + "\"\n" +
		"  fi\n" +
		"  prev=\"$arg\"\n" +
		"done\n" +
		"exit 0\n"
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatalf("write fake yt-dlp script: %v", err)
	}
	return script
}

func newTestModeResolver(t *testing.T, extractorBinary string) (*modeResolver, *bucket.Store) {
	t.Helper()
	cache, err := bucket.Open(t.TempDir(), 0, true)
	if err != nil {
		t.Fatalf("bucket.Open: %v", err)
	}
	cfgStore, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("config.Open: %v", err)
	}
	r := &modeResolver{
		cfg:           cfgStore,
		checker:       &urlcheck.Checker{},
		cache:         cache,
		streamBaseURL: "http://127.0.0.1:8082",
		ytdlp:         &ytdlp.Extractor{BinaryPath: extractorBinary},
	}
	return r, cache
}

func TestResolveRoutesYouTubeURLsThroughYtdlpRegardlessOfPlaybackMode(t *testing.T) {
	script := fakeYtdlpScript(t, "m4a")
	r, _ := newTestModeResolver(t, script)
	// Explicitly stream mode — a YouTube URL still can't be streamed
	// directly (there's no media file at that URL, only a video page), so
	// it must go through extraction regardless of this setting.
	if err := r.cfg.Set(config.Config{Bucket: config.Bucket{Mode: config.ModeStream}}); err != nil {
		t.Fatalf("cfg.Set: %v", err)
	}

	got, err := r.Resolve("https://www.youtube.com/watch?v=abc123XYZ90")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if filepath.Ext(got) != ".m4a" {
		t.Errorf("Resolve() = %q, want a URL onto the extracted .m4a file", got)
	}
	if got[:len(r.streamBaseURL)] != r.streamBaseURL {
		t.Errorf("Resolve() = %q, want it to start with %q", got, r.streamBaseURL)
	}
}

func TestResolveReusesAlreadyExtractedYouTubeAudio(t *testing.T) {
	script := fakeYtdlpScript(t, "opus")
	r, _ := newTestModeResolver(t, script)

	first, err := r.Resolve("https://youtu.be/abc123XYZ90")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Swap in a script that would fail if actually invoked again, so a
	// second Resolve for the same URL only passes if it hit the cache
	// (Lookup) instead of re-extracting.
	failScript := filepath.Join(t.TempDir(), "yt-dlp")
	if err := os.WriteFile(failScript, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatalf("write failing script: %v", err)
	}
	r.ytdlp = &ytdlp.Extractor{BinaryPath: failScript}

	second, err := r.Resolve("https://youtu.be/abc123XYZ90")
	if err != nil {
		t.Fatalf("Resolve (second call): %v", err)
	}
	if second != first {
		t.Errorf("second Resolve() = %q, want the same cached URL as the first (%q)", second, first)
	}
}

func TestUnresolveReversesAnExtractedYouTubeURLBackToTheOriginal(t *testing.T) {
	script := fakeYtdlpScript(t, "m4a")
	r, _ := newTestModeResolver(t, script)
	original := "https://www.youtube.com/watch?v=abc123XYZ90"

	resolved, err := r.Resolve(original)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := r.Unresolve(resolved); got != original {
		t.Errorf("Unresolve(%q) = %q, want %q", resolved, got, original)
	}
}

func TestResolveNonYouTubeURLIsUnaffectedByYtdlpBeingConfigured(t *testing.T) {
	// A yt-dlp extractor being configured must not change ordinary
	// (non-YouTube) URL handling at all — IsYouTubeURL's own host check is
	// what gates this, not merely whether r.ytdlp is non-nil.
	script := fakeYtdlpScript(t, "m4a")
	r, _ := newTestModeResolver(t, script)
	if err := r.cfg.Set(config.Config{Bucket: config.Bucket{Mode: config.ModeStream}}); err != nil {
		t.Fatalf("cfg.Set: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fake-audio-bytes"))
	}))
	defer srv.Close()

	got, err := r.Resolve(srv.URL + "/track.mp3")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != srv.URL+"/track.mp3" {
		t.Errorf("Resolve() = %q, want the original URL unchanged in stream mode", got)
	}
}
