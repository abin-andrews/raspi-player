package serial

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fakePort is an in-memory stand-in for a real go.bug.st/serial.Port,
// recording every Write call (so tests can assert on chunking) and serving
// Read from a preloaded reply buffer.
type fakePort struct {
	mu       sync.Mutex
	writes   [][]byte
	reply    []byte
	readErr  error
	closed   bool
	closeErr error
}

func (f *fakePort) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := append([]byte(nil), p...)
	f.writes = append(f.writes, cp)
	return len(p), nil
}

func (f *fakePort) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return 0, f.readErr
	}
	if len(f.reply) == 0 {
		// Mirrors go.bug.st/serial: a timed-out Read returns (0, nil), not
		// an error.
		return 0, nil
	}
	n := copy(p, f.reply)
	f.reply = f.reply[n:]
	return n, nil
}

func (f *fakePort) SetReadTimeout(t time.Duration) error { return nil }

func (f *fakePort) Close() error {
	f.closed = true
	return f.closeErr
}

func withFastTimeouts(t *testing.T) {
	t.Helper()
	origChunkDelay, origReplyTimeout, origPollTimeout := chunkDelay, replyTimeout, pollTimeout
	chunkDelay = time.Millisecond
	replyTimeout = 100 * time.Millisecond
	pollTimeout = 5 * time.Millisecond
	t.Cleanup(func() {
		chunkDelay, replyTimeout, pollTimeout = origChunkDelay, origReplyTimeout, origPollTimeout
	})
}

func TestSendChunksWrites(t *testing.T) {
	withFastTimeouts(t)
	fp := &fakePort{reply: []byte("OK\n")}
	c := &Client{port: fp}

	reply, err := c.Send("TITLE\tDreams")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if reply != "OK" {
		t.Errorf("reply = %q, want OK", reply)
	}

	var sent []byte
	for _, w := range fp.writes {
		if len(w) > chunkSize {
			t.Errorf("write of %d bytes exceeds chunkSize %d: %q", len(w), chunkSize, w)
		}
		sent = append(sent, w...)
	}
	if want := "TITLE\tDreams\n"; string(sent) != want {
		t.Errorf("assembled write = %q, want %q", sent, want)
	}
	if len(fp.writes) < 2 {
		t.Errorf("expected the command to be split across multiple writes, got %d", len(fp.writes))
	}
}

func TestSendReturnsERRReply(t *testing.T) {
	withFastTimeouts(t)
	fp := &fakePort{reply: []byte("ERR\n")}
	c := &Client{port: fp}

	reply, err := c.Send("BOGUS")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if reply != "ERR" {
		t.Errorf("reply = %q, want ERR", reply)
	}
}

func TestSendReplyAcrossMultipleReads(t *testing.T) {
	withFastTimeouts(t)
	// A reply arriving in several small chunks (as a real serial link
	// might deliver it) must still be reassembled into one line.
	fp := &fakePort{reply: []byte("OK\n")}
	c := &Client{port: fp}

	reply, err := c.Send("END")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if reply != "OK" {
		t.Errorf("reply = %q, want OK", reply)
	}
}

func TestSendTimesOutOnSilentDevice(t *testing.T) {
	withFastTimeouts(t)
	fp := &fakePort{} // never produces a reply
	c := &Client{port: fp}

	start := time.Now()
	_, err := c.Send("PING")
	if err == nil {
		t.Fatal("Send: expected a timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Send took %v, want it bounded by the shrunk replyTimeout", elapsed)
	}
}

func TestSendReadError(t *testing.T) {
	withFastTimeouts(t)
	fp := &fakePort{readErr: errors.New("port closed")}
	c := &Client{port: fp}

	if _, err := c.Send("PING"); err == nil {
		t.Fatal("Send: expected an error, got nil")
	}
}

func TestClose(t *testing.T) {
	fp := &fakePort{}
	c := &Client{port: fp}

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !fp.closed {
		t.Error("expected underlying port to be closed")
	}
}

func TestSendTrimsWhitespace(t *testing.T) {
	withFastTimeouts(t)
	fp := &fakePort{reply: []byte("OK\r\n")}
	c := &Client{port: fp}

	reply, err := c.Send("STATE\tPLAYING")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if reply != "OK" {
		t.Errorf("reply = %q, want OK (whitespace trimmed)", reply)
	}
}
