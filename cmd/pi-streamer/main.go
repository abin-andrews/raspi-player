// Command pi-streamer is the daemon that runs on the Raspberry Pi: it
// controls mpd and serves the web UI's HTTP+WebSocket API.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"time"

	"pi-streamer/internal/api"
	"pi-streamer/internal/indexer"
	"pi-streamer/internal/mpdclient"
	"pi-streamer/internal/player"
	"pi-streamer/internal/store"
	"pi-streamer/internal/ws"
)

func main() {
	mpdAddr := flag.String("mpd-addr", "127.0.0.1:6600", "address of the mpd server to control")
	httpAddr := flag.String("http-addr", ":8080", "address for the HTTP+WebSocket API to listen on")
	webDir := flag.String("web-dir", "web/dist", "directory of the built frontend to serve at /")
	indexerAddr := flag.String("indexer-addr", "http://127.0.0.1:8081", "address of the search-indexer service")
	flag.Parse()

	mpdConn, err := mpdclient.Dial("tcp", *mpdAddr)
	if err != nil {
		log.Fatalf("dial mpd at %s: %v", *mpdAddr, err)
	}
	defer mpdConn.Close()

	idx := indexer.New(*indexerAddr)
	p := player.New(mpdConn, store.NewMemoryStore(), idx)
	hub := ws.NewHub()

	// broadcastStatus fetches the current mpd status and pushes it to every
	// connected WebSocket client. It's the shared endpoint for both the
	// idle-driven and ticker-driven triggers below.
	broadcastStatus := func() {
		status, err := p.Status()
		if err != nil {
			log.Printf("status for broadcast: %v", err)
			return
		}
		data, err := json.Marshal(status)
		if err != nil {
			log.Printf("marshal status for broadcast: %v", err)
			return
		}
		hub.Broadcast(data)
	}

	// Idle-driven: push immediately on player/mixer changes reported by
	// mpd, rather than waiting for the next ticker.
	watcher, err := mpdclient.NewStatusWatcher("tcp", *mpdAddr, "player", "mixer")
	if err != nil {
		// mpd is known reachable at this point (we already dialed it
		// above), so a watcher-specific failure here is a real anomaly.
		// It's not fatal, though: the REST API still works without it,
		// just falling back to the 1s ticker below for pushes.
		log.Printf("mpd status watcher unavailable, falling back to polling only: %v", err)
	} else {
		go func() {
			for range watcher.Events() {
				broadcastStatus()
			}
		}()
	}

	// 1s ticker: catches progress (elapsed-time) changes during playback,
	// which don't generate idle events. Only broadcasts while playing, to
	// avoid needless traffic/wakeups while paused/stopped.
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			status, err := p.Status()
			if err != nil {
				log.Printf("status for broadcast: %v", err)
				continue
			}
			if status.State != "play" {
				continue
			}
			data, err := json.Marshal(status)
			if err != nil {
				log.Printf("marshal status for broadcast: %v", err)
				continue
			}
			hub.Broadcast(data)
		}
	}()

	mux := http.NewServeMux()
	mux.Handle("/api/", api.NewRouter(p))
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		// Send the new client its current status immediately, rather than
		// leaving it to wait for the next unrelated state change — this is
		// what lets a reloading UI show correct state right away instead
		// of a stale/blank flash.
		var initial []byte
		if status, err := p.Status(); err == nil {
			initial, _ = json.Marshal(status)
		}
		if err := hub.ServeWS(w, r, initial); err != nil {
			log.Printf("ws upgrade failed: %v", err)
		}
	})
	mux.Handle("/", http.FileServer(http.Dir(*webDir)))

	log.Printf("pi-streamer listening on %s (mpd at %s)", *httpAddr, *mpdAddr)
	if err := http.ListenAndServe(*httpAddr, mux); err != nil {
		log.Fatal(err)
	}
}
