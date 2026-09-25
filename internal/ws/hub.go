// Package ws implements a WebSocket hub that lets the pi-streamer daemon
// push live playback-state changes (now playing, play/pause, progress) to
// every connected browser client, so multiple open tabs/devices stay in
// sync without polling. Commands still flow over plain HTTP; this package
// only carries the server-to-client push side.
package ws

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// writeWait is the time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// pongWait is the time allowed to read the next pong message from the
	// peer.
	pongWait = 60 * time.Second

	// pingPeriod sends pings to the peer with this period. Must be less
	// than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// maxMessageSize is the maximum message size allowed from a peer.
	maxMessageSize = 512

	// sendBufSize is the buffer size of a client's outbound send channel.
	sendBufSize = 256
)

// client is the hub's internal, transport-agnostic bookkeeping record for a
// connected consumer of broadcasts. It intentionally holds nothing but a
// send channel so that the hub's register/unregister/broadcast logic can be
// exercised in tests via direct channel and method calls, without opening a
// real network socket or websocket connection. The exported Client type
// (which does own a real *websocket.Conn) wraps one of these.
type client struct {
	send chan []byte
}

// Hub maintains the set of active clients and broadcasts messages to them.
// All bookkeeping (the clients map) is owned exclusively by the goroutine
// running Hub.run, which is started by NewHub; register, unregister, and
// broadcast are only ever mutated through the hub's channels, so no
// additional locking is required.
type Hub struct {
	clients    map[*client]bool
	register   chan *client
	unregister chan *client
	broadcast  chan []byte
}

// NewHub creates a Hub and starts its run loop in a background goroutine.
func NewHub() *Hub {
	h := &Hub{
		clients:    make(map[*client]bool),
		register:   make(chan *client),
		unregister: make(chan *client),
		broadcast:  make(chan []byte),
	}
	go h.run()
	return h
}

// run is the hub's single-goroutine event loop. It owns h.clients
// exclusively, so all reads/writes of that map happen here and nowhere
// else.
func (h *Hub) run() {
	for {
		select {
		case c := <-h.register:
			h.clients[c] = true

		case c := <-h.unregister:
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}

		case msg := <-h.broadcast:
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					// The client's buffer is full and not draining fast
					// enough; drop it rather than block the whole hub.
					close(c.send)
					delete(h.clients, c)
				}
			}
		}
	}
}

// Broadcast sends msg to every currently registered client. It never
// blocks on a slow or absent client: with zero clients registered it
// simply hands msg to the hub loop and returns.
func (h *Hub) Broadcast(msg []byte) {
	h.broadcast <- msg
}

// newRegisteredClient creates a client, queues initial onto its send
// channel (if non-empty), and registers it with the hub, in that order —
// so initial is guaranteed to be the first message the client ever
// receives, strictly before any later Broadcast. This lets a newly
// connecting UI (e.g. on page reload) get current state immediately
// instead of waiting for the next unrelated state change to broadcast.
// Queuing happens before registration: the hub's run loop can't reach this
// client via a broadcast until it processes the register message, which
// happens after initial is already sitting in the channel buffer.
func (h *Hub) newRegisteredClient(initial []byte) *client {
	c := &client{send: make(chan []byte, sendBufSize)}
	if len(initial) > 0 {
		c.send <- initial
	}
	h.register <- c
	return c
}

// upgrader upgrades incoming HTTP requests to websocket connections.
// CheckOrigin is permissive (the daemon's web UI may be served from a
// different origin/port on the local network); tighten this if that
// assumption changes.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Client wraps a live websocket connection registered with a Hub.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	c    *client
}

// ServeWS upgrades the HTTP connection to a websocket, registers a new
// Client with the hub, and starts its read/write pumps. initial, if
// non-empty, is sent to this client immediately as its first message —
// callers should pass the current state snapshot so a newly connecting UI
// (e.g. on page reload while something is already playing) renders
// correct state right away instead of waiting for the next broadcast.
// Callers typically wire this up as an http.HandlerFunc, e.g.:
//
//	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
//		status, _ := json.Marshal(currentStatus())
//		if err := hub.ServeWS(w, r, status); err != nil {
//			log.Printf("ws upgrade: %v", err)
//		}
//	})
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request, initial []byte) error {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}

	c := h.newRegisteredClient(initial)
	cl := &Client{hub: h, conn: conn, c: c}

	go cl.writePump()
	go cl.readPump()

	return nil
}

// readPump pumps messages from the websocket connection so disconnects and
// protocol-level pongs are detected. Clients don't need to send anything
// meaningful yet; this just keeps the connection's read side alive and
// unregisters the client the moment the peer goes away.
func (cl *Client) readPump() {
	defer func() {
		cl.hub.unregister <- cl.c
		cl.conn.Close()
	}()

	cl.conn.SetReadLimit(maxMessageSize)
	cl.conn.SetReadDeadline(time.Now().Add(pongWait))
	cl.conn.SetPongHandler(func(string) error {
		cl.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		if _, _, err := cl.conn.ReadMessage(); err != nil {
			break
		}
	}
}

// writePump drains the client's send channel to the websocket connection,
// and periodically pings the peer to keep the connection alive / detect
// dead peers.
func (cl *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		cl.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-cl.c.send:
			cl.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel (client was unregistered).
				cl.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := cl.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			if _, err := w.Write(msg); err != nil {
				w.Close()
				return
			}
			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			cl.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := cl.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
