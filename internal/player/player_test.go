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
	idx := &fakeIndexer{}
	return New(mpdclient.NewFakeClient(), store.NewMemoryStore(), idx, nil, nil, nil), idx
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

func TestPlayURLIndexesBestEffort(t *testing.T) {
	p, idx := newTestPlayerWithIndexer()

	if err := p.PlayURL("http://example.com/stream.mp3"); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}

	if len(idx.indexed) != 1 || idx.indexed[0].URL != "http://example.com/stream.mp3" {
		t.Errorf("indexed = %v, want one entry for the played URL", idx.indexed)
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

	if len(idx.indexed) != 1 || idx.indexed[0].URL != "http://example.com/fav.mp3" {
		t.Errorf("indexed = %v, want one entry for the favorited URL", idx.indexed)
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

	if len(idx.indexed) != 1 || idx.indexed[0].URL != "http://example.com/b.mp3" {
		t.Errorf("indexed = %v, want one entry for the playlisted URL", idx.indexed)
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

	if len(idx.indexed) != 1 || idx.indexed[0].URL != "http://example.com/a.mp3" {
		t.Errorf("indexed = %v, want one entry for the queued URL", idx.indexed)
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

func TestAddFavoriteNormalizesCaseVariantsToOneEntry(t *testing.T) {
	p, idx := newTestPlayerWithIndexer()

	if err := p.AddFavorite("HTTP://Example.com/song.mp3", "Song"); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}
	if err := p.AddFavorite("http://example.com/song.mp3", "Song"); err != nil {
		t.Fatalf("AddFavorite (case variant): %v", err)
	}

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
	idx := &fakeIndexer{indexed: []indexer.Result{
		{URL: "http://example.com/a.mp3", Title: "Dreams", Artist: "Fleetwood Mac", Album: "Rumours"},
	}}
	fetcher := &fakeMetadataFetcher{tags: map[string][3]string{
		"http://example.com/a.mp3": {"Dreams", "Fleetwood Mac", "Rumours"},
	}}
	p := New(mpdclient.NewFakeClient(), store.NewMemoryStore(), idx, nil, nil, fetcher)

	if err := p.AddToQueue("http://example.com/a.mp3"); err != nil {
		t.Fatalf("AddToQueue: %v", err)
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
