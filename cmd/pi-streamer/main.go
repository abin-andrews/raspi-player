// Command pi-streamer is the daemon that runs on the Raspberry Pi: it
// controls mpd and serves the web UI's HTTP+WebSocket API.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"reflect"
	"runtime/debug"
	"time"

	"pi-streamer/internal/api"
	"pi-streamer/internal/bucket"
	"pi-streamer/internal/config"
	"pi-streamer/internal/indexer"
	"pi-streamer/internal/metadata"
	"pi-streamer/internal/mpdclient"
	"pi-streamer/internal/player"
	"pi-streamer/internal/store"
	"pi-streamer/internal/urlcheck"
	"pi-streamer/internal/ws"
)

// safeGo runs fn in a new goroutine, recovering any panic so a bug in a
// background task (status broadcasting, prefetching, favorite archiving)
// logs and stops just that one goroutine instead of crashing the whole
// daemon. net/http already does this for request handlers on its own; a
// goroutine spawned directly with a bare `go` has no such safety net.
func safeGo(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic in %s: %v\n%s", name, r, debug.Stack())
			}
		}()
		fn()
	}()
}

func main() {
	mpdAddr := flag.String("mpd-addr", "127.0.0.1:6600", "address of the mpd server to control")
	httpAddr := flag.String("http-addr", ":8080", "address for the HTTP+WebSocket API to listen on")
	webDir := flag.String("web-dir", "web/dist", "directory of the built frontend to serve at /")
	indexerAddr := flag.String("indexer-addr", "http://127.0.0.1:8081", "address of the search-indexer service")
	configPath := flag.String("config-path", "config.json",
		"path to the daemon's settings file (OLED port/baud, bucket mode/sizes) — editable through the web "+
			"UI, or by hand followed by POST /api/config/reload")
	bucketDir := flag.String("bucket-dir", "bucket-cache",
		"directory for the evictable playback bucket cache (size/mode are config-driven, not flags)")
	favoritesDir := flag.String("favorites-dir", "favorites",
		"directory for permanently archived favorites (size is config-driven, not a flag)")
	bucketStreamAddr := flag.String("bucket-stream-addr", "127.0.0.1:8082",
		"loopback-only address serving cached bucket files to mpd (bucket mode only) — mpd can't be handed a "+
			"raw filesystem path directly (it only permits that over its own Unix socket, which this daemon "+
			"doesn't use), so it fetches from here over plain HTTP instead")
	flag.Parse()

	mpdConn, err := mpdclient.Dial("tcp", *mpdAddr)
	if err != nil {
		log.Fatalf("dial mpd at %s: %v", *mpdAddr, err)
	}
	defer mpdConn.Close()

	cfgStore, err := config.Open(*configPath)
	if err != nil {
		log.Fatalf("open config at %s: %v", *configPath, err)
	}

	// The bucket cache and favorites archive are both optional in the
	// sense that stream mode (the default) never touches them, but the
	// directories/Store themselves always exist so switching into bucket
	// mode or favoriting a track never has to lazily initialize anything.
	cache, err := bucket.Open(*bucketDir, 0, true)
	if err != nil {
		log.Fatalf("open bucket cache at %s: %v", *bucketDir, err)
	}
	favorites, err := bucket.Open(*favoritesDir, 0, false)
	if err != nil {
		log.Fatalf("open favorites archive at %s: %v", *favoritesDir, err)
	}

	idx := indexer.New(*indexerAddr)
	resolver := &modeResolver{
		cfg:           cfgStore,
		checker:       &urlcheck.Checker{},
		cache:         cache,
		streamBaseURL: "http://" + *bucketStreamAddr,
	}
	archiver := &favoriteArchiver{favorites: favorites}
	metadataFetcher := &metadataAdapter{fetcher: &metadata.Fetcher{}}
	p := player.New(mpdConn, store.NewMemoryStore(), idx, resolver, archiver, metadataFetcher)
	hub := ws.NewHub()

	// The OLED display is an optional accessory, configured at runtime
	// (not via flags) through cfg/the web UI's Settings — the daemon runs
	// fine with no Arduino attached, it just skips these updates.
	oled := &oledManager{}
	defer oled.close()
	cfg := &configAdapter{store: cfgStore, oled: oled, cache: cache, favorites: favorites, getStatus: p.Status}
	cfg.apply(cfgStore.Get())
	bucketAPI := &bucketAdapter{cfg: cfgStore, cache: cache, favorites: favorites}

	// wsMessage envelopes every payload multiplexed over the single /ws
	// connection (status, bucket download progress, ...) so one browser
	// WebSocket can carry more than one kind of push update — the frontend
	// dispatches on Type instead of needing a connection per concern.
	// hub.Broadcast itself is payload-agnostic ([]byte in, []byte out), so
	// this envelope is purely a main.go/frontend contract.
	type wsMessage struct {
		Type string `json:"type"`
		Data any    `json:"data"`
	}
	marshalWSMessage := func(msgType string, data any) []byte {
		msg, err := json.Marshal(wsMessage{Type: msgType, Data: data})
		if err != nil {
			log.Printf("marshal %s for broadcast: %v", msgType, err)
			return nil
		}
		return msg
	}

	// broadcastStatus fetches the current mpd status and pushes it to every
	// connected WebSocket client. It's the shared endpoint for both the
	// idle-driven and ticker-driven triggers below.
	broadcastStatus := func() {
		status, err := p.Status()
		if err != nil {
			log.Printf("status for broadcast: %v", err)
			return
		}
		data := marshalWSMessage("status", status)
		if data == nil {
			return
		}
		hub.Broadcast(data)
		updateOLEDTrack(oled, status)
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
		safeGo("mpd status watcher", func() {
			for range watcher.Events() {
				broadcastStatus()
			}
		})
	}

	// 1s ticker: catches progress (elapsed-time) changes during playback,
	// which don't generate idle events. Only broadcasts while playing, to
	// avoid needless traffic/wakeups while paused/stopped. Also triggers
	// prefetching the next queued track once the current one is close to
	// finishing (bucket mode only) — prefetchedFor avoids re-triggering
	// every second once inside that window.
	safeGo("status ticker", func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		prefetchedFor := -1
		for range ticker.C {
			status, err := p.Status()
			if err != nil {
				log.Printf("status for broadcast: %v", err)
				continue
			}
			if status.State != "play" {
				continue
			}
			data := marshalWSMessage("status", status)
			if data == nil {
				continue
			}
			hub.Broadcast(data)
			updateOLEDElapsed(oled, status.Elapsed)

			if status.Duration <= 0 || status.SongID == prefetchedFor {
				continue
			}
			remaining := time.Duration(status.Duration-status.Elapsed) * time.Second
			if remaining <= prefetchWindow {
				prefetchedFor = status.SongID
				songID := status.SongID
				safeGo("prefetch", func() { prefetchNext(p, cache, cfgStore, songID) })
			}
		}
	})

	// Bucket download progress: pushed over the same WebSocket connection
	// as status (see wsMessage above) instead of the frontend polling
	// GET /api/bucket/downloads — a phone-class browser keeping N tabs'
	// worth of 1s HTTP polling loops alive (on top of the status socket)
	// was real, measured overhead. Runs independently of play state (a
	// favorite archive or prefetch can download while nothing's playing)
	// and only broadcasts when the snapshot actually changed from the last
	// one sent, so idle periods with nothing downloading cost nothing.
	safeGo("bucket downloads ticker", func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		var last []api.BucketDownload
		for range ticker.C {
			current := bucketAPI.Downloads()
			if reflect.DeepEqual(current, last) {
				continue
			}
			last = current
			data := marshalWSMessage("downloads", current)
			if data == nil {
				continue
			}
			hub.Broadcast(data)
		}
	})

	// Loopback-only: serves cached bucket files back to mpd (see
	// modeResolver's doc comment on why mpd can't just be handed a
	// filesystem path). Bound explicitly to the address given (default
	// 127.0.0.1, not 0.0.0.0), so it's unreachable from the LAN even if a
	// future change to bucketFileServer's path-validation had a bug — this
	// isn't meant to be part of the app's public HTTP surface at all.
	safeGo("bucket file server", func() {
		if err := http.ListenAndServe(*bucketStreamAddr, &bucketFileServer{cache: cache}); err != nil {
			log.Printf("bucket file server on %s: %v (bucket playback mode won't work)", *bucketStreamAddr, err)
		}
	})

	mux := http.NewServeMux()
	mux.Handle("/api/", api.NewRouter(p, cfg, oled, bucketAPI))
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		// Send the new client its current status and current bucket-
		// download progress immediately, rather than leaving it to wait
		// for the next unrelated state change of each — this is what lets
		// a reloading UI show correct state right away (e.g. mid-download
		// progress already in flight) instead of a stale/blank flash.
		var initialStatus, initialDownloads []byte
		if status, err := p.Status(); err == nil {
			initialStatus = marshalWSMessage("status", status)
		}
		initialDownloads = marshalWSMessage("downloads", bucketAPI.Downloads())
		if err := hub.ServeWS(w, r, initialStatus, initialDownloads); err != nil {
			log.Printf("ws upgrade failed: %v", err)
		}
	})
	mux.Handle("/", http.FileServer(http.Dir(*webDir)))

	log.Printf("pi-streamer listening on %s (mpd at %s)", *httpAddr, *mpdAddr)
	if err := http.ListenAndServe(*httpAddr, mux); err != nil {
		log.Fatal(err)
	}
}
