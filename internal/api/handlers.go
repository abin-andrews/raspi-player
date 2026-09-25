package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

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
