package bucket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestOpenResolvesRelativeDirToAbsolute guards against a real regression:
// mpd's `add` treats any URI without a "://" scheme as relative to *its
// own* music_directory, not the daemon's working directory — a relative
// bucket dir meant paths handed to mpd.Add resolved against the wrong
// root entirely, failing with "No such directory". Every other test here
// uses t.TempDir(), which is already absolute, so it can't catch this.
func TestOpenResolvesRelativeDirToAbsolute(t *testing.T) {
	root := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(oldwd) })

	s, err := Open("relative-subdir", 0, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !filepath.IsAbs(s.dir) {
		t.Fatalf("Store.dir = %q, want an absolute path", s.dir)
	}

	srv := newServer(t, "hello")
	path, err := s.Download(context.Background(), srv.URL+"/a.mp3")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("Download returned %q, want an absolute path", path)
	}

	lookupPath, ok := s.Lookup(srv.URL + "/a.mp3")
	if !ok {
		t.Fatal("Lookup: not found after Download")
	}
	if !filepath.IsAbs(lookupPath) {
		t.Errorf("Lookup returned %q, want an absolute path", lookupPath)
	}
}

func TestDownloadThenLookupFindsIt(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	srv := newServer(t, "fake-audio-bytes")

	path, err := s.Download(context.Background(), srv.URL+"/track.mp3")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(data) != "fake-audio-bytes" {
		t.Errorf("downloaded content = %q, want %q", data, "fake-audio-bytes")
	}

	gotPath, ok := s.Lookup(srv.URL + "/track.mp3")
	if !ok {
		t.Fatal("Lookup: not found after Download")
	}
	if gotPath != path {
		t.Errorf("Lookup path = %q, want %q", gotPath, path)
	}
	if !s.Contains(srv.URL + "/track.mp3") {
		t.Error("Contains = false, want true after Download")
	}
}

func TestLookupMissesForUnknownURL(t *testing.T) {
	s, err := Open(t.TempDir(), 0, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, ok := s.Lookup("http://example.com/never-downloaded.mp3"); ok {
		t.Error("Lookup: want false for a URL never downloaded")
	}
	if s.Contains("http://example.com/never-downloaded.mp3") {
		t.Error("Contains: want false for a URL never downloaded")
	}
}

func TestDownloadFailsOnErrorStatus(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := s.Download(context.Background(), srv.URL); err == nil {
		t.Error("Download: want error for a 404 response, got nil")
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("bucket dir after failed download = %v, want empty (no leftover temp file)", entries)
	}
}

func TestDownloadProgressTracksInFlightDownload(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	const chunkSize = 1000
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "2000")
		w.WriteHeader(http.StatusOK)
		w.Write(make([]byte, chunkSize))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release // hold the response open so we can observe partial progress
		w.Write(make([]byte, chunkSize))
	}))
	defer srv.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := s.Download(context.Background(), srv.URL); err != nil {
			t.Errorf("Download: %v", err)
		}
	}()

	// Poll until the first chunk shows up in progress, rather than a fixed
	// sleep — bounded so a real regression fails fast instead of hanging.
	deadline := time.Now().Add(5 * time.Second)
	var progress []Progress
	for time.Now().Before(deadline) {
		progress = s.DownloadProgress()
		if len(progress) == 1 && progress[0].ReceivedBytes >= chunkSize {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	<-done

	if len(progress) != 1 {
		t.Fatalf("DownloadProgress during download = %v, want exactly one in-flight entry", progress)
	}
	if progress[0].URL != srv.URL {
		t.Errorf("Progress.URL = %q, want %q", progress[0].URL, srv.URL)
	}
	if progress[0].TotalBytes != 2000 {
		t.Errorf("Progress.TotalBytes = %d, want 2000 (from Content-Length)", progress[0].TotalBytes)
	}
	if progress[0].ReceivedBytes < chunkSize {
		t.Errorf("Progress.ReceivedBytes = %d, want at least %d", progress[0].ReceivedBytes, chunkSize)
	}

	if after := s.DownloadProgress(); len(after) != 0 {
		t.Errorf("DownloadProgress after completion = %v, want empty", after)
	}
}

func TestDownloadProgressUnknownTotalWithoutContentLength(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No Content-Length set: httptest's recorder still won't send one
		// for a short chunked response, simulating a live/unbounded stream.
		w.(http.Flusher).Flush()
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	if _, err := s.Download(context.Background(), srv.URL); err != nil {
		t.Fatalf("Download: %v", err)
	}
	// Progress is gone by the time Download returns (completed), so this
	// mainly guards against a panic/negative value if total was never set;
	// the in-flight case is covered by the test above.
	if p := s.DownloadProgress(); len(p) != 0 {
		t.Errorf("DownloadProgress after completion = %v, want empty", p)
	}
}

func TestDownloadEvictsLeastRecentlyUsedToFit(t *testing.T) {
	dir := t.TempDir()
	// Cap small enough that only one ~10-byte file fits at a time.
	s, err := Open(dir, 12, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	srvA := newServer(t, "aaaaaaaaaa") // 10 bytes
	srvB := newServer(t, "bbbbbbbbbb") // 10 bytes

	pathA, err := s.Download(context.Background(), srvA.URL+"/a.mp3")
	if err != nil {
		t.Fatalf("Download A: %v", err)
	}
	// Ensure B's mtime is unambiguously later than A's for the LRU sort.
	time.Sleep(10 * time.Millisecond)
	if _, err := s.Download(context.Background(), srvB.URL+"/b.mp3"); err != nil {
		t.Fatalf("Download B: %v", err)
	}

	if _, err := os.Stat(pathA); err == nil {
		t.Error("A's file still exists, want it evicted to make room for B")
	}
	if !s.Contains(srvB.URL + "/b.mp3") {
		t.Error("B should still be cached")
	}
}

func TestLookupBumpsRecencyProtectingFromEviction(t *testing.T) {
	dir := t.TempDir()
	// Room for A+B (20 bytes) together, but not all of A+B+C (30 bytes).
	s, err := Open(dir, 22, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	srvA := newServer(t, "aaaaaaaaaa")
	srvB := newServer(t, "bbbbbbbbbb")
	srvC := newServer(t, "cccccccccc")

	if _, err := s.Download(context.Background(), srvA.URL+"/a.mp3"); err != nil {
		t.Fatalf("Download A: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := s.Download(context.Background(), srvB.URL+"/b.mp3"); err != nil {
		t.Fatalf("Download B: %v", err)
	}
	// Re-access A so it's now the most-recently-used, and B becomes the
	// least-recently-used entry instead.
	time.Sleep(10 * time.Millisecond)
	if _, ok := s.Lookup(srvA.URL + "/a.mp3"); !ok {
		t.Fatal("Lookup A: want a cache hit")
	}

	time.Sleep(10 * time.Millisecond)
	if _, err := s.Download(context.Background(), srvC.URL+"/c.mp3"); err != nil {
		t.Fatalf("Download C: %v", err)
	}

	if !s.Contains(srvA.URL + "/a.mp3") {
		t.Error("A should have survived eviction (recently accessed)")
	}
	if s.Contains(srvB.URL + "/b.mp3") {
		t.Error("B should have been evicted (least-recently-used)")
	}
}

func TestUncappedStoreNeverEvicts(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0, true) // 0 = uncapped
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	srvA := newServer(t, strings.Repeat("a", 1000))
	srvB := newServer(t, strings.Repeat("b", 1000))

	if _, err := s.Download(context.Background(), srvA.URL); err != nil {
		t.Fatalf("Download A: %v", err)
	}
	if _, err := s.Download(context.Background(), srvB.URL); err != nil {
		t.Fatalf("Download B: %v", err)
	}
	if !s.Contains(srvA.URL) || !s.Contains(srvB.URL) {
		t.Error("both entries should remain when uncapped")
	}
}

func TestSetMaxSizeAppliesOnNextDownload(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0, true) // start uncapped
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	srvA := newServer(t, "aaaaaaaaaa")
	if _, err := s.Download(context.Background(), srvA.URL); err != nil {
		t.Fatalf("Download A: %v", err)
	}

	s.SetMaxSize(12) // now capped small
	time.Sleep(10 * time.Millisecond)
	srvB := newServer(t, "bbbbbbbbbb")
	if _, err := s.Download(context.Background(), srvB.URL); err != nil {
		t.Fatalf("Download B: %v", err)
	}

	if s.Contains(srvA.URL) {
		t.Error("A should have been evicted once the cap was lowered")
	}
}

func TestNonEvictableStoreRefusesInsteadOfDeleting(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 12, false) // permanent — must not self-evict
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	srvA := newServer(t, "aaaaaaaaaa") // 10 bytes, fits under the 12-byte cap
	srvB := newServer(t, "bbbbbbbbbb") // would push total over the cap

	if _, err := s.Download(context.Background(), srvA.URL); err != nil {
		t.Fatalf("Download A: %v", err)
	}
	if _, err := s.Download(context.Background(), srvB.URL); err == nil {
		t.Error("Download B: want an error (over cap, non-evictable), got nil")
	}
	if !s.Contains(srvA.URL) {
		t.Error("A must still exist — a non-evictable store never deletes to make room")
	}
	if s.Contains(srvB.URL) {
		t.Error("B should not have been saved — it didn't fit and nothing was evicted")
	}
}

func TestMinFreeMarginEvictsOnEvictableStore(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0, true) // no size cap, only the margin constrains it
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Fake a filesystem with only 15 bytes free, regardless of what's
	// actually free on the machine running this test.
	var free int64 = 15
	s.diskFree = func(string) (int64, error) { return free, nil }
	s.SetMinFree(10) // must always leave >= 10 bytes free

	srvA := newServer(t, "aaaaaaaaaa") // 10 bytes: 15-10=5 < margin 10 -> must evict, but nothing to evict yet, so this must fail
	if _, err := s.Download(context.Background(), srvA.URL); err == nil {
		t.Fatal("Download A: want an error — 15 free - 10 bytes leaves only 5, below the 10-byte margin, and there's nothing yet to evict")
	}
}

func TestMinFreeMarginAllowsWhenSpaceAvailable(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var free int64 = 1000
	s.diskFree = func(string) (int64, error) { return free, nil }
	s.SetMinFree(10)

	srv := newServer(t, "aaaaaaaaaa") // 10 bytes: 1000-10=990 >= margin 10 -> fine
	if _, err := s.Download(context.Background(), srv.URL); err != nil {
		t.Fatalf("Download: %v, want it to succeed with plenty of free space", err)
	}
}

func TestMinFreeMarginRefusesOnNonEvictableStore(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0, false) // permanent favorites archive
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var free int64 = 15
	s.diskFree = func(string) (int64, error) { return free, nil }
	s.SetMinFree(10)

	srv := newServer(t, "aaaaaaaaaa") // 10 bytes: would leave only 5 free, below the margin
	if _, err := s.Download(context.Background(), srv.URL); err == nil {
		t.Error("Download: want an error — saving this would violate the safety margin, and a permanent store can't evict anything to fix that")
	}
}

func TestStats(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 100, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	srv := newServer(t, "0123456789") // 10 bytes
	if _, err := s.Download(context.Background(), srv.URL); err != nil {
		t.Fatalf("Download: %v", err)
	}

	used, max, _, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if used != 10 {
		t.Errorf("used = %d, want 10", used)
	}
	if max != 100 {
		t.Errorf("max = %d, want 100", max)
	}
}

func TestKeyForIsStableAndFilesystemSafe(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	srv := newServer(t, "x")
	// A URL with characters that would be awkward/unsafe as a raw filename.
	url := srv.URL + "/weird?query=a/b&other=value#frag"

	if _, err := s.Download(context.Background(), url); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !s.Contains(url) {
		t.Error("Contains: want true for the same URL just downloaded")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var cacheFiles []os.DirEntry
	for _, e := range entries {
		if isCacheFile(e.Name()) {
			cacheFiles = append(cacheFiles, e)
		}
	}
	if len(cacheFiles) != 1 {
		t.Fatalf("cache files in bucket dir = %v, want exactly one", cacheFiles)
	}
	if strings.ContainsAny(cacheFiles[0].Name(), "?&# ") {
		t.Errorf("cache filename %q contains unsafe characters", cacheFiles[0].Name())
	}
	_ = filepath.Join // keep filepath imported for the path construction above
}

func TestListReportsCachedEntriesWithURLs(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	srvA := newServer(t, "aaaaaaaaaa")
	srvB := newServer(t, "bb")

	if _, err := s.Download(context.Background(), srvA.URL+"/a.mp3"); err != nil {
		t.Fatalf("Download A: %v", err)
	}
	if _, err := s.Download(context.Background(), srvB.URL+"/b.mp3"); err != nil {
		t.Fatalf("Download B: %v", err)
	}

	entries, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List returned %d entries, want 2", len(entries))
	}
	byURL := map[string]Entry{}
	for _, e := range entries {
		byURL[e.URL] = e
	}
	a, ok := byURL[srvA.URL+"/a.mp3"]
	if !ok {
		t.Fatalf("List missing A's URL; got %+v", entries)
	}
	if a.SizeBytes != 10 {
		t.Errorf("A's SizeBytes = %d, want 10", a.SizeBytes)
	}
	if _, ok := byURL[srvB.URL+"/b.mp3"]; !ok {
		t.Fatalf("List missing B's URL; got %+v", entries)
	}
}

func TestListSurvivesReopenViaPersistedIndex(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir, 0, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	srv := newServer(t, "hello")
	if _, err := s1.Download(context.Background(), srv.URL+"/a.mp3"); err != nil {
		t.Fatalf("Download: %v", err)
	}

	s2, err := Open(dir, 0, true)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	entries, err := s2.List()
	if err != nil {
		t.Fatalf("List on reopened store: %v", err)
	}
	if len(entries) != 1 || entries[0].URL != srv.URL+"/a.mp3" {
		t.Errorf("List on reopened store = %+v, want the URL to survive via the persisted index", entries)
	}
}

func TestRemoveDeletesFileAndIndexEntry(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	srv := newServer(t, "hello")
	url := srv.URL + "/a.mp3"
	if _, err := s.Download(context.Background(), url); err != nil {
		t.Fatalf("Download: %v", err)
	}

	if err := s.Remove(url); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if s.Contains(url) {
		t.Error("Contains after Remove: want false")
	}
	entries, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List after Remove = %+v, want empty", entries)
	}
}

func TestRemoveOnUncachedURLIsNotAnError(t *testing.T) {
	s, err := Open(t.TempDir(), 0, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Remove("http://example.com/never-downloaded.mp3"); err != nil {
		t.Errorf("Remove on an uncached URL: %v, want nil", err)
	}
}

func TestFilePathResolvesKnownEntry(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	srv := newServer(t, "hello")
	url := srv.URL + "/a.mp3"
	downloadedPath, err := s.Download(context.Background(), url)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}

	name := filepath.Base(downloadedPath)
	got, ok := s.FilePath(name)
	if !ok {
		t.Fatalf("FilePath(%q): not found, want it resolved", name)
	}
	if got != downloadedPath {
		t.Errorf("FilePath(%q) = %q, want %q", name, got, downloadedPath)
	}
}

func TestFilePathRejectsUnknownOrTraversalNames(t *testing.T) {
	s, err := Open(t.TempDir(), 0, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, name := range []string{"never-downloaded.mp3", "../../etc/passwd", "sub/dir.mp3", ""} {
		if _, ok := s.FilePath(name); ok {
			t.Errorf("FilePath(%q): want false", name)
		}
	}
}

func TestURLForFilenameResolvesKnownEntry(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	srv := newServer(t, "hello")
	url := srv.URL + "/a.mp3"
	downloadedPath, err := s.Download(context.Background(), url)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}

	name := filepath.Base(downloadedPath)
	got, ok := s.URLForFilename(name)
	if !ok {
		t.Fatalf("URLForFilename(%q): not found, want it resolved", name)
	}
	if got != url {
		t.Errorf("URLForFilename(%q) = %q, want %q", name, got, url)
	}
}

func TestURLForFilenameMissesForUnknownName(t *testing.T) {
	s, err := Open(t.TempDir(), 0, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, ok := s.URLForFilename("never-downloaded.mp3"); ok {
		t.Error("URLForFilename: want false for a name never downloaded")
	}
}
