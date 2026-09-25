package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pi-streamer/internal/indexer"
	"pi-streamer/internal/mpdclient"
	"pi-streamer/internal/store"
)

// fakePlayer is a minimal in-memory Player implementation for handler tests.
type fakePlayer struct {
	played    []string
	paused    bool
	status    mpdclient.Status
	favorites []store.Track
	playlists map[string][]store.Track
	history   []store.Track
	forceErr  error

	seekCalls []float64

	nextCalls         int
	previousCalls     int
	volumeCalls       []int
	seekRelativeCalls []float64

	art    []byte
	artErr error

	searchResults []indexer.Result
	searchErr     error

	queue              []mpdclient.QueueTrack
	addToQueueCalls    []string
	removeFromQueueIDs []int
	moveInQueueCalls   [][2]int
	playQueueItemIDs   []int
	clearQueueCalls    int
}

func newFakePlayer() *fakePlayer {
	return &fakePlayer{playlists: map[string][]store.Track{}}
}

func (f *fakePlayer) PlayURL(url string) error {
	if f.forceErr != nil {
		return f.forceErr
	}
	f.played = append(f.played, url)
	f.paused = false
	f.status = mpdclient.Status{State: "play", Song: url}
	return nil
}

func (f *fakePlayer) Pause() error {
	f.paused = true
	f.status.State = "pause"
	return nil
}

func (f *fakePlayer) Resume() error {
	f.paused = false
	f.status.State = "play"
	return nil
}

func (f *fakePlayer) Status() (mpdclient.Status, error) { return f.status, nil }

func (f *fakePlayer) AddFavorite(url, title string) error {
	for _, t := range f.favorites {
		if t.URL == url {
			return nil
		}
	}
	f.favorites = append(f.favorites, store.Track{URL: url, Title: title})
	return nil
}

func (f *fakePlayer) RemoveFavorite(url string) error {
	out := f.favorites[:0]
	for _, t := range f.favorites {
		if t.URL != url {
			out = append(out, t)
		}
	}
	f.favorites = out
	return nil
}

func (f *fakePlayer) Favorites() ([]store.Track, error) { return f.favorites, nil }

func (f *fakePlayer) CreatePlaylist(name string) error {
	if _, ok := f.playlists[name]; ok {
		return store.ErrPlaylistExists
	}
	f.playlists[name] = nil
	return nil
}

func (f *fakePlayer) AddToPlaylist(name, url, title string) error {
	if _, ok := f.playlists[name]; !ok {
		return store.ErrPlaylistNotFound
	}
	f.playlists[name] = append(f.playlists[name], store.Track{URL: url, Title: title})
	return nil
}

func (f *fakePlayer) Playlist(name string) ([]store.Track, error) {
	tracks, ok := f.playlists[name]
	if !ok {
		return nil, store.ErrPlaylistNotFound
	}
	return tracks, nil
}

func (f *fakePlayer) Playlists() ([]string, error) {
	names := make([]string, 0, len(f.playlists))
	for name := range f.playlists {
		names = append(names, name)
	}
	return names, nil
}

func (f *fakePlayer) History(limit int) ([]store.Track, error) { return f.history, nil }

func (f *fakePlayer) Seek(seconds float64) error {
	f.seekCalls = append(f.seekCalls, seconds)
	return nil
}

func (f *fakePlayer) Next() error {
	f.nextCalls++
	return nil
}

func (f *fakePlayer) Previous() error {
	f.previousCalls++
	return nil
}

func (f *fakePlayer) SetVolume(volume int) error {
	f.volumeCalls = append(f.volumeCalls, volume)
	return nil
}

func (f *fakePlayer) SeekRelative(seconds float64) error {
	f.seekRelativeCalls = append(f.seekRelativeCalls, seconds)
	return nil
}

func (f *fakePlayer) AlbumArt(url string) ([]byte, error) { return f.art, f.artErr }

func (f *fakePlayer) Search(query string, limit int) ([]indexer.Result, error) {
	return f.searchResults, f.searchErr
}

func (f *fakePlayer) Queue() ([]mpdclient.QueueTrack, error) {
	if f.forceErr != nil {
		return nil, f.forceErr
	}
	return f.queue, nil
}

func (f *fakePlayer) AddToQueue(url string) error {
	if f.forceErr != nil {
		return f.forceErr
	}
	f.addToQueueCalls = append(f.addToQueueCalls, url)
	f.queue = append(f.queue, mpdclient.QueueTrack{ID: len(f.queue) + 1, Position: len(f.queue), URL: url})
	return nil
}

func (f *fakePlayer) RemoveFromQueue(id int) error {
	if f.forceErr != nil {
		return f.forceErr
	}
	f.removeFromQueueIDs = append(f.removeFromQueueIDs, id)
	out := f.queue[:0]
	for _, t := range f.queue {
		if t.ID != id {
			out = append(out, t)
		}
	}
	f.queue = out
	return nil
}

func (f *fakePlayer) MoveInQueue(id, position int) error {
	if f.forceErr != nil {
		return f.forceErr
	}
	f.moveInQueueCalls = append(f.moveInQueueCalls, [2]int{id, position})
	return nil
}

func (f *fakePlayer) PlayQueueItem(id int) error {
	if f.forceErr != nil {
		return f.forceErr
	}
	f.playQueueItemIDs = append(f.playQueueItemIDs, id)
	return nil
}

func (f *fakePlayer) ClearQueue() error {
	if f.forceErr != nil {
		return f.forceErr
	}
	f.clearQueueCalls++
	f.queue = nil
	return nil
}

func doRequest(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandlePlay(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/play", `{"url":"http://example.com/a.mp3"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(p.played) != 1 || p.played[0] != "http://example.com/a.mp3" {
		t.Errorf("played = %v, want [http://example.com/a.mp3]", p.played)
	}
}

func TestHandlePlayBadBody(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/play", `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandlePauseResume(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/pause", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("pause status = %d, want 200", rec.Code)
	}
	if !p.paused {
		t.Error("expected paused = true after /api/pause")
	}

	rec = doRequest(t, h, "POST", "/api/resume", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("resume status = %d, want 200", rec.Code)
	}
	if p.paused {
		t.Error("expected paused = false after /api/resume")
	}
}

func TestHandleStatus(t *testing.T) {
	p := newFakePlayer()
	p.status = mpdclient.Status{State: "play", Song: "http://example.com/a.mp3"}
	h := NewRouter(p)

	rec := doRequest(t, h, "GET", "/api/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got mpdclient.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != p.status {
		t.Errorf("got %+v, want %+v", got, p.status)
	}
}

func TestHandleFavorites(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/favorites", `{"url":"http://example.com/fav.mp3","title":"Fav"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add favorite status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	rec = doRequest(t, h, "GET", "/api/favorites", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list favorites status = %d, want 200", rec.Code)
	}
	var favs []store.Track
	if err := json.Unmarshal(rec.Body.Bytes(), &favs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(favs) != 1 || favs[0].URL != "http://example.com/fav.mp3" {
		t.Errorf("favorites = %v, want one entry", favs)
	}

	rec = doRequest(t, h, "DELETE", "/api/favorites?url=http://example.com/fav.mp3", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remove favorite status = %d, want 204", rec.Code)
	}
	if len(p.favorites) != 0 {
		t.Errorf("favorites after delete = %v, want empty", p.favorites)
	}
}

func TestHandlePlaylists(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/playlists", `{"name":"chill"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create playlist status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	rec = doRequest(t, h, "POST", "/api/playlists/chill", `{"url":"http://example.com/b.mp3","title":"B"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add to playlist status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	rec = doRequest(t, h, "GET", "/api/playlists/chill", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get playlist status = %d, want 200", rec.Code)
	}
	var tracks []store.Track
	if err := json.Unmarshal(rec.Body.Bytes(), &tracks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(tracks) != 1 || tracks[0].URL != "http://example.com/b.mp3" {
		t.Errorf("playlist tracks = %v, want one entry", tracks)
	}

	rec = doRequest(t, h, "GET", "/api/playlists", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list playlists status = %d, want 200", rec.Code)
	}
	var names []string
	if err := json.Unmarshal(rec.Body.Bytes(), &names); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(names) != 1 || names[0] != "chill" {
		t.Errorf("playlists = %v, want [chill]", names)
	}
}

func TestHandleAddToUnknownPlaylist(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/playlists/nope", `{"url":"http://example.com/x.mp3"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleHistory(t *testing.T) {
	p := newFakePlayer()
	p.history = []store.Track{{URL: "http://example.com/a.mp3"}}
	h := NewRouter(p)

	rec := doRequest(t, h, "GET", "/api/history", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got []store.Track
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].URL != "http://example.com/a.mp3" {
		t.Errorf("history = %v, want one entry", got)
	}
}

func TestHandleSeek(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/seek", `{"seconds":42.5}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(p.seekCalls) != 1 || p.seekCalls[0] != 42.5 {
		t.Errorf("seekCalls = %v, want [42.5]", p.seekCalls)
	}
}

func TestHandleNext(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/next", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if p.nextCalls != 1 {
		t.Errorf("nextCalls = %d, want 1", p.nextCalls)
	}
}

func TestHandlePrevious(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/previous", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if p.previousCalls != 1 {
		t.Errorf("previousCalls = %d, want 1", p.previousCalls)
	}
}

func TestHandleVolume(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/volume", `{"volume":75}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(p.volumeCalls) != 1 || p.volumeCalls[0] != 75 {
		t.Errorf("volumeCalls = %v, want [75]", p.volumeCalls)
	}
}

func TestHandleVolumeBadBody(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/volume", `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleSeekRelative(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/seek/relative", `{"seconds":-10.5}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(p.seekRelativeCalls) != 1 || p.seekRelativeCalls[0] != -10.5 {
		t.Errorf("seekRelativeCalls = %v, want [-10.5]", p.seekRelativeCalls)
	}
}

func TestHandleSeekRelativeBadBody(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/seek/relative", `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleAlbumArtFound(t *testing.T) {
	p := newFakePlayer()
	p.art = []byte("fake-jpeg-bytes")
	h := NewRouter(p)

	rec := doRequest(t, h, "GET", "/api/albumart?url=http://example.com/a.mp3", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), p.art) {
		t.Errorf("body = %v, want %v", rec.Body.Bytes(), p.art)
	}
}

func TestHandleAlbumArtNotFound(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "GET", "/api/albumart?url=http://example.com/a.mp3", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleSearch(t *testing.T) {
	p := newFakePlayer()
	p.searchResults = []indexer.Result{{URL: "http://example.com/x.mp3", Title: "X"}}
	h := NewRouter(p)

	rec := doRequest(t, h, "GET", "/api/search?q=x", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got []indexer.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0] != p.searchResults[0] {
		t.Errorf("results = %v, want %v", got, p.searchResults)
	}
}

func TestHandleGetQueue(t *testing.T) {
	p := newFakePlayer()
	p.queue = []mpdclient.QueueTrack{{ID: 1, Position: 0, URL: "http://example.com/a.mp3"}}
	h := NewRouter(p)

	rec := doRequest(t, h, "GET", "/api/queue", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got []mpdclient.QueueTrack
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].URL != "http://example.com/a.mp3" {
		t.Errorf("queue = %v, want one entry for a.mp3", got)
	}
}

func TestHandleAddToQueue(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/queue", `{"url":"http://example.com/a.mp3"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if len(p.addToQueueCalls) != 1 || p.addToQueueCalls[0] != "http://example.com/a.mp3" {
		t.Errorf("addToQueueCalls = %v, want [http://example.com/a.mp3]", p.addToQueueCalls)
	}
}

func TestHandleAddToQueueBadBody(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/queue", `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleRemoveFromQueue(t *testing.T) {
	p := newFakePlayer()
	p.queue = []mpdclient.QueueTrack{{ID: 5, URL: "http://example.com/a.mp3"}}
	h := NewRouter(p)

	rec := doRequest(t, h, "DELETE", "/api/queue/5", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if len(p.removeFromQueueIDs) != 1 || p.removeFromQueueIDs[0] != 5 {
		t.Errorf("removeFromQueueIDs = %v, want [5]", p.removeFromQueueIDs)
	}
}

func TestHandleRemoveFromQueueBadID(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "DELETE", "/api/queue/not-a-number", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleMoveInQueue(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/queue/5/move", `{"position":2}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(p.moveInQueueCalls) != 1 || p.moveInQueueCalls[0] != [2]int{5, 2} {
		t.Errorf("moveInQueueCalls = %v, want [[5 2]]", p.moveInQueueCalls)
	}
}

func TestHandleMoveInQueueBadID(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/queue/not-a-number/move", `{"position":2}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleMoveInQueueBadBody(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/queue/5/move", `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandlePlayQueueItem(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/queue/5/play", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(p.playQueueItemIDs) != 1 || p.playQueueItemIDs[0] != 5 {
		t.Errorf("playQueueItemIDs = %v, want [5]", p.playQueueItemIDs)
	}
}

func TestHandlePlayQueueItemBadID(t *testing.T) {
	p := newFakePlayer()
	h := NewRouter(p)

	rec := doRequest(t, h, "POST", "/api/queue/not-a-number/play", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleClearQueue(t *testing.T) {
	p := newFakePlayer()
	p.queue = []mpdclient.QueueTrack{{ID: 1, URL: "http://example.com/a.mp3"}}
	h := NewRouter(p)

	rec := doRequest(t, h, "DELETE", "/api/queue", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if p.clearQueueCalls != 1 {
		t.Errorf("clearQueueCalls = %d, want 1", p.clearQueueCalls)
	}
	if len(p.queue) != 0 {
		t.Errorf("queue after clear = %v, want empty", p.queue)
	}
}
