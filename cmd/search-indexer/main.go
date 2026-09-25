// Command search-indexer runs the SQLite/FTS5-backed search service over
// the URL collection. It runs on a separate, more capable machine than the
// Raspberry Pi, since the Pi Zero 2W can't do fast search indexing itself.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"strconv"

	"pi-streamer/internal/search"
)

func main() {
	dbPath := flag.String("db", "search.db", "path to the SQLite database file (or :memory:)")
	httpAddr := flag.String("http-addr", ":8081", "address for the search HTTP API to listen on")
	flag.Parse()

	idx, err := search.Open(*dbPath)
	if err != nil {
		log.Fatalf("open search index at %s: %v", *dbPath, err)
	}
	defer idx.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /index", handleIndex(idx))
	mux.HandleFunc("GET /search", handleSearch(idx))

	log.Printf("search-indexer listening on %s (db: %s)", *httpAddr, *dbPath)
	if err := http.ListenAndServe(*httpAddr, mux); err != nil {
		log.Fatal(err)
	}
}

func handleIndex(idx *search.Index) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL   string `json:"url"`
			Title string `json:"title"`
			Tags  string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := idx.IndexURL(req.URL, req.Title, req.Tags); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}
}

func handleSearch(idx *search.Index) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		limit := 0
		if q := r.URL.Query().Get("limit"); q != "" {
			if n, err := strconv.Atoi(q); err == nil {
				limit = n
			}
		}
		results, err := idx.Search(query, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(results)
	}
}
