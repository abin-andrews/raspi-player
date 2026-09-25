// Package indexer provides an HTTP client for the search-indexer service.
package indexer

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestServer mimics the two routes exposed by cmd/search-indexer closely
// enough to exercise the client's request-building and response-parsing
// logic: POST /index and GET /search.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	mux.HandleFunc("POST /index", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL   string `json:"url"`
			Title string `json:"title"`
			Tags  string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.URL == "" {
			http.Error(w, "url is required", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})

	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "fail" {
			http.Error(w, "search failed", http.StatusBadRequest)
			return
		}

		limit := r.URL.Query().Get("limit")

		results := []Result{
			{URL: "https://example.com/a", Title: "A", Tags: "foo"},
			{URL: "https://example.com/b", Title: "B", Tags: "bar"},
		}
		if limit == "1" {
			results = results[:1]
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(results)
	})

	return httptest.NewServer(mux)
}

func TestNewTrimsTrailingSlash(t *testing.T) {
	c := New("http://example.com:8081/")
	if c.baseURL != "http://example.com:8081" {
		t.Fatalf("baseURL = %q, want %q", c.baseURL, "http://example.com:8081")
	}

	c2 := New("http://example.com:8081")
	if c2.baseURL != "http://example.com:8081" {
		t.Fatalf("baseURL = %q, want %q", c2.baseURL, "http://example.com:8081")
	}
}

func TestIndexURLSuccess(t *testing.T) {
	var gotBody []byte
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /index", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var err error
		gotBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusCreated)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL)
	if err := c.IndexURL("https://example.com/x", "Title X", "tag1,tag2"); err != nil {
		t.Fatalf("IndexURL returned error: %v", err)
	}

	if gotPath != "/index" {
		t.Errorf("request path = %q, want /index", gotPath)
	}

	var decoded struct {
		URL   string `json:"url"`
		Title string `json:"title"`
		Tags  string `json:"tags"`
	}
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if decoded.URL != "https://example.com/x" || decoded.Title != "Title X" || decoded.Tags != "tag1,tag2" {
		t.Errorf("decoded body = %+v, want {https://example.com/x Title X tag1,tag2}", decoded)
	}
}

func TestIndexURLError(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	c := New(srv.URL)
	err := c.IndexURL("", "no url", "")
	if err == nil {
		t.Fatal("expected error for empty url, got nil")
	}
	if !strings.Contains(err.Error(), "400") && !strings.Contains(err.Error(), "url is required") {
		t.Errorf("error = %q, want it to mention the status code or body", err.Error())
	}
}

func TestSearchSuccess(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	c := New(srv.URL)
	results, err := c.Search("hello world", 0)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].URL != "https://example.com/a" || results[0].Title != "A" || results[0].Tags != "foo" {
		t.Errorf("results[0] = %+v, want {https://example.com/a A foo}", results[0])
	}
}

func TestSearchOmitsLimitWhenNonPositive(t *testing.T) {
	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]Result{})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.Search("term", 0); err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if strings.Contains(gotQuery, "limit") {
		t.Errorf("query = %q, should omit limit param when limit <= 0", gotQuery)
	}

	if _, err := c.Search("term", -5); err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if strings.Contains(gotQuery, "limit") {
		t.Errorf("query = %q, should omit limit param when limit < 0", gotQuery)
	}
}

func TestSearchIncludesLimitAndEscapesQuery(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	c := New(srv.URL)
	results, err := c.Search("foo bar", 1)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1 (limit should have been applied)", len(results))
	}
}

func TestSearchError(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.Search("fail", 0)
	if err == nil {
		t.Fatal("expected error for query \"fail\", got nil")
	}
	if !strings.Contains(err.Error(), "400") && !strings.Contains(err.Error(), "search failed") {
		t.Errorf("error = %q, want it to mention the status code or body", err.Error())
	}
}
