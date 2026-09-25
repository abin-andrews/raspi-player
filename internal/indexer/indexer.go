// Package indexer provides an HTTP client for talking to the search-indexer
// service (cmd/search-indexer) over the network. It deliberately does not
// import internal/search, so that consumers of this package (in particular
// the main pi-streamer daemon) don't pull in internal/search's
// modernc.org/sqlite dependency just to get this 3-field struct and a couple
// of HTTP calls.
package indexer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// defaultTimeout bounds how long a single request to the search-indexer
// service is allowed to take.
const defaultTimeout = 5 * time.Second

// Result is a single search match returned by the search-indexer service.
// It is an independent copy of internal/search.Result's shape, not an
// import of that package.
type Result struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Tags  string `json:"tags"`
}

// Client is an HTTP client for the search-indexer service.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New returns a Client for the search-indexer service running at baseURL
// (e.g. "http://host:8081"). A trailing slash on baseURL, if present, is
// trimmed so callers can pass either "http://host:8081" or
// "http://host:8081/".
func New(baseURL string) *Client {
	return &Client{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{Timeout: defaultTimeout},
	}
}

// IndexURL POSTs the given url, title, and tags to the search-indexer
// service's /index endpoint. It returns a descriptive error if the request
// fails or the response status is not 2xx.
func (c *Client) IndexURL(urlStr, title, tags string) error {
	body, err := json.Marshal(struct {
		URL   string `json:"url"`
		Title string `json:"title"`
		Tags  string `json:"tags"`
	}{URL: urlStr, Title: title, Tags: tags})
	if err != nil {
		return fmt.Errorf("indexer: marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/index", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("indexer: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("indexer: POST /index: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("indexer: POST /index: status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	return nil
}

// Search GETs the search-indexer service's /search endpoint with the given
// query and limit, and decodes the JSON array response into a []Result. If
// limit is <= 0, the limit query parameter is omitted entirely, leaving the
// server to apply its own default. It returns a descriptive error if the
// request fails or the response status is not 2xx.
func (c *Client) Search(query string, limit int) ([]Result, error) {
	values := url.Values{}
	values.Set("q", query)
	if limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}

	reqURL := c.baseURL + "/search?" + values.Encode()

	resp, err := c.httpClient.Get(reqURL)
	if err != nil {
		return nil, fmt.Errorf("indexer: GET /search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("indexer: GET /search: status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var results []Result
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("indexer: decode response: %w", err)
	}

	return results, nil
}
