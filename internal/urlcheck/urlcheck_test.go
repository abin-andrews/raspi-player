package urlcheck

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckSucceedsOn200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &Checker{}
	if err := c.Check(srv.URL); err != nil {
		t.Errorf("Check: %v, want nil", err)
	}
}

func TestCheckSucceedsOnPartialContent(t *testing.T) {
	// A server that actually honors the Range: bytes=0-0 header, like a
	// real streaming server would.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "" {
			t.Errorf("request missing Range header")
		}
		w.Header().Set("Content-Range", "bytes 0-0/1000")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte{0})
	}))
	defer srv.Close()

	c := &Checker{}
	if err := c.Check(srv.URL); err != nil {
		t.Errorf("Check: %v, want nil", err)
	}
}

func TestCheckFailsOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := &Checker{}
	if err := c.Check(srv.URL); err == nil {
		t.Error("Check: want error for a 404 response, got nil")
	}
}

func TestCheckFailsOn500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := &Checker{}
	if err := c.Check(srv.URL); err == nil {
		t.Error("Check: want error for a 500 response, got nil")
	}
}

func TestCheckFailsOnUnreachableHost(t *testing.T) {
	c := &Checker{Timeout: 200 * time.Millisecond}
	// Port 0 on localhost never accepts connections.
	if err := c.Check("http://127.0.0.1:0/stream.mp3"); err == nil {
		t.Error("Check: want error for an unreachable host, got nil")
	}
}

func TestCheckTimesOutOnSlowServer(t *testing.T) {
	unblock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-unblock // never respond before the test's timeout fires
	}))
	// srv.Close waits for outstanding requests to finish, so the blocked
	// handler above must be unblocked first — deferred in this order so
	// close(unblock) runs before srv.Close (defers are LIFO).
	defer srv.Close()
	defer close(unblock)

	c := &Checker{Timeout: 100 * time.Millisecond}
	start := time.Now()
	err := c.Check(srv.URL)
	if err == nil {
		t.Error("Check: want a timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Check took %v, want it bounded by the configured timeout", elapsed)
	}
}

func TestCheckSkipsNonHTTPURLs(t *testing.T) {
	c := &Checker{}
	if err := c.Check("file:///some/local/path.mp3"); err != nil {
		t.Errorf("Check on a non-http(s) URL: %v, want nil (not our concern)", err)
	}
}
