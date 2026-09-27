package ytdlp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIsYouTubeURLRecognizesOrdinaryVideoLinks(t *testing.T) {
	tests := []string{
		"https://www.youtube.com/watch?v=abc123XYZ90",
		"https://youtube.com/watch?v=abc123XYZ90",
		"https://m.youtube.com/watch?v=abc123XYZ90",
		"https://music.youtube.com/watch?v=abc123XYZ90",
		"https://youtu.be/abc123XYZ90",
		"http://youtu.be/abc123XYZ90",
	}
	for _, u := range tests {
		if !IsYouTubeURL(u) {
			t.Errorf("IsYouTubeURL(%q) = false, want true", u)
		}
	}
}

func TestIsYouTubeURLRejectsUnrelatedURLs(t *testing.T) {
	tests := []string{
		"https://example.com/song.mp3",
		"https://archive.org/download/some-album/track.mp3",
		"https://notyoutube.com/watch?v=abc",
		"https://youtube.com.evil.example/watch?v=abc",
		"not a url at all",
		"",
	}
	for _, u := range tests {
		if IsYouTubeURL(u) {
			t.Errorf("IsYouTubeURL(%q) = true, want false", u)
		}
	}
}

func TestVideoIDExtractsFromEveryCommonURLShape(t *testing.T) {
	const want = "abc123XYZ90"
	tests := []string{
		"https://www.youtube.com/watch?v=abc123XYZ90",
		"https://youtube.com/watch?v=abc123XYZ90",
		"https://m.youtube.com/watch?v=abc123XYZ90",
		"https://music.youtube.com/watch?v=abc123XYZ90",
		"https://youtu.be/abc123XYZ90",
		"http://youtu.be/abc123XYZ90",
		"https://www.youtube.com/embed/abc123XYZ90",
		"https://www.youtube.com/shorts/abc123XYZ90",
		"https://www.youtube.com/live/abc123XYZ90",
		// A share link's tracking param, a timestamp, and a playlist
		// reference all coexisting with ?v= — must not confuse extraction.
		"https://www.youtube.com/watch?v=abc123XYZ90&si=someTrackingToken12",
		"https://www.youtube.com/watch?v=abc123XYZ90&t=42s",
		"https://www.youtube.com/watch?list=PLxxxxxxxxxxxxxxxxxx&v=abc123XYZ90",
		// youtu.be links can also carry query params (a timestamp, a
		// share-tracking one) after the video ID path segment.
		"https://youtu.be/abc123XYZ90?si=someTrackingToken12",
		"https://youtu.be/abc123XYZ90?t=42",
		// Embed/Shorts links with a trailing path segment or query string.
		"https://www.youtube.com/embed/abc123XYZ90?rel=0",
		"https://www.youtube.com/shorts/abc123XYZ90/",
	}
	for _, u := range tests {
		got, ok := VideoID(u)
		if !ok {
			t.Errorf("VideoID(%q): want ok=true", u)
			continue
		}
		if got != want {
			t.Errorf("VideoID(%q) = %q, want %q", u, got, want)
		}
	}
}

func TestVideoIDFailsForURLsWithNoSingleVideo(t *testing.T) {
	tests := []string{
		"https://www.youtube.com/",
		"https://www.youtube.com/channel/UCxxxxxxxxxxxxxxxxxxxxxx",
		"https://www.youtube.com/playlist?list=PLxxxxxxxxxxxxxxxxxx",
		"https://example.com/not-youtube-at-all",
		"https://www.youtube.com/watch?v=tooshort",
		"not a url at all",
		"",
	}
	for _, u := range tests {
		if got, ok := VideoID(u); ok {
			t.Errorf("VideoID(%q) = (%q, true), want ok=false", u, got)
		}
	}
}

func TestThumbnailURLUsesTheExtractedVideoID(t *testing.T) {
	got, ok := ThumbnailURL("https://youtu.be/abc123XYZ90")
	if !ok {
		t.Fatal("ThumbnailURL: want ok=true")
	}
	want := "https://i.ytimg.com/vi/abc123XYZ90/maxresdefault.jpg"
	if got != want {
		t.Errorf("ThumbnailURL() = %q, want %q", got, want)
	}
}

func TestHQThumbnailURLUsesTheExtractedVideoID(t *testing.T) {
	got, ok := HQThumbnailURL("https://www.youtube.com/watch?v=abc123XYZ90")
	if !ok {
		t.Fatal("HQThumbnailURL: want ok=true")
	}
	want := "https://i.ytimg.com/vi/abc123XYZ90/hqdefault.jpg"
	if got != want {
		t.Errorf("HQThumbnailURL() = %q, want %q", got, want)
	}
}

func TestThumbnailURLFailsWithoutAnExtractableVideoID(t *testing.T) {
	if _, ok := ThumbnailURL("https://www.youtube.com/channel/UCxxxxxxxxxxxxxxxxxxxxxx"); ok {
		t.Error("ThumbnailURL: want ok=false for a URL with no video ID")
	}
	if _, ok := HQThumbnailURL("https://example.com/not-youtube"); ok {
		t.Error("HQThumbnailURL: want ok=false for a non-YouTube URL")
	}
}

// fakeYtdlpScript writes a shell script standing in for the real yt-dlp
// binary, for testing ExtractAudio's own plumbing (constructing the
// command, locating the output file it produced) without a real yt-dlp
// install or any network access — it never actually contacts YouTube.
// wantExt controls the extension of the file it "produces", to exercise
// ExtractAudio's glob-based discovery of whatever yt-dlp actually wrote.
func fakeYtdlpScript(t *testing.T, wantExt string, exitCode int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake yt-dlp script is a shell script; not run on windows")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "yt-dlp")
	// Finds its own -o argument (the second-to-last flag pair) and creates
	// a file at <that template's base>.<wantExt> instead of the literal
	// "%(ext)s" yt-dlp itself would substitute — good enough to exercise
	// ExtractAudio's glob(base + ".*") without reimplementing yt-dlp's own
	// templating.
	content := "#!/bin/sh\n" +
		"exit_code=" + itoa(exitCode) + "\n" +
		"prev=\"\"\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$prev\" = \"-o\" ]; then\n" +
		"    base=$(echo \"$arg\" | sed 's/\\.%(ext)s$//')\n" +
		"    if [ \"$exit_code\" = \"0\" ]; then\n" +
		"      touch \"$base." + wantExt + "\"\n" +
		"    fi\n" +
		"  fi\n" +
		"  prev=\"$arg\"\n" +
		"done\n" +
		"if [ \"$exit_code\" != \"0\" ]; then echo 'fake yt-dlp: simulated failure' >&2; fi\n" +
		"exit $exit_code\n"
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatalf("write fake yt-dlp script: %v", err)
	}
	return script
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	return string(rune('0' + n))
}

func TestExtractAudioFindsTheProducedFile(t *testing.T) {
	script := fakeYtdlpScript(t, "m4a", 0)
	e := &Extractor{BinaryPath: script}
	dir := t.TempDir()

	path, err := e.ExtractAudio(context.Background(), "https://youtu.be/abc123XYZ90", dir)
	if err != nil {
		t.Fatalf("ExtractAudio: %v", err)
	}
	if filepath.Ext(path) != ".m4a" {
		t.Errorf("ExtractAudio path = %q, want a .m4a file", path)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("ExtractAudio path = %q, want it inside %q", path, dir)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("produced file does not exist: %v", err)
	}
}

func TestExtractAudioFindsWhicheverExtensionWasProduced(t *testing.T) {
	// yt-dlp's chosen container varies by video (opus/webm is common for
	// bestaudio) — ExtractAudio must not assume any specific one.
	script := fakeYtdlpScript(t, "opus", 0)
	e := &Extractor{BinaryPath: script}

	path, err := e.ExtractAudio(context.Background(), "https://youtu.be/abc123XYZ90", t.TempDir())
	if err != nil {
		t.Fatalf("ExtractAudio: %v", err)
	}
	if filepath.Ext(path) != ".opus" {
		t.Errorf("ExtractAudio path = %q, want a .opus file", path)
	}
}

func TestExtractAudioPropagatesCommandFailure(t *testing.T) {
	script := fakeYtdlpScript(t, "m4a", 1)
	e := &Extractor{BinaryPath: script}

	if _, err := e.ExtractAudio(context.Background(), "https://youtu.be/abc123XYZ90", t.TempDir()); err == nil {
		t.Error("ExtractAudio: want error when the command exits non-zero, got nil")
	}
}

func TestExtractAudioErrorsWhenNoFileWasProduced(t *testing.T) {
	// Exit code 0 but the fake script "forgets" to write anything —
	// simulates a yt-dlp version/flag mismatch producing no output file
	// despite reporting success.
	script := fakeYtdlpScript(t, "m4a", 0)
	// Overwrite the script to always succeed without creating any file.
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("rewrite fake yt-dlp script: %v", err)
	}
	e := &Extractor{BinaryPath: script}

	if _, err := e.ExtractAudio(context.Background(), "https://youtu.be/abc123XYZ90", t.TempDir()); err == nil {
		t.Error("ExtractAudio: want error when no output file was produced, got nil")
	}
}

func TestExtractorDefaultsBinaryAndFormat(t *testing.T) {
	e := &Extractor{}
	if e.binary() != "yt-dlp" {
		t.Errorf("binary() = %q, want %q", e.binary(), "yt-dlp")
	}
	if e.format() != "bestaudio" {
		t.Errorf("format() = %q, want %q", e.format(), "bestaudio")
	}
}

func TestFetchTitleReturnsTheOembedTitle(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.Query().Get("url")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"title":"Example Video Title","author_name":"Example Channel"}`))
	}))
	defer srv.Close()

	f := &TitleFetcher{baseURL: srv.URL}
	title, ok := f.FetchTitle(context.Background(), "https://youtu.be/abc123XYZ90")
	if !ok {
		t.Fatal("FetchTitle: want ok=true")
	}
	if title != "Example Video Title" {
		t.Errorf("FetchTitle() = %q, want %q", title, "Example Video Title")
	}
	if gotURL != "https://youtu.be/abc123XYZ90" {
		t.Errorf("oembed request url param = %q, want the original video URL", gotURL)
	}
}

func TestFetchTitleFailsOnNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	f := &TitleFetcher{baseURL: srv.URL}
	if _, ok := f.FetchTitle(context.Background(), "https://youtu.be/abc123XYZ90"); ok {
		t.Error("FetchTitle: want ok=false on a non-200 response")
	}
}

func TestFetchTitleFailsOnEmptyTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"title":""}`))
	}))
	defer srv.Close()

	f := &TitleFetcher{baseURL: srv.URL}
	if _, ok := f.FetchTitle(context.Background(), "https://youtu.be/abc123XYZ90"); ok {
		t.Error("FetchTitle: want ok=false when the response has no title")
	}
}

func TestFetchTitleFailsOnMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	f := &TitleFetcher{baseURL: srv.URL}
	if _, ok := f.FetchTitle(context.Background(), "https://youtu.be/abc123XYZ90"); ok {
		t.Error("FetchTitle: want ok=false on a malformed response body")
	}
}

func TestThumbnailFetcherPrefersMaxresWhenAvailable(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("\xff\xd8\xff\xe0maxresbytes"))
	}))
	defer srv.Close()

	f := &ThumbnailFetcher{baseURL: srv.URL}
	data, err := f.Fetch(context.Background(), "https://youtu.be/abc123XYZ90")
	if err != nil {
		t.Fatalf("Fetch() error = %v, want nil", err)
	}
	if string(data) != "\xff\xd8\xff\xe0maxresbytes" {
		t.Errorf("Fetch() = %q, want the maxres bytes", data)
	}
	if len(gotPaths) != 1 || gotPaths[0] != "/vi/abc123XYZ90/maxresdefault.jpg" {
		t.Errorf("requested paths = %v, want exactly one request for maxresdefault.jpg", gotPaths)
	}
}

func TestThumbnailFetcherFallsBackToHQWhenMaxresIs404(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		if r.URL.Path == "/vi/abc123XYZ90/maxresdefault.jpg" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("\xff\xd8\xff\xe0hqbytes"))
	}))
	defer srv.Close()

	f := &ThumbnailFetcher{baseURL: srv.URL}
	data, err := f.Fetch(context.Background(), "https://youtu.be/abc123XYZ90")
	if err != nil {
		t.Fatalf("Fetch() error = %v, want nil", err)
	}
	if string(data) != "\xff\xd8\xff\xe0hqbytes" {
		t.Errorf("Fetch() = %q, want the hq fallback bytes", data)
	}
	if len(gotPaths) != 2 {
		t.Fatalf("requested paths = %v, want maxres attempted then hq fallback", gotPaths)
	}
	if gotPaths[1] != "/vi/abc123XYZ90/hqdefault.jpg" {
		t.Errorf("second request path = %q, want hqdefault.jpg", gotPaths[1])
	}
}

func TestThumbnailFetcherReturnsNilWithoutAnExtractableVideoID(t *testing.T) {
	f := &ThumbnailFetcher{baseURL: "http://should-not-be-contacted.invalid"}
	data, err := f.Fetch(context.Background(), "https://www.youtube.com/channel/UCxxxxxxxxxxxxxxxxxxxxxx")
	if err != nil {
		t.Fatalf("Fetch() error = %v, want nil", err)
	}
	if data != nil {
		t.Errorf("Fetch() = %v, want nil for a URL with no video ID", data)
	}
}

func TestThumbnailFetcherReturnsNilWhenBothSizesAre404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	f := &ThumbnailFetcher{baseURL: srv.URL}
	data, err := f.Fetch(context.Background(), "https://youtu.be/abc123XYZ90")
	if err != nil {
		t.Fatalf("Fetch() error = %v, want nil (a 404 is a confirmed miss, not an error)", err)
	}
	if data != nil {
		t.Errorf("Fetch() = %v, want nil when neither size exists", data)
	}
}
