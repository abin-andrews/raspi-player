package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pi-streamer/internal/config"
	"pi-streamer/internal/indexer"
	"pi-streamer/internal/mpdclient"
	"pi-streamer/internal/store"
)

// newRouter builds a router with a fakePlayer plus default (unconfigured)
// fakeConfig/fakeOled/fakeBucket — the tests below that don't care about
// config/OLED/bucket behavior use this instead of constructing their own
// fakes.
func newRouter(p Player) http.Handler {
	return NewRouter(p, newFakeConfig(), newFakeOled(), newFakeBucket())
}

// fakeConfig is a minimal in-memory Config implementation for handler tests.
type fakeConfig struct {
	cfg         config.Config
	setErr      error
	reloadErr   error
	setCalls    []config.Config
	reloadCalls int
}

func newFakeConfig() *fakeConfig { return &fakeConfig{} }

func (f *fakeConfig) Get() config.Config { return f.cfg }

func (f *fakeConfig) Set(cfg config.Config) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.cfg = cfg
	f.setCalls = append(f.setCalls, cfg)
	return nil
}

func (f *fakeConfig) Reload() error {
	f.reloadCalls++
	return f.reloadErr
}

// fakeOled is a minimal in-memory Oled implementation for handler tests.
type fakeOled struct {
	status   OledStatus
	ports    []string
	portsErr error
}

func newFakeOled() *fakeOled { return &fakeOled{} }

func (f *fakeOled) Status() OledStatus { return f.status }

func (f *fakeOled) ListPorts() ([]string, error) { return f.ports, f.portsErr }

// fakeBucket is a minimal in-memory Bucket implementation for handler tests.
type fakeBucket struct {
	status        BucketStatus
	queryCalls    [][]string
	queryResp     map[string]bool
	listResp      []BucketEntry
	listErr       error
	downloadsResp []BucketDownload
}

func newFakeBucket() *fakeBucket { return &fakeBucket{} }

func (f *fakeBucket) Status() BucketStatus { return f.status }

func (f *fakeBucket) Query(urls []string) map[string]bool {
	f.queryCalls = append(f.queryCalls, urls)
	return f.queryResp
}

func (f *fakeBucket) List() ([]BucketEntry, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listResp, nil
}

func (f *fakeBucket) Downloads() []BucketDownload { return f.downloadsResp }

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

	libraryResults []indexer.Result
	libraryErr     error

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

func (f *fakePlayer) Library(limit, offset int) ([]indexer.Result, error) {
	return f.libraryResults, f.libraryErr
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
	h := newRouter(p)

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
	h := newRouter(p)

	rec := doRequest(t, h, "POST", "/api/play", `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandlePauseResume(t *testing.T) {
	p := newFakePlayer()
	h := newRouter(p)

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
	h := newRouter(p)

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
	h := newRouter(p)

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
	h := newRouter(p)

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
	h := newRouter(p)

	rec := doRequest(t, h, "POST", "/api/playlists/nope", `{"url":"http://example.com/x.mp3"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleHistory(t *testing.T) {
	p := newFakePlayer()
	p.history = []store.Track{{URL: "http://example.com/a.mp3"}}
	h := newRouter(p)

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
	h := newRouter(p)

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
	h := newRouter(p)

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
	h := newRouter(p)

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
	h := newRouter(p)

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
	h := newRouter(p)

	rec := doRequest(t, h, "POST", "/api/volume", `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleSeekRelative(t *testing.T) {
	p := newFakePlayer()
	h := newRouter(p)

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
	h := newRouter(p)

	rec := doRequest(t, h, "POST", "/api/seek/relative", `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleAlbumArtFound(t *testing.T) {
	p := newFakePlayer()
	p.art = []byte("fake-jpeg-bytes")
	h := newRouter(p)

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
	h := newRouter(p)

	rec := doRequest(t, h, "GET", "/api/albumart?url=http://example.com/a.mp3", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleSearch(t *testing.T) {
	p := newFakePlayer()
	p.searchResults = []indexer.Result{{URL: "http://example.com/x.mp3", Title: "X"}}
	h := newRouter(p)

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

func TestHandleLibrary(t *testing.T) {
	p := newFakePlayer()
	p.libraryResults = []indexer.Result{{URL: "http://example.com/x.mp3", Title: "X"}}
	h := newRouter(p)

	rec := doRequest(t, h, "GET", "/api/library", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got []indexer.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0] != p.libraryResults[0] {
		t.Errorf("results = %v, want %v", got, p.libraryResults)
	}
}

func TestHandleLibraryError(t *testing.T) {
	p := newFakePlayer()
	p.libraryErr = errors.New("library unavailable")
	h := newRouter(p)

	rec := doRequest(t, h, "GET", "/api/library", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestHandleGetQueue(t *testing.T) {
	p := newFakePlayer()
	p.queue = []mpdclient.QueueTrack{{ID: 1, Position: 0, URL: "http://example.com/a.mp3"}}
	h := newRouter(p)

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
	h := newRouter(p)

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
	h := newRouter(p)

	rec := doRequest(t, h, "POST", "/api/queue", `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleRemoveFromQueue(t *testing.T) {
	p := newFakePlayer()
	p.queue = []mpdclient.QueueTrack{{ID: 5, URL: "http://example.com/a.mp3"}}
	h := newRouter(p)

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
	h := newRouter(p)

	rec := doRequest(t, h, "DELETE", "/api/queue/not-a-number", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleMoveInQueue(t *testing.T) {
	p := newFakePlayer()
	h := newRouter(p)

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
	h := newRouter(p)

	rec := doRequest(t, h, "POST", "/api/queue/not-a-number/move", `{"position":2}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleMoveInQueueBadBody(t *testing.T) {
	p := newFakePlayer()
	h := newRouter(p)

	rec := doRequest(t, h, "POST", "/api/queue/5/move", `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandlePlayQueueItem(t *testing.T) {
	p := newFakePlayer()
	h := newRouter(p)

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
	h := newRouter(p)

	rec := doRequest(t, h, "POST", "/api/queue/not-a-number/play", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleClearQueue(t *testing.T) {
	p := newFakePlayer()
	p.queue = []mpdclient.QueueTrack{{ID: 1, URL: "http://example.com/a.mp3"}}
	h := newRouter(p)

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

func TestHandleGetConfig(t *testing.T) {
	p := newFakePlayer()
	cfg := newFakeConfig()
	cfg.cfg = config.Config{OLED: config.OLED{Port: "/dev/ttyACM0", Baud: 115200}}
	h := NewRouter(p, cfg, newFakeOled(), newFakeBucket())

	rec := doRequest(t, h, "GET", "/api/config", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got config.Config
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != cfg.cfg {
		t.Errorf("got %+v, want %+v", got, cfg.cfg)
	}
}

func TestHandleSetConfig(t *testing.T) {
	p := newFakePlayer()
	cfg := newFakeConfig()
	o := newFakeOled()
	o.ports = []string{"/dev/ttyACM0"}
	h := NewRouter(p, cfg, o, newFakeBucket())

	rec := doRequest(t, h, "PUT", "/api/config", `{"oled":{"port":"/dev/ttyACM0","baud":115200}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	want := config.Config{OLED: config.OLED{Port: "/dev/ttyACM0", Baud: 115200}}
	if len(cfg.setCalls) != 1 || cfg.setCalls[0] != want {
		t.Errorf("setCalls = %+v, want [%+v]", cfg.setCalls, want)
	}
}

func TestHandleSetConfigBadBody(t *testing.T) {
	p := newFakePlayer()
	cfg := newFakeConfig()
	h := NewRouter(p, cfg, newFakeOled(), newFakeBucket())

	rec := doRequest(t, h, "PUT", "/api/config", `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleSetConfigError(t *testing.T) {
	p := newFakePlayer()
	cfg := newFakeConfig()
	cfg.setErr = errors.New("write failed")
	o := newFakeOled()
	o.ports = []string{"/dev/ttyACM0"}
	h := NewRouter(p, cfg, o, newFakeBucket())

	rec := doRequest(t, h, "PUT", "/api/config", `{"oled":{"port":"/dev/ttyACM0","baud":115200}}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestHandleSetConfigDisallowedBaud(t *testing.T) {
	p := newFakePlayer()
	cfg := newFakeConfig()
	o := newFakeOled()
	o.ports = []string{"/dev/ttyACM0"}
	h := NewRouter(p, cfg, o, newFakeBucket())

	rec := doRequest(t, h, "PUT", "/api/config", `{"oled":{"port":"/dev/ttyACM0","baud":31337}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if len(cfg.setCalls) != 0 {
		t.Errorf("setCalls = %+v, want none (validation should reject before Set)", cfg.setCalls)
	}
}

func TestHandleSetConfigUnavailablePort(t *testing.T) {
	p := newFakePlayer()
	cfg := newFakeConfig()
	o := newFakeOled()
	o.ports = []string{"/dev/ttyUSB0"} // does not include the requested port
	h := NewRouter(p, cfg, o, newFakeBucket())

	rec := doRequest(t, h, "PUT", "/api/config", `{"oled":{"port":"/dev/ttyACM0","baud":115200}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if len(cfg.setCalls) != 0 {
		t.Errorf("setCalls = %+v, want none (validation should reject before Set)", cfg.setCalls)
	}
}

func TestHandleSetConfigEmptyPortSkipsValidation(t *testing.T) {
	p := newFakePlayer()
	cfg := newFakeConfig()
	o := newFakeOled() // no ports registered, and no baud given either
	h := NewRouter(p, cfg, o, newFakeBucket())

	rec := doRequest(t, h, "PUT", "/api/config", `{"oled":{"port":"","baud":0}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (empty port means \"disabled\", always allowed); body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleOledBauds(t *testing.T) {
	p := newFakePlayer()
	h := newRouter(p)

	rec := doRequest(t, h, "GET", "/api/oled/bauds", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got []int
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) == 0 {
		t.Error("bauds list is empty, want at least the common rates")
	}
	found := false
	for _, b := range got {
		if b == 115200 {
			found = true
		}
	}
	if !found {
		t.Errorf("bauds = %v, want it to include 115200", got)
	}
}

func TestHandleReloadConfig(t *testing.T) {
	p := newFakePlayer()
	cfg := newFakeConfig()
	cfg.cfg = config.Config{OLED: config.OLED{Port: "/dev/ttyACM1", Baud: 9600}}
	h := NewRouter(p, cfg, newFakeOled(), newFakeBucket())

	rec := doRequest(t, h, "POST", "/api/config/reload", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if cfg.reloadCalls != 1 {
		t.Errorf("reloadCalls = %d, want 1", cfg.reloadCalls)
	}
	var got config.Config
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != cfg.cfg {
		t.Errorf("got %+v, want %+v", got, cfg.cfg)
	}
}

func TestHandleReloadConfigError(t *testing.T) {
	p := newFakePlayer()
	cfg := newFakeConfig()
	cfg.reloadErr = errors.New("read failed")
	h := NewRouter(p, cfg, newFakeOled(), newFakeBucket())

	rec := doRequest(t, h, "POST", "/api/config/reload", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestHandleOledStatus(t *testing.T) {
	p := newFakePlayer()
	o := newFakeOled()
	o.status = OledStatus{Connected: true, Port: "/dev/ttyACM0", Baud: 115200}
	h := NewRouter(p, newFakeConfig(), o, newFakeBucket())

	rec := doRequest(t, h, "GET", "/api/oled/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got OledStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != o.status {
		t.Errorf("got %+v, want %+v", got, o.status)
	}
}

func TestHandleOledPorts(t *testing.T) {
	p := newFakePlayer()
	o := newFakeOled()
	o.ports = []string{"/dev/ttyACM0", "/dev/ttyUSB0"}
	h := NewRouter(p, newFakeConfig(), o, newFakeBucket())

	rec := doRequest(t, h, "GET", "/api/oled/ports", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got []string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 || got[0] != "/dev/ttyACM0" || got[1] != "/dev/ttyUSB0" {
		t.Errorf("got %v, want %v", got, o.ports)
	}
}

func TestHandleOledPortsError(t *testing.T) {
	p := newFakePlayer()
	o := newFakeOled()
	o.portsErr = errors.New("enumeration failed")
	h := NewRouter(p, newFakeConfig(), o, newFakeBucket())

	rec := doRequest(t, h, "GET", "/api/oled/ports", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestHandleBucketStatus(t *testing.T) {
	p := newFakePlayer()
	b := newFakeBucket()
	b.status = BucketStatus{Mode: "bucket", UsedBytes: 100, MaxBytes: 1000}
	h := NewRouter(p, newFakeConfig(), newFakeOled(), b)

	rec := doRequest(t, h, "GET", "/api/bucket/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got BucketStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != b.status {
		t.Errorf("got %+v, want %+v", got, b.status)
	}
}

func TestHandleBucketQuery(t *testing.T) {
	p := newFakePlayer()
	b := newFakeBucket()
	b.queryResp = map[string]bool{"http://example.com/a.mp3": true, "http://example.com/b.mp3": false}
	h := NewRouter(p, newFakeConfig(), newFakeOled(), b)

	rec := doRequest(t, h, "POST", "/api/bucket/query",
		`{"urls":["http://example.com/a.mp3","http://example.com/b.mp3"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["http://example.com/a.mp3"] != true || got["http://example.com/b.mp3"] != false {
		t.Errorf("got %v, want %v", got, b.queryResp)
	}
	if len(b.queryCalls) != 1 || len(b.queryCalls[0]) != 2 {
		t.Errorf("queryCalls = %v, want one call with 2 URLs", b.queryCalls)
	}
}

func TestHandleBucketQueryBadBody(t *testing.T) {
	p := newFakePlayer()
	h := newRouter(p)

	rec := doRequest(t, h, "POST", "/api/bucket/query", `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleBucketList(t *testing.T) {
	p := newFakePlayer()
	b := newFakeBucket()
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	b.listResp = []BucketEntry{{URL: "http://example.com/a.mp3", SizeBytes: 1234, LastAccessed: when}}
	h := NewRouter(p, newFakeConfig(), newFakeOled(), b)

	rec := doRequest(t, h, "GET", "/api/bucket/list", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got []BucketEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].URL != "http://example.com/a.mp3" || got[0].SizeBytes != 1234 {
		t.Errorf("got %+v, want %+v", got, b.listResp)
	}
}

func TestHandleBucketListError(t *testing.T) {
	p := newFakePlayer()
	b := newFakeBucket()
	b.listErr = errors.New("list failed")
	h := NewRouter(p, newFakeConfig(), newFakeOled(), b)

	rec := doRequest(t, h, "GET", "/api/bucket/list", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestHandleBucketDownloads(t *testing.T) {
	p := newFakePlayer()
	b := newFakeBucket()
	b.downloadsResp = []BucketDownload{
		{URL: "http://example.com/a.mp3", ReceivedBytes: 500, TotalBytes: 2000},
	}
	h := NewRouter(p, newFakeConfig(), newFakeOled(), b)

	rec := doRequest(t, h, "GET", "/api/bucket/downloads", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got []BucketDownload
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].URL != "http://example.com/a.mp3" || got[0].ReceivedBytes != 500 {
		t.Errorf("got %+v, want %+v", got, b.downloadsResp)
	}
}

func TestHandleSetConfigDisallowedMode(t *testing.T) {
	p := newFakePlayer()
	h := newRouter(p)

	rec := doRequest(t, h, "PUT", "/api/config", `{"bucket":{"mode":"teleport"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleSetConfigNegativeBucketSize(t *testing.T) {
	p := newFakePlayer()
	h := newRouter(p)

	rec := doRequest(t, h, "PUT", "/api/config", `{"bucket":{"mode":"bucket","maxSizeMb":-1}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleSetConfigValidBucketSettings(t *testing.T) {
	p := newFakePlayer()
	cfg := newFakeConfig()
	h := NewRouter(p, cfg, newFakeOled(), newFakeBucket())

	rec := doRequest(t, h, "PUT", "/api/config",
		`{"bucket":{"mode":"bucket","maxSizeMb":1024,"favoritesMaxSizeMb":2048,"minFreeMb":512}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	want := config.Bucket{Mode: config.ModeBucket, MaxSizeMB: 1024, FavoritesMaxSizeMB: 2048, MinFreeMB: 512}
	if len(cfg.setCalls) != 1 || cfg.setCalls[0].Bucket != want {
		t.Errorf("setCalls = %+v, want [%+v]", cfg.setCalls, want)
	}
}
