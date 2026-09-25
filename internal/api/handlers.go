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

func handleAlbumArt(p Player) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		url := r.URL.Query().Get("url")
		data, err := p.AlbumArt(url)
		if err != nil || len(data) == 0 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", http.DetectContentType(data))
		w.Write(data)
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
