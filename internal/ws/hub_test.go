package ws

import (
	"testing"
	"time"
)

const testTimeout = 100 * time.Millisecond

// registerTestClient registers c with the hub and fails the test if the
// hub's run loop doesn't accept it within testTimeout.
func registerTestClient(t *testing.T, h *Hub, c *client) {
	t.Helper()
	select {
	case h.register <- c:
	case <-time.After(testTimeout):
		t.Fatal("timed out registering client")
	}
}

// unregisterTestClient unregisters c from the hub and fails the test if the
// hub's run loop doesn't accept it within testTimeout.
func unregisterTestClient(t *testing.T, h *Hub, c *client) {
	t.Helper()
	select {
	case h.unregister <- c:
	case <-time.After(testTimeout):
		t.Fatal("timed out unregistering client")
	}
}

func TestHubBroadcastDeliversToRegisteredClient(t *testing.T) {
	h := NewHub()
	c := &client{send: make(chan []byte, 1)}
	registerTestClient(t, h, c)

	msg := []byte("hello")
	h.Broadcast(msg)

	select {
	case got := <-c.send:
		if string(got) != string(msg) {
			t.Fatalf("got %q, want %q", got, msg)
		}
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for broadcast message")
	}
}

func TestHubBroadcastFansOutToMultipleClients(t *testing.T) {
	h := NewHub()
	c1 := &client{send: make(chan []byte, 1)}
	c2 := &client{send: make(chan []byte, 1)}
	registerTestClient(t, h, c1)
	registerTestClient(t, h, c2)

	msg := []byte("fanout")
	h.Broadcast(msg)

	for i, c := range []*client{c1, c2} {
		select {
		case got := <-c.send:
			if string(got) != string(msg) {
				t.Fatalf("client %d: got %q, want %q", i, got, msg)
			}
		case <-time.After(testTimeout):
			t.Fatalf("client %d: timed out waiting for broadcast message", i)
		}
	}
}

func TestHubUnregisterStopsFurtherBroadcasts(t *testing.T) {
	h := NewHub()
	c := &client{send: make(chan []byte, 1)}
	registerTestClient(t, h, c)
	unregisterTestClient(t, h, c)

	h.Broadcast([]byte("should not be received"))

	// The hub closes the send channel on unregister, so reading from it
	// should return immediately with ok == false rather than blocking or
	// yielding a broadcast message.
	select {
	case got, ok := <-c.send:
		if ok {
			t.Fatalf("unregistered client received unexpected message: %q", got)
		}
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for closed send channel")
	}
}

func TestHubBroadcastWithNoClientsDoesNotBlockOrPanic(t *testing.T) {
	h := NewHub()

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.Broadcast([]byte("into the void"))
	}()

	select {
	case <-done:
	case <-time.After(testTimeout):
		t.Fatal("Broadcast blocked with zero registered clients")
	}
}

func TestHubUnregisterUnknownClientIsNoOp(t *testing.T) {
	h := NewHub()
	c := &client{send: make(chan []byte, 1)}

	// c was never registered; unregistering it should not panic or block,
	// and should not close a channel that a real client might still hold
	// (nothing to assert on directly here beyond "it doesn't hang").
	unregisterTestClient(t, h, c)
}

func TestNewRegisteredClientReceivesInitialMessageFirst(t *testing.T) {
	h := NewHub()
	c := h.newRegisteredClient([]byte("initial"))

	select {
	case got := <-c.send:
		if string(got) != "initial" {
			t.Fatalf("got %q, want %q", got, "initial")
		}
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for initial message")
	}

	// A later broadcast must arrive strictly after the initial message —
	// this is what lets a reloading UI show correct state immediately
	// instead of waiting for the next unrelated state change.
	h.Broadcast([]byte("later"))
	select {
	case got := <-c.send:
		if string(got) != "later" {
			t.Fatalf("got %q, want %q", got, "later")
		}
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for broadcast message")
	}
}

func TestNewRegisteredClientWithNoInitialMessage(t *testing.T) {
	h := NewHub()
	c := h.newRegisteredClient(nil)

	h.Broadcast([]byte("only message"))
	select {
	case got := <-c.send:
		if string(got) != "only message" {
			t.Fatalf("got %q, want %q", got, "only message")
		}
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for broadcast message")
	}
}

func TestNewRegisteredClientReceivesMultipleInitialMessagesInOrder(t *testing.T) {
	h := NewHub()
	// Multiplexing more than one kind of state (e.g. status + bucket
	// download progress) onto one connection means more than one initial
	// snapshot may need queuing for a newly connecting client — each
	// non-empty one, in order, strictly before any later broadcast.
	c := h.newRegisteredClient([]byte("status-snapshot"), nil, []byte("downloads-snapshot"))

	for _, want := range []string{"status-snapshot", "downloads-snapshot"} {
		select {
		case got := <-c.send:
			if string(got) != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		case <-time.After(testTimeout):
			t.Fatalf("timed out waiting for initial message %q", want)
		}
	}

	h.Broadcast([]byte("later"))
	select {
	case got := <-c.send:
		if string(got) != "later" {
			t.Fatalf("got %q, want %q", got, "later")
		}
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for broadcast message")
	}
}
