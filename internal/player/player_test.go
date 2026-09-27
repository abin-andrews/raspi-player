package player

import (
	"errors"
	"sync"
	"testing"
	"time"

	"pi-streamer/internal/indexer"
	"pi-streamer/internal/mpdclient"
	"pi-streamer/internal/store"
)

// fakeIndexer is an in-memory Indexer implementation for tests. Mutex-
// guarded since enrichMetadata runs its IndexURL call in its own goroutine.
type fakeIndexer struct {
	mu      sync.Mutex
	indexed []indexer.Result
	results []indexer.Result
	err     error
	// indexedCh, if non-nil, receives a copy of every IndexURL call (best
	// effort — send is non-blocking) — lets a test observe the async
	// metadata-enrichment goroutine's IndexURL call deterministically
	// instead of polling or sleeping.
	indexedCh chan indexer.Result
	// getCh, if non-nil, is signaled (best effort) on every Get call — for
	// a test asserting that indexAsync's background goroutine did
	// *nothing* further (e.g. an already fully-tagged URL, where there's
	// no IndexURL call to wait on instead): waiting for the Get call is a
	// checkpoint proving the goroutine actually ran before the test checks
	// that nothing changed.
	getCh chan struct{}
	// getCalls counts every Get call, for tests asserting an exact count
	// (e.g. "exactly one lookup per song change, not one per Status call").
	getCalls int
}

func (f *fakeIndexer) IndexURL(url, title, artist, album, tags string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	result := indexer.Result{URL: url, Title: title, Artist: artist, Album: album, Tags: tags}
	found := false
	for i, r := range f.indexed {
		if r.URL == url {
			f.indexed[i] = result
			found = true
			break
		}
	}
	if !found {
		f.indexed = append(f.indexed, result)
	}
	if f.indexedCh != nil {
		select {
		case f.indexedCh <- result:
		default:
		}
	}
	return nil
}

func (f *fakeIndexer) Search(query string, limit int) ([]indexer.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.results, nil
}

func (f *fakeIndexer) List(limit, offset int) ([]indexer.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.results, nil
}

// Get mirrors internal/search.Index.Get/internal/indexer.Client.Get against
// this fake's own indexed slice, so index()'s existing-entry dedup logic
// can be exercised in tests without a real search-indexer service.
func (f *fakeIndexer) Get(url string) (indexer.Result, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	defer func() {
		if f.getCh != nil {
			select {
			case f.getCh <- struct{}{}:
			default:
			}
		}
	}()
	if f.err != nil {
		return indexer.Result{}, false, f.err
	}
	for _, r := range f.indexed {
		if r.URL == url {
			return r, true, nil
		}
	}
	return indexer.Result{}, false, nil
}

func (f *fakeIndexer) Delete(url string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	for i, r := range f.indexed {
		if r.URL == url {
			f.indexed = append(f.indexed[:i], f.indexed[i+1:]...)
			break
		}
	}
	return nil
}

// fakeMetadataFetcher is an in-memory MetadataFetcher for tests. tags maps
// a URL to the (title, artist, album) it should report; a URL with no
// entry reports ok=false, matching "no usable metadata found".
type fakeMetadataFetcher struct {
	mu    sync.Mutex
	tags  map[string][3]string
	calls []string
}

func (f *fakeMetadataFetcher) Fetch(url string) (title, artist, album string, ok bool) {
	f.mu.Lock()
	f.calls = append(f.calls, url)
	f.mu.Unlock()
	t, found := f.tags[url]
	if !found {
		return "", "", "", false
	}
	return t[0], t[1], t[2], true
}

// fakeResolver is an in-memory Resolver implementation for tests. By
// default it passes the URL through unchanged (like stream mode with no
// checker configured); set err to make it reject, or playURI to simulate
// bucket mode returning a different local path.
type fakeResolver struct {
	err        error // returned for every URL if set
	playURI    string
	resolved   []string
	unresolved []string
	// unresolveMap, if set, maps a resolved URI back to its original URL
	// (mimicking modeResolver.Unresolve) — otherwise Unresolve is a no-op
	// passthrough, matching stream mode.
	unresolveMap map[string]string
}

func (f *fakeResolver) Resolve(url string) (string, error) {
	f.resolved = append(f.resolved, url)
	if f.err != nil {
		return "", f.err
	}
	if f.playURI != "" {
		return f.playURI, nil
	}
	return url, nil
}

func (f *fakeResolver) Unresolve(uri string) string {
	f.unresolved = append(f.unresolved, uri)
	if original, ok := f.unresolveMap[uri]; ok {
		return original
	}
	return uri
}

// fakeArchiver is an in-memory FavoriteArchiver implementation for tests.
type fakeArchiver struct {
	archived []string
	forgot   []string
}

func (f *fakeArchiver) Archive(url string) {
	f.archived = append(f.archived, url)
}

func (f *fakeArchiver) Forget(url string) {
	f.forgot = append(f.forgot, url)
}

func newTestPlayer() (*Player, *mpdclient.FakeClient, store.Store) {
	mpd := mpdclient.NewFakeClient()
	st := store.NewMemoryStore()
	return New(mpd, st, &fakeIndexer{}, nil, nil, nil), mpd, st
}

func newTestPlayerWithIndexer() (*Player, *fakeIndexer) {
	// indexedCh is buffered and only ever read by tests that specifically
	// want to synchronize on it (see waitForIndexed) — index() runs
	// asynchronously now (see its doc comment), so any test asserting on
	// idx.indexed must wait for it rather than checking immediately.
	idx := &fakeIndexer{indexedCh: make(chan indexer.Result, 4)}
	return New(mpdclient.NewFakeClient(), store.NewMemoryStore(), idx, nil, nil, nil), idx
}

// waitForIndexed blocks until index()'s background goroutine has made its
// first IndexURL call (or the test times out), returning what it indexed —
// necessary because PlayURL/AddToQueue/AddFavorite/AddToPlaylist no longer
// wait for indexing to complete before returning (see Player.index's doc
// comment on why: it must not block the caller on the search-indexer
// service's response time).
func waitForIndexed(t *testing.T, idx *fakeIndexer) indexer.Result {
	t.Helper()
	select {
	case result := <-idx.indexedCh:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the async IndexURL call")
		return indexer.Result{}
	}
}

func TestPlayURLAddsPlaysAndRecordsHistory(t *testing.T) {
	p, mpd, st := newTestPlayer()

	if err := p.PlayURL("http://example.com/stream.mp3"); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}

	if got, want := mpd.State, "play"; got != want {
		t.Errorf("mpd state = %q, want %q", got, want)
	}
	if len(mpd.Playlist) != 1 || mpd.Playlist[0] != "http://example.com/stream.mp3" {
		t.Errorf("mpd playlist = %v, want [http://example.com/stream.mp3]", mpd.Playlist)
	}

	hist, err := st.History(0)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 1 || hist[0].URL != "http://example.com/stream.mp3" {
		t.Errorf("history = %v, want one entry for the played URL", hist)
	}
}

func TestPlayURLJumpsToFrontAndPlaysImmediately(t *testing.T) {
	p, mpd, _ := newTestPlayer()

	if err := p.PlayURL("http://example.com/first.mp3"); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}

	// A second PlayURL while the first is already "playing" should switch
	// to it immediately (State stays "play", Song becomes the new URL) and
	// place it at the front of the queue, rather than just appending it
	// behind whatever was already there.
	if err := p.PlayURL("http://example.com/second.mp3"); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}

	if got, want := mpd.State, "play"; got != want {
		t.Errorf("mpd state = %q, want %q", got, want)
	}
	if got, want := mpd.Song, "http://example.com/second.mp3"; got != want {
		t.Errorf("mpd song = %q, want %q", got, want)
	}
	if len(mpd.QueueTracks) != 2 {
		t.Fatalf("queue = %+v, want 2 tracks", mpd.QueueTracks)
	}
	if got, want := mpd.QueueTracks[0].URL, "http://example.com/second.mp3"; got != want {
		t.Errorf("front of queue = %q, want %q (the just-played URL)", got, want)
	}
}

func TestPlayURLIndexesBestEffort(t *testing.T) {
	p, idx := newTestPlayerWithIndexer()

	if err := p.PlayURL("http://example.com/stream.mp3"); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}

	if got := waitForIndexed(t, idx); got.URL != "http://example.com/stream.mp3" {
		t.Errorf("indexed = %+v, want one entry for the played URL", got)
	}
}

func TestPlayURLSucceedsEvenIfIndexingFails(t *testing.T) {
	idx := &fakeIndexer{err: errors.New("indexer unreachable")}
	p := New(mpdclient.NewFakeClient(), store.NewMemoryStore(), idx, nil, nil, nil)

	if err := p.PlayURL("http://example.com/stream.mp3"); err != nil {
		t.Fatalf("PlayURL should succeed even if indexing fails, got: %v", err)
	}
}

func TestSeek(t *testing.T) {
	p, mpd, _ := newTestPlayer()

	if err := p.Seek(42.5); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	if len(mpd.SeekCalls) != 1 || mpd.SeekCalls[0] != 42500*time.Millisecond {
		t.Errorf("SeekCalls = %v, want [42.5s]", mpd.SeekCalls)
	}
}

func TestNext(t *testing.T) {
	p, mpd, _ := newTestPlayer()

	if err := p.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if mpd.NextCalls != 1 {
		t.Errorf("NextCalls = %d, want 1", mpd.NextCalls)
	}
}

func TestPrevious(t *testing.T) {
	p, mpd, _ := newTestPlayer()

	if err := p.Previous(); err != nil {
		t.Fatalf("Previous: %v", err)
	}
	if mpd.PreviousCalls != 1 {
		t.Errorf("PreviousCalls = %d, want 1", mpd.PreviousCalls)
	}
}

func TestSetVolume(t *testing.T) {
	p, mpd, _ := newTestPlayer()

	if err := p.SetVolume(50); err != nil {
		t.Fatalf("SetVolume: %v", err)
	}
	if len(mpd.VolumeCalls) != 1 || mpd.VolumeCalls[0] != 50 {
		t.Errorf("VolumeCalls = %v, want [50]", mpd.VolumeCalls)
	}
}

func TestSetVolumeClampsToRange(t *testing.T) {
	p, mpd, _ := newTestPlayer()

	if err := p.SetVolume(150); err != nil {
		t.Fatalf("SetVolume: %v", err)
	}
	if err := p.SetVolume(-10); err != nil {
		t.Fatalf("SetVolume: %v", err)
	}

	want := []int{100, 0}
	if len(mpd.VolumeCalls) != len(want) {
		t.Fatalf("VolumeCalls = %v, want %v", mpd.VolumeCalls, want)
	}
	for i, v := range want {
		if mpd.VolumeCalls[i] != v {
			t.Errorf("VolumeCalls[%d] = %d, want %d", i, mpd.VolumeCalls[i], v)
		}
	}
}

func TestSeekRelative(t *testing.T) {
	p, mpd, _ := newTestPlayer()

	if err := p.SeekRelative(-10.5); err != nil {
		t.Fatalf("SeekRelative: %v", err)
	}
	if len(mpd.SeekRelativeCalls) != 1 || mpd.SeekRelativeCalls[0] != -10500*time.Millisecond {
		t.Errorf("SeekRelativeCalls = %v, want [-10.5s]", mpd.SeekRelativeCalls)
	}
}

func TestAlbumArt(t *testing.T) {
	p, mpd, _ := newTestPlayer()
	mpd.Art = []byte("fake-art-bytes")

	art, err := p.AlbumArt("http://example.com/a.mp3")
	if err != nil {
		t.Fatalf("AlbumArt: %v", err)
	}
	if string(art) != "fake-art-bytes" {
		t.Errorf("AlbumArt = %q, want %q", art, "fake-art-bytes")
	}
}

func TestSearchDelegatesToIndexer(t *testing.T) {
	p, idx := newTestPlayerWithIndexer()
	idx.results = []indexer.Result{{URL: "http://example.com/x.mp3", Title: "X"}}

	results, err := p.Search("x", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].URL != "http://example.com/x.mp3" {
		t.Errorf("Search results = %v, want one entry", results)
	}
}

func TestSearchWithoutIndexerErrors(t *testing.T) {
	p := New(mpdclient.NewFakeClient(), store.NewMemoryStore(), nil, nil, nil, nil)

	if _, err := p.Search("x", 10); err == nil {
		t.Error("Search with no indexer configured: want error, got nil")
	}
}

func TestLibraryDelegatesToIndexer(t *testing.T) {
	p, idx := newTestPlayerWithIndexer()
	idx.results = []indexer.Result{{URL: "http://example.com/x.mp3", Title: "X"}}

	results, err := p.Library(10, 0)
	if err != nil {
		t.Fatalf("Library: %v", err)
	}
	if len(results) != 1 || results[0].URL != "http://example.com/x.mp3" {
		t.Errorf("Library results = %v, want one entry", results)
	}
}

func TestLibraryWithoutIndexerErrors(t *testing.T) {
	p := New(mpdclient.NewFakeClient(), store.NewMemoryStore(), nil, nil, nil, nil)

	if _, err := p.Library(10, 0); err == nil {
		t.Error("Library with no indexer configured: want error, got nil")
	}
}

func TestAddToLibrary(t *testing.T) {
	p, idx := newTestPlayerWithIndexer()

	if err := p.AddToLibrary("http://example.com/a.mp3", "Dreams", "Fleetwood Mac", "Rumours", "classic"); err != nil {
		t.Fatalf("AddToLibrary: %v", err)
	}

	if len(idx.indexed) != 1 {
		t.Fatalf("indexed = %v, want one entry", idx.indexed)
	}
	got := idx.indexed[0]
	if got.URL != "http://example.com/a.mp3" || got.Title != "Dreams" || got.Artist != "Fleetwood Mac" ||
		got.Album != "Rumours" || got.Tags != "classic" {
		t.Errorf("indexed = %+v, want the exact submitted fields", got)
	}
}

func TestAddToLibraryFetchesMetadataWhenAllFieldsEmpty(t *testing.T) {
	idx := &fakeIndexer{}
	fetcher := &fakeMetadataFetcher{tags: map[string][3]string{
		"http://example.com/a.mp3": {"Dreams", "Fleetwood Mac", "Rumours"},
	}}
	p := New(mpdclient.NewFakeClient(), store.NewMemoryStore(), idx, nil, nil, fetcher)

	if err := p.AddToLibrary("http://example.com/a.mp3", "", "", "", ""); err != nil {
		t.Fatalf("AddToLibrary: %v", err)
	}

	if len(idx.indexed) != 1 || idx.indexed[0].Artist != "Fleetwood Mac" || idx.indexed[0].Album != "Rumours" {
		t.Errorf("indexed = %v, want the fetched metadata used", idx.indexed)
	}
}

func TestAddToLibrarySkipsMetadataFetchWhenAnyFieldProvided(t *testing.T) {
	idx := &fakeIndexer{}
	fetcher := &fakeMetadataFetcher{tags: map[string][3]string{
		"http://example.com/a.mp3": {"Dreams", "Fleetwood Mac", "Rumours"},
	}}
	p := New(mpdclient.NewFakeClient(), store.NewMemoryStore(), idx, nil, nil, fetcher)

	if err := p.AddToLibrary("http://example.com/a.mp3", "My Own Title", "", "", ""); err != nil {
		t.Fatalf("AddToLibrary: %v", err)
	}

	if len(fetcher.calls) != 0 {
		t.Errorf("fetcher.calls = %v, want no metadata fetch when any field was provided", fetcher.calls)
	}
	if len(idx.indexed) != 1 || idx.indexed[0].Title != "My Own Title" || idx.indexed[0].Artist != "" {
		t.Errorf("indexed = %v, want the submitted title used as-is, artist left blank", idx.indexed)
	}
}

func TestAddToLibraryNormalizesURL(t *testing.T) {
	p, idx := newTestPlayerWithIndexer()

	if err := p.AddToLibrary("HTTP://Example.com:80/a.mp3", "A", "", "", ""); err != nil {
		t.Fatalf("AddToLibrary: %v", err)
	}

	if len(idx.indexed) != 1 || idx.indexed[0].URL != "http://example.com/a.mp3" {
		t.Errorf("indexed = %v, want the normalized URL", idx.indexed)
	}
}

func TestAddToLibraryErrorsWithNoIndexer(t *testing.T) {
	p := New(mpdclient.NewFakeClient(), store.NewMemoryStore(), nil, nil, nil, nil)

	if err := p.AddToLibrary("http://example.com/a.mp3", "A", "", "", ""); err == nil {
		t.Error("AddToLibrary with no indexer configured: want error, got nil")
	}
}

func TestRemoveFromLibrary(t *testing.T) {
	idx := &fakeIndexer{indexed: []indexer.Result{
		{URL: "http://example.com/a.mp3", Title: "A"},
	}}
	p := New(mpdclient.NewFakeClient(), store.NewMemoryStore(), idx, nil, nil, nil)

	if err := p.RemoveFromLibrary("http://example.com/a.mp3"); err != nil {
		t.Fatalf("RemoveFromLibrary: %v", err)
	}

	if len(idx.indexed) != 0 {
		t.Errorf("indexed = %v, want empty after removal", idx.indexed)
	}
}

func TestRemoveFromLibraryErrorsWithNoIndexer(t *testing.T) {
	p := New(mpdclient.NewFakeClient(), store.NewMemoryStore(), nil, nil, nil, nil)

	if err := p.RemoveFromLibrary("http://example.com/a.mp3"); err == nil {
		t.Error("RemoveFromLibrary with no indexer configured: want error, got nil")
	}
}

func TestPauseAndResume(t *testing.T) {
	p, mpd, _ := newTestPlayer()

	if err := p.PlayURL("http://example.com/a.mp3"); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}
	if err := p.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if got, want := mpd.State, "pause"; got != want {
		t.Errorf("mpd state after Pause = %q, want %q", got, want)
	}

	if err := p.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got, want := mpd.State, "play"; got != want {
		t.Errorf("mpd state after Resume = %q, want %q", got, want)
	}
}

func TestStatus(t *testing.T) {
	p, _, _ := newTestPlayer()

	if err := p.PlayURL("http://example.com/a.mp3"); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}

	status, err := p.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != "play" || status.Song != "http://example.com/a.mp3" {
		t.Errorf("Status = %+v, want State=play Song=http://example.com/a.mp3", status)
	}
}

func TestStatusUnresolvesBucketModeURL(t *testing.T) {
	mpd := mpdclient.NewFakeClient()
	resolver := &fakeResolver{
		playURI:      "http://127.0.0.1:8082/abc123.mp3",
		unresolveMap: map[string]string{"http://127.0.0.1:8082/abc123.mp3": "http://example.com/original.mp3"},
	}
	p := New(mpd, store.NewMemoryStore(), &fakeIndexer{}, resolver, nil, nil)
	if err := p.PlayURL("http://example.com/original.mp3"); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}

	status, err := p.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Song != "http://example.com/original.mp3" {
		t.Errorf("Status.Song = %q, want the original URL, not the resolved local file-server one", status.Song)
	}
}

func TestAddAndListFavorites(t *testing.T) {
	p, _, _ := newTestPlayer()

	if err := p.AddFavorite("http://example.com/fav.mp3", "My Favorite"); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}
	// Idempotent: adding the same URL again should not duplicate.
	if err := p.AddFavorite("http://example.com/fav.mp3", "My Favorite"); err != nil {
		t.Fatalf("AddFavorite (dup): %v", err)
	}

	favs, err := p.Favorites()
	if err != nil {
		t.Fatalf("Favorites: %v", err)
	}
	if len(favs) != 1 || favs[0].URL != "http://example.com/fav.mp3" {
		t.Errorf("Favorites = %v, want one entry for the favorited URL", favs)
	}

	if err := p.RemoveFavorite("http://example.com/fav.mp3"); err != nil {
		t.Fatalf("RemoveFavorite: %v", err)
	}
	favs, err = p.Favorites()
	if err != nil {
		t.Fatalf("Favorites: %v", err)
	}
	if len(favs) != 0 {
		t.Errorf("Favorites after remove = %v, want empty", favs)
	}
}

func TestAddFavoriteIndexesBestEffort(t *testing.T) {
	p, idx := newTestPlayerWithIndexer()

	if err := p.AddFavorite("http://example.com/fav.mp3", "My Favorite"); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}

	if got := waitForIndexed(t, idx); got.URL != "http://example.com/fav.mp3" {
		t.Errorf("indexed = %+v, want one entry for the favorited URL", got)
	}
}

func TestAddToPlaylistIndexesBestEffort(t *testing.T) {
	p, idx := newTestPlayerWithIndexer()

	if err := p.CreatePlaylist("chill"); err != nil {
		t.Fatalf("CreatePlaylist: %v", err)
	}
	if err := p.AddToPlaylist("chill", "http://example.com/b.mp3", "B"); err != nil {
		t.Fatalf("AddToPlaylist: %v", err)
	}

	if got := waitForIndexed(t, idx); got.URL != "http://example.com/b.mp3" {
		t.Errorf("indexed = %+v, want one entry for the playlisted URL", got)
	}
}

func TestPlaylistCreateAddList(t *testing.T) {
	p, _, _ := newTestPlayer()

	if err := p.CreatePlaylist("chill"); err != nil {
		t.Fatalf("CreatePlaylist: %v", err)
	}
	if err := p.AddToPlaylist("chill", "http://example.com/b.mp3", "B"); err != nil {
		t.Fatalf("AddToPlaylist: %v", err)
	}

	tracks, err := p.Playlist("chill")
	if err != nil {
		t.Fatalf("Playlist: %v", err)
	}
	if len(tracks) != 1 || tracks[0].URL != "http://example.com/b.mp3" {
		t.Errorf("Playlist(chill) = %v, want one entry for the added URL", tracks)
	}

	names, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if len(names) != 1 || names[0] != "chill" {
		t.Errorf("Playlists = %v, want [chill]", names)
	}
}

func TestAddToPlaylistUnknownFails(t *testing.T) {
	p, _, _ := newTestPlayer()

	if err := p.AddToPlaylist("does-not-exist", "http://example.com/x.mp3", ""); err == nil {
		t.Error("AddToPlaylist on unknown playlist: want error, got nil")
	}
}

func TestQueueReturnsMpdQueue(t *testing.T) {
	p, mpd, _ := newTestPlayer()
	_ = mpd.Add("http://example.com/a.mp3")
	_ = mpd.Add("http://example.com/b.mp3")

	queue, err := p.Queue()
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if len(queue) != 2 || queue[0].URL != "http://example.com/a.mp3" || queue[1].URL != "http://example.com/b.mp3" {
		t.Errorf("Queue = %v, want two entries for a.mp3 and b.mp3", queue)
	}
}

func TestQueueFillsInTitleWhenMPDHasNone(t *testing.T) {
	p, mpd, _ := newTestPlayer()
	// FakeClient.Add mirrors mpd's own untagged-file behavior: no Title.
	_ = mpd.Add("https://example.com/download/My_Cool-Track.mp3")

	queue, err := p.Queue()
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if len(queue) != 1 || queue[0].Title != "My Cool Track" {
		t.Errorf("Queue = %v, want Title derived from the URL", queue)
	}
}

func TestQueueLeavesRealTitleAlone(t *testing.T) {
	p, mpd, _ := newTestPlayer()
	_ = mpd.Add("https://example.com/a.mp3")
	mpd.QueueTracks[0].Title = "Dreams"

	queue, err := p.Queue()
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if queue[0].Title != "Dreams" {
		t.Errorf("Queue[0].Title = %q, want the real tag left untouched", queue[0].Title)
	}
}

// TestQueueEnrichesTitleAndArtistFromIndexOnceLookupCompletes covers the
// exact gap "queue should have the same logic as track for the title"
// pointed at: Status already overrode mpd's tag/URL-derived fallback with
// the search index's value (see enrichStatus), but Queue previously had no
// such override at all — just mpd's own tag or deriveTitleFromURL, which
// produces useless results for e.g. a bare YouTube watch link (its path
// is just "/watch").
func TestQueueEnrichesTitleAndArtistFromIndexOnceLookupCompletes(t *testing.T) {
	mpd := mpdclient.NewFakeClient()
	_ = mpd.Add("https://youtu.be/abc123XYZ90") // no Title tag, mirrors an untagged/YouTube-sourced file
	idx := &fakeIndexer{
		indexed: []indexer.Result{
			{URL: "https://youtu.be/abc123XYZ90", Title: "Real Video Title", Artist: "Real Channel"},
		},
		getCh: make(chan struct{}, 4),
	}
	p := New(mpd, store.NewMemoryStore(), idx, nil, nil, nil)

	// First call kicks off the lookup in the background and falls back to
	// the URL-derived title in the meantime — it must never block Queue on
	// the indexer's response time.
	queue, err := p.Queue()
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if len(queue) != 1 || queue[0].Title == "Real Video Title" {
		t.Errorf("first Queue() = %+v, want the URL-derived fallback (enrichment hasn't resolved yet)", queue)
	}

	waitForGet(t, idx)

	queue, err = p.Queue()
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if len(queue) != 1 {
		t.Fatalf("Queue() = %v, want one entry", queue)
	}
	if queue[0].Title != "Real Video Title" {
		t.Errorf("Queue[0].Title = %q, want the index's title to win over the URL-derived fallback", queue[0].Title)
	}
	if queue[0].Artist != "Real Channel" {
		t.Errorf("Queue[0].Artist = %q, want the index's artist", queue[0].Artist)
	}
}

func TestQueueEnrichmentCachesPerURLAcrossMultipleCalls(t *testing.T) {
	mpd := mpdclient.NewFakeClient()
	_ = mpd.Add("https://youtu.be/abc123XYZ90")
	idx := &fakeIndexer{
		indexed: []indexer.Result{{URL: "https://youtu.be/abc123XYZ90", Title: "Real Video Title"}},
		getCh:   make(chan struct{}, 4),
	}
	p := New(mpd, store.NewMemoryStore(), idx, nil, nil, nil)

	if _, err := p.Queue(); err != nil {
		t.Fatalf("Queue: %v", err)
	}
	waitForGet(t, idx)

	for i := 0; i < 5; i++ {
		if _, err := p.Queue(); err != nil {
			t.Fatalf("Queue: %v", err)
		}
	}

	idx.mu.Lock()
	got := idx.getCalls
	idx.mu.Unlock()
	if got != 1 {
		t.Errorf("Get calls = %d, want exactly 1 (one lookup per URL, not one per Queue call)", got)
	}
}

func TestQueueEnrichmentHandlesMultipleDistinctTracksIndependently(t *testing.T) {
	mpd := mpdclient.NewFakeClient()
	_ = mpd.Add("https://youtu.be/aaaaaaaaaaa")
	_ = mpd.Add("https://youtu.be/bbbbbbbbbbb")
	idx := &fakeIndexer{
		indexed: []indexer.Result{
			{URL: "https://youtu.be/aaaaaaaaaaa", Title: "First Video"},
			{URL: "https://youtu.be/bbbbbbbbbbb", Title: "Second Video"},
		},
		getCh: make(chan struct{}, 4),
	}
	p := New(mpd, store.NewMemoryStore(), idx, nil, nil, nil)

	if _, err := p.Queue(); err != nil {
		t.Fatalf("Queue: %v", err)
	}
	waitForGet(t, idx)
	waitForGet(t, idx)

	queue, err := p.Queue()
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if len(queue) != 2 {
		t.Fatalf("Queue() = %v, want 2 entries", queue)
	}
	if queue[0].Title != "First Video" {
		t.Errorf("queue[0].Title = %q, want %q", queue[0].Title, "First Video")
	}
	if queue[1].Title != "Second Video" {
		t.Errorf("queue[1].Title = %q, want %q", queue[1].Title, "Second Video")
	}
}

func TestStatusFillsInTitleWhenMPDHasNone(t *testing.T) {
	p, mpd, _ := newTestPlayer()
	mpd.Song = "https://example.com/download/My_Cool-Track.mp3"
	mpd.State = "play"

	status, err := p.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Title != "My Cool Track" {
		t.Errorf("Status.Title = %q, want derived from the URL", status.Title)
	}
}

// waitForGet blocks until idx.getCh receives a signal — proof that a
// background enrichStatus lookup for the current song actually ran, since
// there's no other externally-visible side effect to synchronize on.
func waitForGet(t *testing.T, idx *fakeIndexer) {
	t.Helper()
	select {
	case <-idx.getCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the async status-enrichment Get call")
	}
}

func TestStatusEnrichesFromIndexOnceLookupCompletes(t *testing.T) {
	mpd := mpdclient.NewFakeClient()
	mpd.Song = "http://example.com/a.mp3"
	mpd.State = "play"
	mpd.Artist = "Original Artist"
	mpd.Album = "Original Album"
	mpd.Title = "Original Title"
	idx := &fakeIndexer{
		indexed: []indexer.Result{
			{URL: "http://example.com/a.mp3", Title: "Edited Title", Artist: "Edited Artist", Album: "Edited Album"},
		},
		getCh: make(chan struct{}, 4),
	}
	p := New(mpd, store.NewMemoryStore(), idx, nil, nil, nil)

	// The first call for a new song kicks off the lookup in the background
	// and returns immediately with mpd's own (unenriched) tags — it must
	// never block on the indexer's response.
	status, err := p.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Title != "Original Title" || status.Artist != "Original Artist" || status.Album != "Original Album" {
		t.Errorf("first Status() = %+v, want mpd's own tags (enrichment hasn't resolved yet)", status)
	}

	waitForGet(t, idx)

	status, err = p.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Title != "Edited Title" {
		t.Errorf("Status.Title = %q, want the index's edited title to win over mpd's own tag", status.Title)
	}
	if status.Artist != "Edited Artist" {
		t.Errorf("Status.Artist = %q, want the index's edited artist to win over mpd's own tag", status.Artist)
	}
	if status.Album != "Edited Album" {
		t.Errorf("Status.Album = %q, want the index's edited album to win over mpd's own tag", status.Album)
	}
}

func TestStatusEnrichmentDoesNotBlankFieldsTheIndexLeavesEmpty(t *testing.T) {
	mpd := mpdclient.NewFakeClient()
	mpd.Song = "http://example.com/a.mp3"
	mpd.State = "play"
	mpd.Artist = "mpd's own Artist"
	mpd.Album = "mpd's own Album"
	idx := &fakeIndexer{
		// Only Album was ever edited — Artist was left blank in the index,
		// which must not blank out mpd's own Artist tag.
		indexed: []indexer.Result{
			{URL: "http://example.com/a.mp3", Album: "Edited Album"},
		},
		getCh: make(chan struct{}, 4),
	}
	p := New(mpd, store.NewMemoryStore(), idx, nil, nil, nil)

	if _, err := p.Status(); err != nil {
		t.Fatalf("Status: %v", err)
	}
	waitForGet(t, idx)

	status, err := p.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Artist != "mpd's own Artist" {
		t.Errorf("Status.Artist = %q, want mpd's own tag preserved (index left this field blank)", status.Artist)
	}
	if status.Album != "Edited Album" {
		t.Errorf("Status.Album = %q, want the index's edited album", status.Album)
	}
}

func TestStatusEnrichmentLooksUpOnceAndCachesForSameSong(t *testing.T) {
	mpd := mpdclient.NewFakeClient()
	mpd.Song = "http://example.com/a.mp3"
	mpd.State = "play"
	idx := &fakeIndexer{getCh: make(chan struct{}, 4)}
	p := New(mpd, store.NewMemoryStore(), idx, nil, nil, nil)

	if _, err := p.Status(); err != nil {
		t.Fatalf("Status: %v", err)
	}
	waitForGet(t, idx)

	// Several more calls for the *same* song must not trigger another
	// lookup — that's the whole point of caching per-song rather than
	// looking up on every tick.
	for i := 0; i < 5; i++ {
		if _, err := p.Status(); err != nil {
			t.Fatalf("Status: %v", err)
		}
	}

	idx.mu.Lock()
	got := idx.getCalls
	idx.mu.Unlock()
	if got != 1 {
		t.Errorf("Get calls = %d, want exactly 1 (one lookup per song, not one per Status call)", got)
	}
}

func TestStatusEnrichmentRefetchesWhenSongChanges(t *testing.T) {
	mpd := mpdclient.NewFakeClient()
	mpd.Song = "http://example.com/a.mp3"
	mpd.State = "play"
	idx := &fakeIndexer{getCh: make(chan struct{}, 4)}
	p := New(mpd, store.NewMemoryStore(), idx, nil, nil, nil)

	if _, err := p.Status(); err != nil {
		t.Fatalf("Status: %v", err)
	}
	waitForGet(t, idx)

	mpd.Song = "http://example.com/b.mp3"
	if _, err := p.Status(); err != nil {
		t.Fatalf("Status: %v", err)
	}
	waitForGet(t, idx)

	idx.mu.Lock()
	got := idx.getCalls
	idx.mu.Unlock()
	if got != 2 {
		t.Errorf("Get calls = %d, want exactly 2 (one per distinct song)", got)
	}
}

// TestAddToLibraryRefreshesStatusEnrichmentForTheCurrentlyPlayingTrack
// covers the exact bug report that motivated enrichStatus's invalidation
// path: editing a track's metadata while that same track is already
// playing (song URL unchanged throughout) must show up without needing to
// change tracks first.
func TestAddToLibraryRefreshesStatusEnrichmentForTheCurrentlyPlayingTrack(t *testing.T) {
	mpd := mpdclient.NewFakeClient()
	mpd.Song = "http://example.com/a.mp3"
	mpd.State = "play"
	mpd.Artist = "File Artist"
	mpd.Album = "File Album"
	idx := &fakeIndexer{getCh: make(chan struct{}, 4)}
	p := New(mpd, store.NewMemoryStore(), idx, nil, nil, nil)

	// First Status() call establishes the cache for the currently playing
	// song — mirrors the real daemon's 1s status ticker already having
	// polled at least once before the user gets around to editing anything.
	if _, err := p.Status(); err != nil {
		t.Fatalf("Status: %v", err)
	}
	waitForGet(t, idx)

	if err := p.AddToLibrary("http://example.com/a.mp3", "", "Edited Artist", "Edited Album", ""); err != nil {
		t.Fatalf("AddToLibrary: %v", err)
	}
	waitForGet(t, idx) // the invalidation-triggered re-lookup

	status, err := p.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Artist != "Edited Artist" || status.Album != "Edited Album" {
		t.Errorf("Status = %+v, want the just-edited artist/album — editing the currently playing track "+
			"must show up immediately, not only after the track changes", status)
	}
}

func TestQueueUnresolvesBucketModeURLs(t *testing.T) {
	mpd := mpdclient.NewFakeClient()
	// Simulates bucket mode: mpd's queue only ever sees the resolved local
	// file-server URL, never the original.
	resolver := &fakeResolver{
		playURI:      "http://127.0.0.1:8082/abc123.mp3",
		unresolveMap: map[string]string{"http://127.0.0.1:8082/abc123.mp3": "http://example.com/original.mp3"},
	}
	p := New(mpd, store.NewMemoryStore(), &fakeIndexer{}, resolver, nil, nil)
	if err := p.AddToQueue("http://example.com/original.mp3"); err != nil {
		t.Fatalf("AddToQueue: %v", err)
	}

	queue, err := p.Queue()
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if len(queue) != 1 || queue[0].URL != "http://example.com/original.mp3" {
		t.Errorf("Queue = %v, want the original URL, not the resolved local file-server one", queue)
	}
}

func TestAddToQueueDoesNotPlayOrRecordHistory(t *testing.T) {
	p, mpd, st := newTestPlayer()

	if err := p.AddToQueue("http://example.com/a.mp3"); err != nil {
		t.Fatalf("AddToQueue: %v", err)
	}

	if mpd.State == "play" {
		t.Errorf("mpd state = %q, want AddToQueue not to start playback", mpd.State)
	}
	if len(mpd.Playlist) != 1 || mpd.Playlist[0] != "http://example.com/a.mp3" {
		t.Errorf("mpd playlist = %v, want [http://example.com/a.mp3]", mpd.Playlist)
	}

	hist, err := st.History(0)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 0 {
		t.Errorf("history = %v, want empty (AddToQueue is not a play event)", hist)
	}
}

func TestAddToQueueIndexesBestEffort(t *testing.T) {
	p, idx := newTestPlayerWithIndexer()

	if err := p.AddToQueue("http://example.com/a.mp3"); err != nil {
		t.Fatalf("AddToQueue: %v", err)
	}

	if got := waitForIndexed(t, idx); got.URL != "http://example.com/a.mp3" {
		t.Errorf("indexed = %+v, want one entry for the queued URL", got)
	}
}

func TestPlayURLRejectsUnreachableURLWithoutTouchingMPD(t *testing.T) {
	mpd := mpdclient.NewFakeClient()
	resolver := &fakeResolver{err: errors.New("connection refused")}
	p := New(mpd, store.NewMemoryStore(), &fakeIndexer{}, resolver, nil, nil)

	err := p.PlayURL("http://example.com/dead-stream.mp3")
	if err == nil {
		t.Fatal("PlayURL: want an error for an unreachable URL, got nil")
	}
	if len(resolver.resolved) != 1 || resolver.resolved[0] != "http://example.com/dead-stream.mp3" {
		t.Errorf("resolver.resolved = %v, want the URL to have been resolved", resolver.resolved)
	}
	if len(mpd.Playlist) != 0 {
		t.Errorf("mpd playlist = %v, want empty — a rejected URL should never reach mpd.Add", mpd.Playlist)
	}
	if mpd.State == "play" {
		t.Error("mpd state = play, want unchanged — a rejected URL should never start playback")
	}
}

func TestPlayURLUsesResolvedURIButRecordsOriginalURL(t *testing.T) {
	mpd := mpdclient.NewFakeClient()
	st := store.NewMemoryStore()
	resolver := &fakeResolver{playURI: "/var/lib/pi-streamer/bucket/abc123.mp3"}
	p := New(mpd, st, &fakeIndexer{}, resolver, nil, nil)

	if err := p.PlayURL("http://example.com/a.mp3"); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}
	if len(mpd.Playlist) != 1 || mpd.Playlist[0] != "/var/lib/pi-streamer/bucket/abc123.mp3" {
		t.Errorf("mpd playlist = %v, want the resolved local path", mpd.Playlist)
	}
	hist, err := st.History(0)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 1 || hist[0].URL != "http://example.com/a.mp3" {
		t.Errorf("history = %v, want the original URL, not the resolved local path", hist)
	}
}

func TestPlayURLSkipsResolveWhenNoResolverConfigured(t *testing.T) {
	p, mpd, _ := newTestPlayer() // newTestPlayer passes a nil resolver

	if err := p.PlayURL("http://example.com/a.mp3"); err != nil {
		t.Fatalf("PlayURL with no resolver configured: %v, want nil", err)
	}
	if len(mpd.Playlist) != 1 {
		t.Errorf("mpd playlist = %v, want one entry", mpd.Playlist)
	}
}

func TestAddToQueueRejectsUnreachableURLWithoutTouchingMPD(t *testing.T) {
	mpd := mpdclient.NewFakeClient()
	resolver := &fakeResolver{err: errors.New("404 not found")}
	p := New(mpd, store.NewMemoryStore(), &fakeIndexer{}, resolver, nil, nil)

	if err := p.AddToQueue("http://example.com/dead-stream.mp3"); err == nil {
		t.Fatal("AddToQueue: want an error for an unreachable URL, got nil")
	}
	if len(mpd.Playlist) != 0 {
		t.Errorf("mpd playlist = %v, want empty — a rejected URL should never reach mpd.Add", mpd.Playlist)
	}
}

func TestAddFavoriteArchivesBestEffort(t *testing.T) {
	mpd := mpdclient.NewFakeClient()
	archiver := &fakeArchiver{}
	p := New(mpd, store.NewMemoryStore(), &fakeIndexer{}, nil, archiver, nil)

	if err := p.AddFavorite("http://example.com/a.mp3", "A"); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}
	if len(archiver.archived) != 1 || archiver.archived[0] != "http://example.com/a.mp3" {
		t.Errorf("archived = %v, want one entry for the favorited URL", archiver.archived)
	}
}

func TestAddFavoriteSkipsArchivingWhenNoArchiverConfigured(t *testing.T) {
	p, _, _ := newTestPlayer() // newTestPlayer passes a nil archiver

	if err := p.AddFavorite("http://example.com/a.mp3", "A"); err != nil {
		t.Fatalf("AddFavorite with no archiver configured: %v, want nil", err)
	}
}

func TestRemoveFavoriteForgetsArchivedCopy(t *testing.T) {
	mpd := mpdclient.NewFakeClient()
	archiver := &fakeArchiver{}
	p := New(mpd, store.NewMemoryStore(), &fakeIndexer{}, nil, archiver, nil)
	if err := p.AddFavorite("http://example.com/a.mp3", "A"); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}

	if err := p.RemoveFavorite("http://example.com/a.mp3"); err != nil {
		t.Fatalf("RemoveFavorite: %v", err)
	}
	if len(archiver.forgot) != 1 || archiver.forgot[0] != "http://example.com/a.mp3" {
		t.Errorf("forgot = %v, want one entry for the unfavorited URL", archiver.forgot)
	}
}

func TestRemoveFavoriteSkipsForgetWhenNoArchiverConfigured(t *testing.T) {
	p, _, _ := newTestPlayer() // newTestPlayer passes a nil archiver
	if err := p.AddFavorite("http://example.com/a.mp3", "A"); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}

	if err := p.RemoveFavorite("http://example.com/a.mp3"); err != nil {
		t.Fatalf("RemoveFavorite with no archiver configured: %v, want nil", err)
	}
}

func TestRemoveFromQueue(t *testing.T) {
	p, mpd, _ := newTestPlayer()
	_ = mpd.Add("http://example.com/a.mp3")
	_ = mpd.Add("http://example.com/b.mp3")

	if err := p.RemoveFromQueue(1); err != nil {
		t.Fatalf("RemoveFromQueue: %v", err)
	}

	queue, err := p.Queue()
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if len(queue) != 1 || queue[0].URL != "http://example.com/b.mp3" {
		t.Errorf("Queue after remove = %v, want one entry for b.mp3", queue)
	}
}

func TestMoveInQueue(t *testing.T) {
	p, mpd, _ := newTestPlayer()
	_ = mpd.Add("http://example.com/a.mp3")
	_ = mpd.Add("http://example.com/b.mp3")

	if err := p.MoveInQueue(1, 1); err != nil {
		t.Fatalf("MoveInQueue: %v", err)
	}

	queue, err := p.Queue()
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if len(queue) != 2 || queue[0].URL != "http://example.com/b.mp3" || queue[1].URL != "http://example.com/a.mp3" {
		t.Errorf("Queue after move = %v, want [b.mp3, a.mp3]", queue)
	}
}

func TestPlayQueueItem(t *testing.T) {
	p, mpd, _ := newTestPlayer()
	_ = mpd.Add("http://example.com/a.mp3")
	_ = mpd.Add("http://example.com/b.mp3")

	if err := p.PlayQueueItem(2); err != nil {
		t.Fatalf("PlayQueueItem: %v", err)
	}
	if mpd.State != "play" || mpd.Song != "http://example.com/b.mp3" {
		t.Errorf("mpd State/Song = %q/%q, want play/b.mp3", mpd.State, mpd.Song)
	}
}

func TestClearQueue(t *testing.T) {
	p, mpd, _ := newTestPlayer()
	_ = mpd.Add("http://example.com/a.mp3")

	if err := p.ClearQueue(); err != nil {
		t.Fatalf("ClearQueue: %v", err)
	}

	queue, err := p.Queue()
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if len(queue) != 0 {
		t.Errorf("Queue after clear = %v, want empty", queue)
	}
}

func TestPlayURLRejectsInvalidURL(t *testing.T) {
	p, mpd, _ := newTestPlayer()

	if err := p.PlayURL("not-a-url"); err == nil {
		t.Fatal("PlayURL(\"not-a-url\"): want error, got nil")
	}
	if len(mpd.Playlist) != 0 {
		t.Errorf("mpd playlist = %v, want empty — an invalid URL should never reach mpd.Add", mpd.Playlist)
	}
}

func TestPlayURLNormalizesCaseVariantsToTheSameHistoryEntry(t *testing.T) {
	p, _, st := newTestPlayer()

	if err := p.PlayURL("HTTP://Example.com/song.mp3"); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}
	if err := p.PlayURL("http://example.com/song.mp3"); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}

	hist, err := st.History(0)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("History len = %d, want 2 (both plays recorded, each history entry is its own event)", len(hist))
	}
	if hist[0].URL != hist[1].URL {
		t.Errorf("History URLs = %q, %q, want both normalized to the same canonical URL", hist[0].URL, hist[1].URL)
	}
}

// TestPlayURLNormalizesYouTubeURLShapesToTheSameHistoryEntry covers the
// same "different formats, same video, same identity" request as the
// urlnorm-level tests, but end to end through PlayURL — proving the whole
// point of collapsing at normalizeURL's level: everything downstream
// (history here; the bucket cache/album art/search index/enrichment cache
// elsewhere) automatically shares one identity for the same video however
// it was pasted in, with no extra plumbing needed at any of those call
// sites.
func TestPlayURLNormalizesYouTubeURLShapesToTheSameHistoryEntry(t *testing.T) {
	p, _, st := newTestPlayer()

	if err := p.PlayURL("https://youtu.be/abc123XYZ90?si=someTrackingToken12"); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}
	if err := p.PlayURL("https://www.youtube.com/watch?v=abc123XYZ90&t=42s"); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}

	hist, err := st.History(0)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("History len = %d, want 2 (both plays recorded, each history entry is its own event)", len(hist))
	}
	want := "https://www.youtube.com/watch?v=abc123XYZ90"
	if hist[0].URL != want || hist[1].URL != want {
		t.Errorf("History URLs = %q, %q, want both collapsed to %q", hist[0].URL, hist[1].URL, want)
	}
}

func TestAddFavoriteNormalizesCaseVariantsToOneEntry(t *testing.T) {
	p, idx := newTestPlayerWithIndexer()

	if err := p.AddFavorite("HTTP://Example.com/song.mp3", "Song"); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}
	if err := p.AddFavorite("http://example.com/song.mp3", "Song"); err != nil {
		t.Fatalf("AddFavorite (case variant): %v", err)
	}

	// Both AddFavorite calls kick off their own async index() call (see
	// index's doc comment) — wait for both before asserting on the
	// indexer's state.
	waitForIndexed(t, idx)
	waitForIndexed(t, idx)

	favs, err := p.Favorites()
	if err != nil {
		t.Fatalf("Favorites: %v", err)
	}
	if len(favs) != 1 {
		t.Errorf("Favorites = %v, want one entry — differently-cased URLs should collapse to the same favorite", favs)
	}
	if len(idx.indexed) != 1 {
		t.Errorf("indexed = %v, want one entry — differently-cased URLs should collapse to the same index row", idx.indexed)
	}
}

func TestIndexReusesExistingFullyTaggedEntryWithoutRefetchingMetadata(t *testing.T) {
	idx := &fakeIndexer{
		indexed: []indexer.Result{
			{URL: "http://example.com/a.mp3", Title: "Dreams", Artist: "Fleetwood Mac", Album: "Rumours"},
		},
		getCh: make(chan struct{}, 1),
	}
	fetcher := &fakeMetadataFetcher{tags: map[string][3]string{
		"http://example.com/a.mp3": {"Dreams", "Fleetwood Mac", "Rumours"},
	}}
	p := New(mpdclient.NewFakeClient(), store.NewMemoryStore(), idx, nil, nil, fetcher)

	if err := p.AddToQueue("http://example.com/a.mp3"); err != nil {
		t.Fatalf("AddToQueue: %v", err)
	}

	// An already fully-tagged URL makes no further calls at all (that's
	// the whole point of this test), so there's no "it happened" signal
	// to wait on directly — instead, wait for the Get call indexAsync
	// always makes first, which proves the goroutine actually ran and
	// reached its "return early" branch before asserting nothing else did.
	select {
	case <-idx.getCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the async Get call")
	}

	if len(idx.indexed) != 1 || idx.indexed[0].Title != "Dreams" {
		t.Errorf("indexed = %v, want the existing entry left untouched", idx.indexed)
	}
	if len(fetcher.calls) != 0 {
		t.Errorf("fetcher.calls = %v, want no metadata fetch for an already fully-tagged URL", fetcher.calls)
	}
}

func TestIndexEnrichesANewURLWithFetchedMetadata(t *testing.T) {
	idx := &fakeIndexer{indexedCh: make(chan indexer.Result, 4)}
	fetcher := &fakeMetadataFetcher{tags: map[string][3]string{
		"http://example.com/a.mp3": {"Dreams", "Fleetwood Mac", "Rumours"},
	}}
	p := New(mpdclient.NewFakeClient(), store.NewMemoryStore(), idx, nil, nil, fetcher)

	if err := p.AddToQueue("http://example.com/a.mp3"); err != nil {
		t.Fatalf("AddToQueue: %v", err)
	}

	// First IndexURL call is the synchronous, thin add-time entry.
	select {
	case first := <-idx.indexedCh:
		if first.Artist != "" {
			t.Errorf("first IndexURL call = %+v, want no artist yet (metadata fetch hasn't run)", first)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the initial IndexURL call")
	}

	// Second call is the async enrichment, once the fake fetcher "found" tags.
	select {
	case second := <-idx.indexedCh:
		if second.Artist != "Fleetwood Mac" || second.Album != "Rumours" {
			t.Errorf("enriched IndexURL call = %+v, want Artist=Fleetwood Mac Album=Rumours", second)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the async metadata-enrichment IndexURL call")
	}
}

func TestIndexSkipsEnrichmentWhenMetadataFetcherFindsNothing(t *testing.T) {
	idx := &fakeIndexer{indexedCh: make(chan indexer.Result, 4)}
	fetcher := &fakeMetadataFetcher{tags: map[string][3]string{}} // no entry for any URL -> ok=false
	p := New(mpdclient.NewFakeClient(), store.NewMemoryStore(), idx, nil, nil, fetcher)

	if err := p.AddToQueue("http://example.com/radio-stream.mp3"); err != nil {
		t.Fatalf("AddToQueue: %v", err)
	}

	select {
	case first := <-idx.indexedCh:
		if first.Artist != "" {
			t.Errorf("indexed = %+v, want empty artist (no tags found)", first)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the initial IndexURL call")
	}

	// Give the enrichment goroutine a moment to (not) run, then confirm no
	// second IndexURL call happened.
	select {
	case second := <-idx.indexedCh:
		t.Errorf("unexpected second IndexURL call = %+v, want none when no metadata was found", second)
	case <-time.After(200 * time.Millisecond):
	}
}
