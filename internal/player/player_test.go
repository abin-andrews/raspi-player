package player

import (
	"errors"
	"testing"
	"time"

	"pi-streamer/internal/indexer"
	"pi-streamer/internal/mpdclient"
	"pi-streamer/internal/store"
)

// fakeIndexer is an in-memory Indexer implementation for tests.
type fakeIndexer struct {
	indexed []indexer.Result
	results []indexer.Result
	err     error
}

func (f *fakeIndexer) IndexURL(url, title, tags string) error {
	if f.err != nil {
		return f.err
	}
	f.indexed = append(f.indexed, indexer.Result{URL: url, Title: title, Tags: tags})
	return nil
}

func (f *fakeIndexer) Search(query string, limit int) ([]indexer.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.results, nil
}

func newTestPlayer() (*Player, *mpdclient.FakeClient, store.Store) {
	mpd := mpdclient.NewFakeClient()
	st := store.NewMemoryStore()
	return New(mpd, st, &fakeIndexer{}), mpd, st
}

func newTestPlayerWithIndexer() (*Player, *fakeIndexer) {
	idx := &fakeIndexer{}
	return New(mpdclient.NewFakeClient(), store.NewMemoryStore(), idx), idx
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
	p := New(mpdclient.NewFakeClient(), store.NewMemoryStore(), idx)

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
	p := New(mpdclient.NewFakeClient(), store.NewMemoryStore(), nil)

	if _, err := p.Search("x", 10); err == nil {
		t.Error("Search with no indexer configured: want error, got nil")
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
