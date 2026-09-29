package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"pi-streamer/internal/audioinfo"
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

// fakeTrackInfoLookup is an in-memory trackInfoLookup for tests.
type fakeTrackInfoLookup struct {
	results map[string][3]string // url -> [title, artist, album]
	err     error
}

func (f *fakeTrackInfoLookup) TrackInfo(url string) (title, artist, album string, err error) {
	if f.err != nil {
		return "", "", "", f.err
	}
	r := f.results[url]
	return r[0], r[1], r[2], nil
}

// fakeAudioProber is an in-memory audioProber for tests.
type fakeAudioProber struct {
	info  audioinfo.Info
	err   error
	calls []string
}

func (f *fakeAudioProber) Probe(ctx context.Context, path string) (audioinfo.Info, error) {
	f.calls = append(f.calls, path)
	if f.err != nil {
		return audioinfo.Info{}, f.err
	}
	return f.info, nil
}

func newTestBucketAdapter(t *testing.T) (*bucketAdapter, *bucket.Store) {
	t.Helper()
	cache, err := bucket.Open(t.TempDir(), 0, true)
	if err != nil {
		t.Fatalf("bucket.Open: %v", err)
	}
	favorites, err := bucket.Open(t.TempDir(), 0, false)
	if err != nil {
		t.Fatalf("bucket.Open: %v", err)
	}
	cfgStore, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("config.Open: %v", err)
	}
	return &bucketAdapter{cfg: cfgStore, cache: cache, favorites: favorites}, cache
}

func TestBucketAdapterListEnrichesWithTrackInfoAndAudioInfo(t *testing.T) {
	b, cache := newTestBucketAdapter(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fake-audio-bytes"))
	}))
	defer srv.Close()
	if _, err := cache.Download(context.Background(), srv.URL+"/track.mp3"); err != nil {
		t.Fatalf("Download: %v", err)
	}

	b.trackInfo = &fakeTrackInfoLookup{results: map[string][3]string{
		srv.URL + "/track.mp3": {"Dreams", "Fleetwood Mac", "Rumours"},
	}}
	prober := &fakeAudioProber{info: audioinfo.Info{Codec: "mp3", SampleRateHz: 44100, Channels: 2, BitrateKbps: 320}}
	b.prober = prober

	entries, err := b.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want exactly one", entries)
	}
	e := entries[0]
	if e.Title != "Dreams" || e.Artist != "Fleetwood Mac" || e.Album != "Rumours" {
		t.Errorf("title/artist/album = %q/%q/%q, want Dreams/Fleetwood Mac/Rumours", e.Title, e.Artist, e.Album)
	}
	if e.Codec != "mp3" || e.SampleRateHz != 44100 || e.Channels != 2 || e.BitrateKbps != 320 {
		t.Errorf("audio info = %+v, want the fake prober's result", e)
	}
	if len(prober.calls) != 1 {
		t.Errorf("prober.calls = %v, want exactly one probe", prober.calls)
	}
}

func TestBucketAdapterListSkipsEnrichmentWhenNotConfigured(t *testing.T) {
	b, cache := newTestBucketAdapter(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fake-audio-bytes"))
	}))
	defer srv.Close()
	if _, err := cache.Download(context.Background(), srv.URL+"/track.mp3"); err != nil {
		t.Fatalf("Download: %v", err)
	}

	// b.trackInfo/b.prober deliberately left nil.
	entries, err := b.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want exactly one", entries)
	}
	if entries[0].Title != "" || entries[0].Codec != "" {
		t.Errorf("entry = %+v, want no enrichment with nil trackInfo/prober", entries[0])
	}
}

func TestBucketAdapterRemoveDeletesFromPlaybackCacheOnly(t *testing.T) {
	b, cache := newTestBucketAdapter(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fake-audio-bytes"))
	}))
	defer srv.Close()
	if _, err := cache.Download(context.Background(), srv.URL+"/track.mp3"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !cache.Contains(srv.URL + "/track.mp3") {
		t.Fatal("expected the track to be cached before Remove")
	}

	if err := b.Remove(srv.URL + "/track.mp3"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if cache.Contains(srv.URL + "/track.mp3") {
		t.Error("Remove: track still cached after removal")
	}
}
