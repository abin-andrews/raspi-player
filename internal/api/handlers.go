package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"pi-streamer/internal/config"
	"pi-streamer/internal/store"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// discoveryResponse is what GET /api/discover answers with — deliberately
// static and dependency-free (no Player/mpd involved at all), so a client
// scanning the LAN for this daemon gets an instant, always-available
// positive identification rather than tripping over whatever transient
// state Status()/mpd happen to be in. Service/Version let a client (the
// browser extension in extension/) tell "this is actually pi-streamer,"
// not just "something answered on this port" — matters once a scan is
// probing arbitrary addresses that might be running an unrelated HTTP
// server. Version is this response shape's own version, not the app's.
type discoveryResponse struct {
	Service string `json:"service"`
	Version int    `json:"version"`
}

func handleDiscover(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, discoveryResponse{Service: "pi-streamer", Version: 1})
}

func handlePlay(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL string `json:"url"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := p.PlayURL(req.URL); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, nil)
	}
}

func handlePause(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := p.Pause(); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, nil)
	}
}

func handleResume(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := p.Resume(); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, nil)
	}
}

func handleStatus(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := p.Status()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, status)
	}
}

func handleListFavorites(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		favs, err := p.Favorites()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, favs)
	}
}

func handleAddFavorite(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := p.AddFavorite(req.URL, req.Title); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusCreated, nil)
	}
}

func handleRemoveFavorite(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		url := r.URL.Query().Get("url")
		if err := p.RemoveFavorite(url); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusNoContent, nil)
	}
}

func handleListPlaylists(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		names, err := p.Playlists()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, names)
	}
}

func handleCreatePlaylist(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string `json:"name"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := p.CreatePlaylist(req.Name); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrPlaylistExists) {
				status = http.StatusConflict
			}
			writeError(w, status, err)
			return
		}
		writeJSON(w, http.StatusCreated, nil)
	}
}

func handleGetPlaylist(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tracks, err := p.Playlist(r.PathValue("name"))
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrPlaylistNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, err)
			return
		}
		writeJSON(w, http.StatusOK, tracks)
	}
}

func handleAddToPlaylist(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := p.AddToPlaylist(r.PathValue("name"), req.URL, req.Title); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrPlaylistNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, err)
			return
		}
		writeJSON(w, http.StatusCreated, nil)
	}
}

func handleHistory(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 0
		if q := r.URL.Query().Get("limit"); q != "" {
			if n, err := strconv.Atoi(q); err == nil {
				limit = n
			}
		}
		hist, err := p.History(limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, hist)
	}
}

func handleSeek(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Seconds float64 `json:"seconds"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := p.Seek(req.Seconds); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, nil)
	}
}

func handleNext(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := p.Next(); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, nil)
	}
}

func handlePrevious(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := p.Previous(); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, nil)
	}
}

func handleVolume(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Volume int `json:"volume"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := p.SetVolume(req.Volume); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, nil)
	}
}

func handleSeekRelative(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Seconds float64 `json:"seconds"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := p.SeekRelative(req.Seconds); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, nil)
	}
}

// handleAlbumArt resolves url's art (fetching+persisting to disk on first
// request, see Art) and redirects to the static path it's served from —
// this handler itself never holds or writes image bytes; that all happens
// behind Art.Resolve (cmd/pi-streamer's artAdapter + internal/artstore).
// A confirmed "no art" result 404s directly rather than redirecting
// anywhere.
func handleAlbumArt(art Art) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		url := r.URL.Query().Get("url")
		artist := r.URL.Query().Get("artist")
		album := r.URL.Query().Get("album")

		path, ok, err := art.Resolve(url, artist, album)
		if err != nil || !ok {
			http.NotFound(w, r)
			return
		}

		// Art for a given URL practically never changes once tagged, so
		// browsers can cache this redirect aggressively — this is what
		// stops the same track's art from round-tripping through this
		// handler at all (let alone mpd) on repeat requests; the static
		// path itself is served by a plain http.FileServer.
		w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
		http.Redirect(w, r, path, http.StatusFound)
	}
}

// handleRefreshAlbumArt bypasses whatever's already known and always
// re-resolves — e.g. a file was re-tagged with new art since it was last
// resolved, or a MusicBrainz fallback should be retried now that it's
// configured. Unlike handleAlbumArt, this never redirects — it reports
// the outcome as JSON so the frontend can react (e.g. re-render the art)
// without a second round trip.
func handleRefreshAlbumArt(art Art) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL    string `json:"url"`
			Artist string `json:"artist"`
			Album  string `json:"album"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		path, ok, err := art.Refresh(req.URL, req.Artist, req.Album)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, ArtStatus{HasArt: ok, Path: path})
	}
}

// handleAlbumArtQuery reports, for each of the submitted urls, what's
// already known — never fetching anything — so the frontend can decide up
// front whether to request the static path directly, or go straight to a
// client-generated placeholder, instead of always trying an image and
// reacting to onError. A url this has no answer for yet (never resolved)
// is simply omitted from the response map.
func handleAlbumArtQuery(art Art) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URLs []string `json:"urls"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, art.Query(req.URLs))
	}
}

// handleWarmAlbumArt kicks off a background scan of the whole library,
// resolving art for every entry not already known — an explicit "initiate"
// action so a user can pre-warm the cache (e.g. right after adding a batch
// of new tracks) rather than only discovering art lazily, one track at a
// time, as each is first viewed. Returns immediately.
func handleWarmAlbumArt(art Art) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		art.Warm()
		writeJSON(w, http.StatusAccepted, nil)
	}
}

// handleSuggestMetadata looks up candidate Title/Artist/Album matches for
// the submitted query (typically whatever's already in an edit form, or a
// name derived from the track's URL) — for the Library edit form's "Look
// up" action to show as suggestions, not applied automatically.
func handleSuggestMetadata(art Art) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if req.Query == "" {
			writeError(w, http.StatusBadRequest, errors.New("query is required"))
			return
		}
		suggestions, err := art.Suggest(req.Query)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string][]MetadataSuggestion{"suggestions": suggestions})
	}
}

// handleSetCustomArt fetches the submitted imageUrl and records it as
// scope/key's custom art — see Art.SetCustomArt's doc comment for what
// scope/key mean and how this interacts with auto-resolution afterward.
func handleSetCustomArt(art Art) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Scope    string `json:"scope"`
			Key      string `json:"key"`
			ImageURL string `json:"imageUrl"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if req.Scope == "" || req.Key == "" || req.ImageURL == "" {
			writeError(w, http.StatusBadRequest, errors.New("scope, key, and imageUrl are all required"))
			return
		}
		status, err := art.SetCustomArt(req.Scope, req.Key, req.ImageURL)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, status)
	}
}

// handleClearCustomArt removes scope/key's custom art, reverting it to
// whatever auto-resolution finds on its own next Resolve/Refresh.
func handleClearCustomArt(art Art) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Scope string `json:"scope"`
			Key   string `json:"key"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if req.Scope == "" || req.Key == "" {
			writeError(w, http.StatusBadRequest, errors.New("scope and key are both required"))
			return
		}
		if err := art.ClearCustomArt(req.Scope, req.Key); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleListJobs reports every currently-tracked background job (running
// or recently finished) — the frontend normally gets this pushed over
// /ws instead (see main.go's wsMessage, mirroring how bucket-download
// progress is pushed rather than polled), but this stays available for a
// one-off check, same as GET /api/bucket/downloads.
func handleListJobs(j Jobs) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, j.List())
	}
}

func handleGetQueue(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tracks, err := p.Queue()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, tracks)
	}
}

func handleAddToQueue(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL string `json:"url"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := p.AddToQueue(req.URL); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusCreated, nil)
	}
}

func handleRemoveFromQueue(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := p.RemoveFromQueue(id); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusNoContent, nil)
	}
}

func handleMoveInQueue(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		var req struct {
			Position int `json:"position"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := p.MoveInQueue(id, req.Position); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, nil)
	}
}

func handlePlayQueueItem(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := p.PlayQueueItem(id); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, nil)
	}
}

func handleClearQueue(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := p.ClearQueue(); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusNoContent, nil)
	}
}

func handleSearch(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		limit := 0
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil {
				limit = n
			}
		}
		results, err := p.Search(q, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, results)
	}
}

// trackInfoResponse is GET /api/track's response shape — a plain
// title/artist/album description of a URL, on demand, for a caller (the
// browser extension in extension/, in particular) that already submitted
// the URL via /api/play or /api/queue and wants to describe what it just
// sent, e.g. in a notification.
type trackInfoResponse struct {
	Title  string `json:"title"`
	Artist string `json:"artist,omitempty"`
	Album  string `json:"album,omitempty"`
}

func handleTrackInfo(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		url := r.URL.Query().Get("url")
		if url == "" {
			writeError(w, http.StatusBadRequest, errors.New("missing url query parameter"))
			return
		}
		title, artist, album, err := p.TrackInfo(url)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, trackInfoResponse{Title: title, Artist: artist, Album: album})
	}
}

func handleLibrary(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 0
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil {
				limit = n
			}
		}
		offset := 0
		if o := r.URL.Query().Get("offset"); o != "" {
			if n, err := strconv.Atoi(o); err == nil {
				offset = n
			}
		}
		results, err := p.Library(limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, results)
	}
}

func handleAddToLibrary(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL    string `json:"url"`
			Title  string `json:"title"`
			Artist string `json:"artist"`
			Album  string `json:"album"`
			Tags   string `json:"tags"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := p.AddToLibrary(req.URL, req.Title, req.Artist, req.Album, req.Tags); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusCreated, nil)
	}
}

func handleRemoveFromLibrary(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		url := r.URL.Query().Get("url")
		if err := p.RemoveFromLibrary(url); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusNoContent, nil)
	}
}

func handleGetConfig(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, cfg.Get())
	}
}

// handleSetConfig replaces the whole config (not a partial patch) and
// persists it — the frontend always sends back the full object it got from
// GET /api/config with its edits applied. The OLED port/baud are validated
// against the daemon's own currently-detected ports and allowed baud rates
// before anything is written, rather than trusting arbitrary client input
// (a typo'd port would otherwise just silently fail to connect later).
func handleSetConfig(cfg Config, o Oled) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var next config.Config
		if err := decodeJSON(r, &next); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := validateOLED(next.OLED, o); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := validateBucket(next.Bucket); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := cfg.Set(next); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, next)
	}
}

// validateOLED rejects an OLED config the daemon shouldn't even attempt: an
// empty port always passes (that's "disabled"), but a non-empty one must
// name a port o currently sees attached, at one of the allowed baud rates.
func validateOLED(oled config.OLED, o Oled) error {
	if oled.ElapsedUpdateIntervalSeconds < 0 {
		return errors.New("elapsed update interval must not be negative")
	}
	if oled.Port == "" {
		return nil
	}
	if !config.IsAllowedBaud(oled.Baud) {
		return fmt.Errorf("%d is not an allowed baud rate", oled.Baud)
	}
	ports, err := o.ListPorts()
	if err != nil {
		return fmt.Errorf("list serial ports: %w", err)
	}
	if !slices.Contains(ports, oled.Port) {
		return fmt.Errorf("port %q is not currently available", oled.Port)
	}
	return nil
}

// validateBucket rejects a Bucket config with a bogus mode or a negative
// size — zero-valued sizes are fine (they mean "use the built-in default",
// applied by cmd/pi-streamer, not this package).
func validateBucket(b config.Bucket) error {
	if b.Mode != "" && !config.IsAllowedMode(b.Mode) {
		return fmt.Errorf("%q is not an allowed playback mode", b.Mode)
	}
	if b.MaxSizeMB < 0 {
		return errors.New("bucket max size must not be negative")
	}
	if b.FavoritesMaxSizeMB < 0 {
		return errors.New("favorites max size must not be negative")
	}
	if b.MinFreeMB < 0 {
		return errors.New("minimum free disk space must not be negative")
	}
	return nil
}

// handleReloadConfig re-reads the config file from disk, for picking up a
// hand-edit (e.g. made over SSH) without restarting the daemon.
func handleReloadConfig(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Reload(); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, cfg.Get())
	}
}

func handleOledStatus(o Oled) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, o.Status())
	}
}

func handleOledPorts(o Oled) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ports, err := o.ListPorts()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, ports)
	}
}

// handleOledBauds returns the fixed set of baud rates the daemon accepts,
// so the frontend's dropdown is always built from the same list the
// backend validates against instead of a hand-copied duplicate.
func handleOledBauds(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, config.AllowedBauds)
}

func handleBucketStatus(b Bucket) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, b.Status())
	}
}

// handleBucketQuery reports, for a batch of URLs, whether each is currently
// cached — so a rendered list of tracks (Queue/Search/Favorites/History)
// needs one request for all its "cached" badges rather than one per row.
func handleBucketQuery(b Bucket) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URLs []string `json:"urls"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, b.Query(req.URLs))
	}
}

func handleBucketList(b Bucket) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := b.List()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, entries)
	}
}

func handleBucketDownloads(b Bucket) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, b.Downloads())
	}
}
