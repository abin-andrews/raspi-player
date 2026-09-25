// Package urlcheck verifies a URL is reachable and streamable before mpd is
// asked to play it. mpd has no good way to report "the remote server never
// responded, or came back with an error page" back through Status() — a bad
// URL can just leave mpd stuck, or silently leave it playing whatever was
// already loaded (see internal/player's PlayURL/AddToQueue, which are this
// package's only callers) — so this catches that failure mode up front,
// with a clear error the caller can surface immediately instead.
package urlcheck

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const defaultTimeout = 5 * time.Second

// Checker verifies a URL responds successfully before it's handed to mpd.
type Checker struct {
	// Timeout bounds how long Check waits for a response. Defaults to 5
	// seconds if zero.
	Timeout time.Duration
}

// Check does a ranged GET (Range: bytes=0-0) rather than HEAD, since many
// internet radio/streaming servers don't implement HEAD at all — a ranged
// GET behaves the same way but the response is closed immediately once its
// headers arrive, without waiting for or downloading any actual audio.
// Non-http(s) URLs (e.g. a local file path some mpd setups support) are
// left alone and always report success — this only guards the network-
// fetch failure mode.
func (c *Checker) Check(url string) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil
	}

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Range", "bytes=0-0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("responded with status %d", resp.StatusCode)
	}
	return nil
}
