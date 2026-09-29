// Package bucket is an on-disk cache of downloaded audio files, keyed by
// their source URL. A Store either backs the evictable "bucket" playback
// mode (internal/config.ModeBucket — size-capped, least-recently-played
// entries evicted to make room) or the permanent favorites archive (its own
// size cap, but never self-evicts: a full favorites archive just refuses
// new downloads rather than deleting an existing "permanent" one) — both
// are the same type, just opened with evictable=false for favorites.
package bucket

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

// indexFileName holds the filename -> source-URL mapping alongside the
// cached files themselves — the filename alone (a content hash, see keyFor)
// can't be reversed back into the URL it came from, so without this, List
// would have nothing meaningful to show. Best-effort: if it's ever missing
// or corrupt, affected entries just show up with an empty URL rather than
// failing the whole Store.
const indexFileName = ".index.json"

// Store is a directory of cached files, safe for concurrent use.
type Store struct {
	mu        sync.Mutex
	dir       string
	maxSize   int64 // bytes; <= 0 means uncapped
	evictable bool  // false for the permanent favorites archive
	diskFree  func(dir string) (int64, error)
	urls      map[string]string // filename -> source URL

	progressMu sync.Mutex
	progress   map[string]*downloadProgress // url -> in-flight download, while Download runs
}

// Progress reports one in-flight Download's status, for a UI to poll —
// e.g. "downloading a 40MB file, 12MB received so far".
type Progress struct {
	URL           string
	ReceivedBytes int64
	// TotalBytes is 0 if the origin didn't send a Content-Length (common
	// for live/chunked streams) — a UI should show a spinner rather than a
	// percentage in that case.
	TotalBytes int64
}

// downloadProgress is the mutable state behind one Progress entry;
// received is updated from the copy loop via a counting io.Reader while
// total is fixed at the start (from the response's Content-Length, if any).
type downloadProgress struct {
	total    int64
	received atomic.Int64
}

// countingReader wraps a Reader, adding every byte read to counter — used
// to track Download's progress without buffering or re-reading anything.
type countingReader struct {
	r       io.Reader
	counter *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.counter.Add(int64(n))
	}
	return n, err
}

// Open opens (creating if necessary) a cache directory. maxSize <= 0 means
// no size cap. evictable controls whether Download may delete this store's
// own least-recently-used entries to make room (true for the bucket
// playback cache) or must instead fail the download outright when full
// (false for the permanent favorites archive).
func Open(dir string, maxSize int64, evictable bool) (*Store, error) {
	// Resolved to absolute up front: paths returned by Lookup/Download are
	// handed to mpd's `add` command, which treats anything without a "://"
	// scheme as relative to *its own* music_directory — a relative bucket
	// path here would have mpd searching its music library for a
	// subdirectory that doesn't exist there, not the daemon's cache dir.
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve absolute path for %s: %w", dir, err)
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		return nil, fmt.Errorf("create bucket dir %s: %w", abs, err)
	}
	s := &Store{dir: abs, maxSize: maxSize, evictable: evictable, diskFree: realDiskFree}
	s.urls = loadIndex(abs)
	s.progress = make(map[string]*downloadProgress)
	return s, nil
}

// DownloadProgress reports every Download currently in flight on this
// store, for a UI to poll (see Progress).
func (s *Store) DownloadProgress() []Progress {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	out := make([]Progress, 0, len(s.progress))
	for url, p := range s.progress {
		out = append(out, Progress{URL: url, ReceivedBytes: p.received.Load(), TotalBytes: p.total})
	}
	return out
}

func loadIndex(dir string) map[string]string {
	data, err := os.ReadFile(filepath.Join(dir, indexFileName))
	if err != nil {
		return map[string]string{}
	}
	var urls map[string]string
	if err := json.Unmarshal(data, &urls); err != nil {
		return map[string]string{}
	}
	return urls
}

// saveIndexLocked persists s.urls. Failures are logged-worthy but not
// fatal to the caller — the actual cached file is the source of truth for
// hit/miss purposes; losing the index only degrades List()'s URL labels
// until the next successful save. Called with s.mu held.
func (s *Store) saveIndexLocked() error {
	data, err := json.Marshal(s.urls)
	if err != nil {
		return fmt.Errorf("marshal bucket index: %w", err)
	}
	return os.WriteFile(filepath.Join(s.dir, indexFileName), data, 0644)
}

// SetMaxSize changes the size cap live (e.g. when the user edits it via the
// Settings tab) — it does not immediately evict; the next Download will.
func (s *Store) SetMaxSize(maxSize int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxSize = maxSize
}

// hashFor is url's content-addressed identity, shared by keyFor (which
// adds a URL-guessed extension) and DownloadVia (which instead preserves
// the extension of whatever file its fetch callback actually produced).
func hashFor(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:])
}

// keyFor derives a stable filename for url: hashFor's content hash (so
// arbitrary URL characters/length are never a filesystem concern) plus a
// best-effort extension guessed from the URL itself (cosmetic only —
// nothing parses it back). Used by Download/Lookup/Contains, where the
// URL is a plain HTTP one and its own path usually does carry a real
// extension; DownloadVia can't use this, since e.g. a YouTube watch link
// carries no such information at all.
func keyFor(url string) string {
	hash := hashFor(url) // computed on the full, untruncated url
	extSource := url
	if i := strings.IndexAny(extSource, "?#"); i >= 0 {
		extSource = extSource[:i]
	}
	ext := ".audio"
	if e := filepath.Ext(extSource); e != "" && len(e) <= 5 {
		ext = e
	}
	return hash + ext
}

// find locates url's cached file regardless of which extension it was
// actually stored under: Download guesses one from the URL itself
// (keyFor), but DownloadVia instead preserves whatever its fetch callback
// produced (see its own doc comment for why — a URL like a YouTube watch
// link carries no usable extension at all) — so Contains/Lookup/Remove
// can't assume keyFor's guess is the real filename and must search by
// hash prefix instead, matching either naming convention.
func (s *Store) find(url string) (string, bool) {
	matches, err := filepath.Glob(filepath.Join(s.dir, hashFor(url)+".*"))
	if err != nil || len(matches) == 0 {
		return "", false
	}
	return matches[0], true
}

// Contains reports whether url is already cached, without affecting its
// LRU recency — for UI "is this cached?" queries, which shouldn't count as
// a use.
func (s *Store) Contains(url string) bool {
	_, ok := s.find(url)
	return ok
}

// FilePath resolves a cache filename (as returned by Lookup/Download via
// filepath.Base, or by List's entries) back to its full path — for serving
// it back out (see cmd/pi-streamer's local-only bucket file server, which
// mpd fetches from instead of being handed a filesystem path directly: mpd
// only permits `add`-ing local files to clients connected via its Unix
// socket, which this daemon doesn't use). name is checked against this
// store's own index rather than just joined onto the directory, so a
// crafted request can't escape it via "../" or reach a file this store
// didn't itself download.
func (s *Store) FilePath(name string) (string, bool) {
	if name == "" || strings.ContainsRune(name, '/') || strings.ContainsRune(name, filepath.Separator) {
		return "", false
	}
	s.mu.Lock()
	_, known := s.urls[name]
	s.mu.Unlock()
	if !known {
		return "", false
	}
	return filepath.Join(s.dir, name), true
}

// URLForFilename returns the source URL this store originally downloaded
// to filename (as found in a resolved local file-server URL's last path
// segment — see cmd/pi-streamer's modeResolver.Unresolve). mpd only ever
// sees what Resolve handed it, never the original URL, so anything reading
// a track's identity back from mpd (Queue, Status) needs this reverse
// mapping — otherwise the daemon's own proxy address gets mistaken for the
// track's real identity, e.g. by prefetching what it thinks is "the next
// URL" and instead re-downloading its own served copy under a nonsense key.
func (s *Store) URLForFilename(name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	url, ok := s.urls[name]
	return url, ok
}

// Lookup returns the local path for url if it's already cached, bumping
// its recency for LRU purposes (this is the actual-playback path, not the
// UI-status path — see Contains for that).
func (s *Store) Lookup(url string) (path string, ok bool) {
	p, ok := s.find(url)
	if !ok {
		return "", false
	}
	now := time.Now()
	_ = os.Chtimes(p, now, now)
	return p, true
}

// Download fetches url into the cache and returns the local path. It
// streams to a temp file and renames atomically at the end, so a
// concurrent Lookup/Contains never sees a half-written file. Before
// finalizing, it makes room within the store's size cap — evicting its own
// least-recently-used entries first if evictable, or simply refusing with
// an error if not (see Open).
func (s *Store) Download(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("download: responded with status %d", resp.StatusCode)
	}

	total := resp.ContentLength
	if total < 0 {
		total = 0 // unknown (e.g. chunked/live), not "empty"
	}
	prog := &downloadProgress{total: total}
	s.progressMu.Lock()
	s.progress[url] = prog
	s.progressMu.Unlock()
	defer func() {
		s.progressMu.Lock()
		delete(s.progress, url)
		s.progressMu.Unlock()
	}()

	tmp, err := os.CreateTemp(s.dir, "download-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	size, copyErr := io.Copy(tmp, &countingReader{r: resp.Body, counter: &prog.received})
	closeErr := tmp.Close()
	if copyErr != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("download: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("close temp file: %w", closeErr)
	}

	finalName := keyFor(url)
	finalPath := filepath.Join(s.dir, finalName)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.makeRoomLocked(size, finalPath); err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("finalize download: %w", err)
	}
	s.urls[finalName] = url
	// Best-effort: the file itself downloaded fine regardless of whether
	// this succeeds, so a failure here doesn't fail the download — it only
	// means this entry shows up with a blank URL in List() until the next
	// successful save.
	_ = s.saveIndexLocked()
	return finalPath, nil
}

// DownloadVia is Download's general form, for a source mpd can't be
// pointed at directly and that isn't a plain HTTP GET either — e.g.
// internal/ytdlp extracting a YouTube video's audio track via the yt-dlp
// CLI. fetch does the actual work of producing the file: it must create a
// new file *inside* dir (the exact path is fetch's choice, e.g. via
// os.CreateTemp(dir, ...) or a tool's own output flag pointed into dir)
// and return that path — DownloadVia takes it from there: sizing it,
// making room within the size cap (see makeRoomLocked), and atomically
// renaming it into place under url's
// content-addressed name, exactly as Download does after its own HTTP
// GET. fetch's output file's own extension (whatever it actually wrote,
// e.g. ".m4a"/".opus") is preserved on the final name — unlike Download's
// keyFor, which guesses an extension from the URL itself, since a URL
// like a YouTube watch link carries no such information at all.
//
// The file must land on the same filesystem as dir: the final step is a
// rename, and a rename across filesystems doesn't work at all (or, on
// some platforms, silently degrades to a slow copy) — this is exactly why
// fetch is required to create its file inside dir to begin with, rather
// than anywhere convenient.
func (s *Store) DownloadVia(ctx context.Context, url string, fetch func(dir string) (path string, err error)) (string, error) {
	tmpPath, err := fetch(s.dir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("stat fetched file: %w", err)
	}

	ext := filepath.Ext(tmpPath)
	if ext == "" {
		ext = ".audio"
	}
	finalName := hashFor(url) + ext
	finalPath := filepath.Join(s.dir, finalName)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.makeRoomLocked(info.Size(), finalPath); err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("finalize download: %w", err)
	}
	s.urls[finalName] = url
	// Best-effort, same reasoning as Download's own save: the file itself
	// landed fine regardless of whether this succeeds.
	_ = s.saveIndexLocked()
	return finalPath, nil
}

// makeRoomLocked ensures there's room for an incoming file of the given
// size, within this store's own size cap. If evictable, it deletes this
// store's own least-recently-used entries (oldest mtime first) to get
// there; if not (the permanent favorites archive), it never deletes
// anything — it either already fits or the download is refused. Called
// with s.mu held.
func (s *Store) makeRoomLocked(incoming int64, keep string) error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("list bucket dir: %w", err)
	}

	type item struct {
		path  string
		size  int64
		mtime time.Time
	}
	var items []item
	var total int64
	for _, e := range entries {
		if e.IsDir() || !isCacheFile(e.Name()) {
			continue
		}
		p := filepath.Join(s.dir, e.Name())
		if p == keep {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, item{p, info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mtime.Before(items[j].mtime) })

	needsRoom := func() bool {
		return s.maxSize > 0 && total+incoming > s.maxSize
	}

	evicted := false
	if s.evictable {
		for needsRoom() && len(items) > 0 {
			it := items[0]
			items = items[1:]
			if err := os.Remove(it.path); err != nil {
				continue
			}
			total -= it.size
			delete(s.urls, filepath.Base(it.path))
			evicted = true
		}
	}
	if evicted {
		_ = s.saveIndexLocked() // best-effort, same as in Download
	}

	if needsRoom() {
		return fmt.Errorf("not enough space for a %d-byte file within the configured size limit", incoming)
	}
	return nil
}

// isCacheFile reports whether name is an actual cached entry — not a
// directory, an in-progress download's temp file, or the URL index itself.
func isCacheFile(name string) bool {
	return !strings.HasSuffix(name, ".tmp") && name != indexFileName
}

// Stats reports current usage. maxBytes and diskFreeBytes are <= 0 if
// uncapped / the margin isn't configured, respectively.
func (s *Store) Stats() (usedBytes, maxBytes, diskFreeBytes int64, err error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("list bucket dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !isCacheFile(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		usedBytes += info.Size()
	}
	free, ferr := s.diskFree(s.dir)
	if ferr == nil {
		diskFreeBytes = free
	}
	s.mu.Lock()
	maxBytes = s.maxSize
	s.mu.Unlock()
	return usedBytes, maxBytes, diskFreeBytes, nil
}

// realDiskFree reports bytes available (to an unprivileged process) on the
// filesystem containing dir.
func realDiskFree(dir string) (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

// Entry describes one cached file, for List. URL is empty if this entry
// predates the index (or the index was lost) — the filename alone can't be
// reversed back into it. Filename is the entry's actual on-disk name
// (content-hash-based, see keyFor/DownloadVia) — resolvable back to a full
// path via FilePath, e.g. for a caller that wants to probe the file itself
// (internal/audioinfo) rather than just report its size/URL.
type Entry struct {
	URL          string
	SizeBytes    int64
	LastAccessed time.Time
	Filename     string
}

// List returns every currently cached entry, most-recently-accessed first
// (i.e. the opposite order eviction removes them in).
func (s *Store) List() ([]Entry, error) {
	dirEntries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("list bucket dir: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Entry
	for _, e := range dirEntries {
		if e.IsDir() || !isCacheFile(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Entry{
			URL:          s.urls[e.Name()],
			SizeBytes:    info.Size(),
			LastAccessed: info.ModTime(),
			Filename:     e.Name(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastAccessed.After(out[j].LastAccessed) })
	return out, nil
}

// Remove deletes url's cached file, if present, and drops it from the
// index. It's not an error for url to not be cached — used e.g. to clean up
// a permanent favorites-archive copy when a favorite is removed, where the
// archive download may never have succeeded in the first place.
func (s *Store) Remove(url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, found := s.find(url)
	if found {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove cached file: %w", err)
		}
	}
	// Also drop the index entry by its actual name, whether or not the
	// file itself was still there — found's guessed name (keyFor's) might
	// not match what's actually indexed if this entry came from
	// DownloadVia, so look the real name up from found's path when
	// available, falling back to keyFor's guess otherwise (nothing else
	// to try if the file is already gone).
	name := keyFor(url)
	if found {
		name = filepath.Base(path)
	}
	if _, ok := s.urls[name]; ok {
		delete(s.urls, name)
		_ = s.saveIndexLocked()
	}
	return nil
}
