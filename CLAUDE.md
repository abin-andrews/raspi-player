# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

The Go daemon, search-indexer service, and a real Mantine-based React player UI are all built and wired
together end-to-end, built TDD-style: every `internal/` package was implemented test-first against fakes, with
no real `mpd`/SQLite server needed to run the unit test suite. The player supports playback (URL submission,
play/pause/resume/next/previous, absolute + relative seek), volume/mute, track metadata (artist/album/title/
elapsed/duration/volume) and best-effort album art from mpd, favorites/playlists/history, and full-text search
over previously played/favorited/playlisted URLs (the daemon proxies to `cmd/search-indexer`). A persistent
bottom player bar (`PlayerBar.jsx`) is visible on every tab; live status reaches the frontend over
`internal/ws`'s WebSocket hub (finally fed — see Architecture notes) instead of polling. A previous Google
Drive integration (`internal/drive`) has been removed for now (may return later). `internal/serial` is a new
addition: a generic line-based serial transport for driving an Arduino Uno + SSD1322 OLED
(`arduino/control.ino`) over USB, so the daemon can mirror now-playing state onto a physical display, wired up
end-to-end from `cmd/pi-streamer/oled.go` through a Settings tab in the web UI. `internal/config` is a small
on-disk JSON settings store backing that Settings tab (OLED port/baud, bucket mode/sizes) — the daemon's only
persistent/disk state, deliberately structured so more settings can be added to it later. `internal/urlcheck`
verifies a URL is reachable before `internal/player.PlayURL`/`AddToQueue` ever hand it to mpd. `internal/bucket`
is a newer, larger addition: an on-disk LRU cache of downloaded audio files backing a "download-then-stream"
playback mode as an alternative to streaming a URL directly, plus a separate permanent archive for favorited
tracks — both wired through `cmd/pi-streamer/bucket.go` and a per-track "cached" badge in the web UI. `mpd`
itself only permits `add`-ing a local file over its Unix socket (confirmed by direct testing, not guesswork),
which this daemon doesn't use, so bucket mode hands mpd a URL onto a small second, loopback-only HTTP server
(`bucketFileServer`) instead of a filesystem path — the same proxy-the-bytes pattern the removed Drive
integration used. `internal/player.Player.Queue`/`Status` also reverse-map that server's URLs back to the
original source (`Resolver.Unresolve`) and fill in a readable title (`deriveTitleFromURL`) whenever mpd's own
tag is empty, so the Queue/Now-Playing views never show the daemon's own internal proxy address or a blank
field. `GET /api/library` (`internal/search.Index.List`) exposes the same persistent search index that already
indexes every played/favorited/playlisted URL as a browsable "media library" — no new storage, just a
query-free view of what's already there. The search index now has real `artist`/`album` columns (not folded
into `tags`), populated by a best-effort background metadata fetch (`internal/metadata`, ID3/FLAC/Vorbis tags
read directly from the file) the moment a URL is first added — see Architecture notes. Every URL-accepting
API call also normalizes the URL first (`internal/urlnorm`) so trivially different representations of the
same track collapse onto one dedup key instead of creating duplicate favorites/playlist/history/index
entries. The web UI's tab is synced to `location.hash` (`useHashTab`, no router dependency) so it survives a
reload and browser back/forward. See Architecture notes for how the bucket-mode pieces fit together. Update
this file as the project grows; don't let it drift from reality.

## Purpose

pi-streamer is a network audio player for a Raspberry Pi Zero 2W:

- A daemon runs on the Pi, drives `mpd`/`mpc` to play audio from arbitrary URLs, and outputs sound through
  an attached DAC.
- A web UI lets a user submit a URL to play, control playback (play/pause/etc.), and browse playlists,
  favorites, and play history.
- A separate system (not the Pi) hosts a SQLite (FTS5) database with search indexing over the URL
  collection, since the Pi Zero 2W is too resource-constrained to do fast search itself. It's exposed via
  its own HTTP API (`cmd/search-indexer`: `POST /index`, `GET /search`, `GET /list`, `GET /get`), and the Pi
  daemon calls it over the network via `internal/indexer` (an HTTP client — deliberately not importing
  `internal/search` directly, to keep `modernc.org/sqlite` out of the main daemon binary). The daemon
  best-effort indexes a URL whenever it's played, favorited, or added to a playlist (failures don't fail the
  primary action — indexing is a nice-to-have), first submitting title-less and then enriching it in the
  background with real artist/album tags read directly from the file (`internal/metadata`) — see Architecture
  notes; `GET /api/search` on the daemon proxies to it.

## Stack

- **Daemon**: Go. Runs as a daemon on the Pi, controls `mpd` via the [`gompd`](https://github.com/fhs/gompd)
  client library, and serves the web UI's backend API.
- **Frontend**: React. Talks to the Go daemon over a hybrid API: HTTP for commands (submit URL, playlist/
  favorites CRUD, search) and a WebSocket (or SSE) for pushing live playback state (now-playing, progress,
  play/pause) so the UI stays in sync without polling `mpd`.
- **Playback engine**: `mpd`, controlled via [`gompd`](https://github.com/fhs/gompd) — not reimplemented in-daemon.
- **Search/indexing backend**: SQLite with the FTS5 extension, running on a separate, more capable system
  (not on the Pi itself) — no separate DB server process needed, FTS5 gives fast full-text search
  out of the box. Driver: [`modernc.org/sqlite`](https://gitlab.com/cznic/sqlite) (pure Go, no CGO) —
  deliberately not `mattn/go-sqlite3`, since a CGO driver would break the daemon's ARM cross-compilation
  (no C toolchain is configured for `GOOS=linux GOARCH=arm/arm64` builds).

## Layout

```
cmd/pi-streamer/       daemon entrypoint: wires mpdclient -> player -> api+ws -> http.Server
cmd/search-indexer/    separate service entrypoint: wires search index -> http.Server

internal/mpdclient/    Client interface wrapping gompd; only package allowed to talk to mpd directly.
                        FakeClient (in-memory) lets everything else be unit tested without a real mpd.
internal/store/        Store interface: playlists, favorites, history. In-memory impl for now.
internal/player/       Playback business logic. Depends only on mpdclient.Client + store.Store interfaces.
internal/api/          HTTP command API (net/http, Go 1.22+ method+path patterns). Depends on a
                        Player-shaped interface, not the concrete player.Player, so handlers are testable
                        against a fake.
internal/ws/           WebSocket broadcast hub (gorilla/websocket) pushing live playback state to UI clients.
internal/search/       SQLite/FTS5 search index (modernc.org/sqlite): IndexURL (url, title, artist, album,
                        tags — real columns, not folded into tags), Search (query-driven, FTS5 MATCH), List
                        (every entry, most-recently-indexed first, no query — the "media library" view; MATCH
                        errors on an empty query, so this isn't just Search("")), and Get (exact-match lookup
                        by url, no FTS involved — used for dedup, see Architecture notes). Used by
                        cmd/search-indexer. Open() migrates a pre-artist/album database file in place
                        (FTS5 doesn't support ALTER TABLE ADD COLUMN, unlike ordinary tables — migration
                        renames the old table aside, creates the new 5-column one, copies rows across with
                        empty artist/album, drops the old table).
internal/indexer/      HTTP client the daemon uses to talk to cmd/search-indexer (IndexURL, Search, List,
                        Get). Own json-tagged Result type, independent of internal/search.Result (no sqlite
                        dependency pulled into the main daemon binary). player.Indexer interface wraps it for
                        testability.
internal/urlnorm/      Normalize(url): canonicalizes scheme/host case and strips a default port/empty path
                        before a submitted URL is used as a dedup key anywhere (search index, favorites,
                        playlists, history) — see Architecture notes. Never touches path/query/fragment,
                        since those are frequently case-sensitive server-side and altering them risks
                        breaking playback rather than just deduping it.
internal/metadata/      Fetcher.Fetch(ctx, url): extracts real title/artist/album tags directly from a URL's
                        own file data (github.com/dhowden/tag over a ranged HTTP GET of the first 1MiB) —
                        this is what lets a freshly-added URL show up in the Library with real Artist/Album
                        grouping immediately, rather than only once mpd happens to play it. See Architecture
                        notes for how this plugs into internal/player.
internal/serial/       Generic line-based serial transport (go.bug.st/serial, pure Go/no CGO — same ARM
                        cross-compilation constraint as internal/search's sqlite driver): Open a port at a
                        baud rate, Send a line, get back one reply line, ListPorts to enumerate devices.
                        Knows nothing about any specific device's command vocabulary — see Architecture
                        notes for the OLED protocol it carries.
internal/config/       Small on-disk JSON settings store (OLED port/baud, bucket mode/size limits/safety
                        margin) — Get/Set/Reload. See Architecture notes; this is the app's only
                        persistent/disk state.
internal/urlcheck/      Checker.Check(url) verifies a URL responds successfully (ranged GET, closed right
                        after headers) before player.PlayURL/AddToQueue ever hand it to mpd. See Architecture
                        notes for why.
internal/bucket/        On-disk LRU cache of downloaded audio files, keyed by source URL (Open/Lookup/
                        Contains/Download/Stats/List/Remove). A persisted filename->URL index
                        (.index.json) lives alongside the cached files themselves, since the filename is a
                        one-way content hash — without it List couldn't report which URL each entry is.
                        Two independent instances back two different concepts — the evictable
                        "download-then-stream" playback cache, and a separate, non-evictable permanent
                        favorites archive — both respecting a shared disk-space safety margin. See
                        Architecture notes.

web/                    React (Vite) + Mantine player UI: Now Playing, Queue, Search, Favorites, Playlists,
                        History, Bucket (browse/favorite the playback cache's contents, see below), Library
                        (browse every indexed URL with no query, see internal/search notes), and Settings
                        (OLED + bucket config, see internal/config notes) tabs, plus a persistent PlayerBar
                        (transport/skip/volume) shown on every tab. Icons via @tabler/icons-react. The active
                        tab is synced to location.hash (web/src/hooks/useHashTab.js — no router dependency,
                        no server-side change needed since a hash fragment is never sent to the server), so a
                        reload or browser back/forward doesn't lose your place.
                        web/src/hooks/useDaemonSocket.js is the single WebSocket connection per tab (called
                        once in App.jsx, status/downloads passed down as props) — components needing live
                        status or bucket-download progress must NOT open their own connection or poll; both
                        are pushed over this one socket as {type, data} envelopes (see Architecture notes).
                        App.jsx doesn't render any tab content at all until that hook reports ready — a
                        loading screen shows until then. Tabs.Panel uses keepMounted={false} (App.jsx), so
                        only the active tab's component is ever mounted — see Architecture notes for why this
                        matters a lot more than it sounds like it should.
deploy/                 Deployment assets that don't belong under cmd/ or scripts/: currently just
                        pi-streamer.service.tmpl, the systemd unit template scripts/deploy-pi.sh renders and
                        installs on the Pi. See Commands below.
```

## Architecture notes for future work

- Keep playback logic (talking to mpd via `gompd`) and the HTTP API layer separate in the Go daemon — the
  daemon is the only thing with direct `mpd` access; the frontend never talks to mpd directly. `internal/api`
  depends on the `Player` interface it defines itself (not `internal/player`'s concrete type), so handlers
  stay testable against a fake.
- `internal/ws.Hub` broadcasts playback-state changes to all connected UI clients so multiple browser
  tabs/devices stay in sync without each one polling — important given the Pi Zero 2W's limited resources
  (and the browser's — see the "browser-side watcher/memory" note below). `Hub` itself is payload-agnostic
  (`Broadcast([]byte)`); `cmd/pi-streamer/main.go` multiplexes two kinds of state over the one `/ws`
  connection as `{"type": "status"|"downloads", "data": ...}` envelopes (`wsMessage`,
  `marshalWSMessage(type, data)`) rather than opening a second socket or falling back to HTTP polling for
  the second kind:
  - **status**: fed by two triggers, both funneled through one `broadcastStatus()` closure: an
    `internal/mpdclient.StatusWatcher` (wraps `gompd`'s `idle`-protocol `Watcher` on its own dedicated mpd
    connection) broadcasting immediately on `"player"`/`"mixer"` subsystem changes, and a 1s ticker that
    broadcasts only while `state == "play"` (idle events alone only fire on transitions, not the continuous
    passage of time — needed for a smoothly moving progress bar). This means the *daemon* still polls mpd
    once a second while playing; what's eliminated is every browser tab independently polling the HTTP API.
  - **downloads**: a separate, always-running 1s ticker calls `bucketAdapter.Downloads()` (merges both
    bucket stores' in-flight progress, see below) and broadcasts only when the snapshot differs from the
    last one sent (`reflect.DeepEqual`) — replaced what used to be `GET /api/bucket/downloads` polled from
    every browser tab every second regardless of activity. Runs independently of play state, since a
    favorite archive or prefetch can download while nothing's playing. `bucketAdapter.Downloads()` sorts its
    result by URL before returning — `bucket.Store.DownloadProgress()` ranges over a map, so without
    sorting, two calls with *identical* in-flight downloads could still come back in a different order and
    be (wrongly) treated as "changed," defeating the point.
  `Hub.ServeWS(w, r, initials ...[]byte)` sends a newly connecting client one snapshot of each kind
  immediately (queued before registration, in order, so they're guaranteed to arrive before any later
  broadcast of either kind) rather than leaving it to wait for the next unrelated state change of each —
  `web/src/hooks/useDaemonSocket.js` gates on receiving the first `"status"` message (`ready`) before
  `App.jsx` renders the real UI at all, so a page reload while something's playing (or downloading) never
  shows a stale/default flash that then jumps to correct values.
- **`GompdClient` has a `sync.Mutex`** guarding every method — required once a background broadcast loop
  started calling `Status()` concurrently with HTTP-handler-triggered command calls on the same single mpd
  TCP connection. Any new method added to `GompdClient` must lock it too, or concurrent access will corrupt
  mpd's text protocol exchange.
- **Next/Previous operate on mpd's queue as-is** — a deliberate choice, not an oversight: `PlayURL` still just
  appends to and resumes mpd's existing queue (see `PlayURL` above), so repeated "Play" clicks silently
  accumulate multiple tracks in the background queue rather than each one reliably jumping to that exact
  track. Next/Previous are thin wrappers over mpd's own `Next()`/`Previous()` over whatever that queue happens
  to contain. Fixing "Play" to clear-and-jump (and adding a "Play all" for playlists so Next/Previous have a
  well-defined queue to navigate) was considered and explicitly declined — revisit only if asked.
- **Two distinct "playlist" concepts — don't conflate them**: the **Queue** tab (`web/src/components/Queue.jsx`,
  backed by `mpdclient.QueueTrack`/`Queue()`/etc.) is mpd's actual live playback queue — what Next/Previous
  navigate, orderable/removable via mpd's own `Id`-based `move`/`delete`. The **Playlists** tab
  (`internal/store`) is the app's own saved, named lists — unrelated to mpd's queue, not currently loadable
  into it (no "play this saved playlist" button exists yet; each track's own Play button still just
  appends-and-resumes per the point above). `Status.SongID` (mpd's `songid`) is what lets the Queue view
  reliably highlight the currently-playing row — compare `QueueTrack.ID`, not title/URL, since `Status.Song`
  prefers Title over file and won't always match a queue row's `URL` field directly.
- Play tracking (last played, favorites, history) is Pi-local state (`internal/store`) distinct from the URL
  search index (`internal/search`), which lives on the separate indexing system (`cmd/search-indexer`) and is
  reached via `internal/indexer`'s HTTP client. `player.New`'s third argument (`Indexer`) may be `nil` — guard
  every use; `main.go` always constructs one from `-indexer-addr` (default `http://127.0.0.1:8081`), but the
  service itself isn't started by `make run` — run `make run-indexer` separately for search to actually work.
  Its fourth (`Resolver`) and fifth (`FavoriteArchiver`) arguments may also be `nil` — see the URL pre-flight
  check and bucket cache notes below.
- `internal/api`'s HTTP surface beyond the original CRUD: `POST /api/seek {seconds}` (absolute),
  `POST /api/seek/relative {seconds}` (skip ±N, positive or negative), `POST /api/next`, `POST /api/previous`,
  `POST /api/volume {volume}` (clamped 0-100 in `internal/player`), `GET /api/search?q=&limit=`,
  `GET /api/library?limit=&offset=` (every indexed entry, no query — the Library tab; `Player.Library`
  errors the same way `Search` does if no indexer is configured),
  `GET /api/albumart?url=` (raw image bytes, `Content-Type` via `http.DetectContentType`; any error or no-art
  returns a plain `404`, not a JSON error body — art-not-found is the common case for radio streams, and the
  frontend's `<img onError>` fallback is the only path it needs), and the mpd-queue routes:
  `GET /api/queue`, `POST /api/queue {url}` (adds without playing — distinct from `POST /api/play`),
  `DELETE /api/queue/{id}`, `POST /api/queue/{id}/move {position}`, `POST /api/queue/{id}/play`,
  `DELETE /api/queue` (clear all). There's no backend "mute" — the frontend
  remembers the last non-zero volume locally and toggles `POST /api/volume {0}` / restore, purely client-side.
  Settings routes (see `internal/config`/OLED notes below): `GET`/`PUT /api/config` (the whole settings
  object; `PUT` persists and applies live), `POST /api/config/reload` (re-read the file from disk),
  `GET /api/oled/status`, `GET /api/oled/ports` (serial port auto-detection for the UI's dropdown),
  `GET /api/oled/bauds`, `GET /api/bucket/status` (usage of both bucket stores, see below),
  `POST /api/bucket/query {urls: [...]}` (batched "is this cached?" lookup for a list's per-track badges — one
  request per rendered list, not one per row), `GET /api/bucket/list` (the playback cache's actual contents,
  for the Bucket tab), and `GET /api/bucket/downloads` (in-flight download progress, polled by the frontend).
- **Google Drive integration removed** (was `internal/drive`, plus a Drive tab in the frontend and
  `-public-base-url`/`-drive-config-path`/`-drive-redirect-url` flags/`DRIVE_CLIENT_ID`/`DRIVE_CLIENT_SECRET`
  env vars in `main.go`): pulled out for now to drop `google.golang.org/api` (+ its gRPC/OpenTelemetry
  transitive deps), which had roughly **doubled** the daemon's ARM binary size (~9.8MB → ~21MB) for a feature
  most deployments don't use. May come back later behind a lighter-weight direct-REST-call client instead of
  the generated one. Its removal briefly left the daemon with no persistent/disk state at all; `internal/config`
  (below) has since reintroduced a small one, for OLED settings.
- **URL resolution & pre-flight check** (`internal/urlcheck`, `player.Resolver`): `player.PlayURL`/`AddToQueue`
  call `Resolver.Resolve(url)` to get the URI mpd is actually given, *before* calling `mpd.Add`/`Play` — the
  *original* `url` (not whatever Resolve returns) is still what's recorded in history/favorites/the search
  index; only mpd sees the resolved value. Motivation: mpd has no way to report "the remote server never
  responded" back through `Status()` — a bad/dead URL could otherwise leave mpd stuck or silently still playing
  whatever was already loaded, while the OLED (and WebSocket clients) stayed on stale data since nothing
  changed to trigger a status broadcast. Resolving/checking first means a bad URL never reaches mpd at all —
  the HTTP handler returns an error immediately, and mpd's (and the OLED's) state genuinely hasn't changed,
  which is the *correct* behavior rather than a symptom of a bug. `player.Resolver` is nil-able (skips
  resolution/passes the URL through as-is, mainly for tests) the same way `Indexer` is. The real
  implementation, `cmd/pi-streamer/bucket.go`'s `modeResolver`, is mode-aware — see below.
- **Bucket cache / "download-then-stream" playback mode** (`internal/bucket`, `cmd/pi-streamer/bucket.go`):
  two independent `bucket.Store` instances, both content-addressed (SHA-256 of the URL) directories of
  downloaded audio files. (1) The evictable **playback cache** — when `config.Bucket.Mode` is `"bucket"`
  (vs. the default `"stream"`), `modeResolver.Resolve` downloads a URL into this cache (or reuses an existing
  `Lookup` hit) instead of doing `urlcheck`'s cheap reachability check. **mpd is never handed a raw filesystem
  path** — confirmed by direct testing that mpd only permits `add`-ing local files (bypassing
  `music_directory`) to clients connected via its own Unix domain socket, flatly refusing `file://<path>`
  (and even a bare absolute path) over TCP with `Access denied`, which is how this daemon always talks to mpd
  (`mpdclient.Dial("tcp", ...)`, both in dev and on the Pi — see the dev-workflow note below). Instead,
  `bucketFileServer` (a second, separate `http.Server`, bound explicitly to `-bucket-stream-addr`'s loopback
  address — **not** part of the main API's `mux`, since it's for mpd only, never browsers/the LAN) serves the
  cached file's bytes back out (`http.ServeFile`, which handles `Range` requests for seeking and Content-Type
  itself), keyed by `bucket.Store.FilePath(name)` — validated against the store's own URL index so a crafted
  request can't escape the cache directory or reach a file the store didn't itself download. `modeResolver`
  hands mpd `http://<bucket-stream-addr>/<cache filename>` instead — an ordinary HTTP URL mpd streams the same
  way it would any other, exactly the pattern the (removed) Google Drive integration used for the same class
  of problem (mpd can't fetch something on its own, so the daemon proxies the bytes itself). Also fixed:
  `bucket.Open` resolves its directory to an absolute path up front (`filepath.Abs`) — a *relative* bucket dir
  meant mpd (which treats any URI without a `://` scheme as relative to *its own* `music_directory`, not the
  daemon's working directory) failed immediately with `No such directory`, before the permission issue above
  even came up; `TestOpenResolvesRelativeDirToAbsolute` guards this specifically, since every other test uses
  `t.TempDir()`, which is already absolute and couldn't have caught it. Least-recently-*played* entries
  (tracked via the file's mtime, bumped on `Lookup`, not on the read-only `Contains` used for UI badges) are
  evicted to stay within `Bucket.MaxSizeMB`. (2) The
  **favorites archive** — `favoriteArchiver.Archive` fires in a background goroutine from
  `player.AddFavorite` (via `player.FavoriteArchiver`, also nil-able) regardless of playback mode, downloading
  into a *separate*, non-evictable store capped by its own `Bucket.FavoritesMaxSizeMB`: "permanent" means it's
  never auto-deleted to make room for something else, not that it's unbounded — once full, saving a *new*
  favorite's audio just fails (logged, not surfaced to the caller) rather than evicting an existing one.
  **Shared safety margin**: `Bucket.MinFreeMB` bounds *both* stores independently via `golang.org/x/sys/unix.
  Statfs` on the store's directory (real disk free space, not just each store's own size accounting) — the
  evictable cache will evict its own entries harder to protect the margin if needed; the non-evictable
  favorites store simply refuses the download, same as hitting its own cap. All three size fields are
  zero-defaultable (`config.DefaultBucketMaxSizeMB`/`DefaultFavoritesMaxSizeMB`/`DefaultMinFreeMB`, applied by
  `configAdapter.applyBucket`, mirroring `applyOLED`'s baud-default pattern) and validated non-negative by
  `internal/api`'s `validateBucket` before `Set` ever persists them. **Prefetching**: the existing 1s
  playing-ticker in `main.go` also triggers `prefetchNext` once the current track has ≤15s left
  (`prefetchWindow`), downloading the *next* mpd-queue entry (found via matching `Status.SongID` in
  `Player.Queue()`, +1 position) into the playback cache ahead of time — bucket mode only, a no-op otherwise;
  `prefetchedFor` (a closure-local `SongID`) stops it from re-triggering every second within that window.
  Entirely best-effort: any failure (unreachable, disk full, nothing next queued) is logged and dropped,
  never affecting the *currently*-playing track. **Un-favoriting**: `player.RemoveFavorite` calls
  `FavoriteArchiver.Forget` (the interface's other method, alongside `Archive`), which just calls the
  favorites `Store`'s `Remove(url)` — deletes the cached file if present and drops its entry from the URL
  index; not an error if it was never archived in the first place (e.g. the original `Archive` download
  failed). **Browsing**: `bucket.Store.List()` returns every entry in a store (URL/size/last-accessed, most-
  recent-first) using the same persisted URL index that makes `Forget`'s cleanup targeted rather than
  hash-matching-blind; `bucketAdapter.List` (implementing `api.Bucket`) exposes only the *playback* cache this
  way via `GET /api/bucket/list` — the favorites archive doesn't need its own equivalent since it's already
  browsable through `GET /api/favorites`/`internal/store` (keyed by title, not the bucket's raw file entries).
  **UI**: the Settings tab's "Audio Bucket" section edits mode/sizes and shows live usage
  (`GET /api/bucket/status`); a "Cached" / "Saved offline" `Badge` on Queue/Favorites rows
  (`web/src/hooks/useCachedUrls.js`, batched via `POST /api/bucket/query` — one request per rendered list, not
  per row) reports a URL as cached if *either* store has it, matching the UI's "is this available locally"
  framing rather than which specific store answered yes. The **Bucket tab** (`web/src/components/Bucket.jsx`)
  lists the playback cache's actual contents and lets you favorite/unfavorite a cached entry directly (reusing
  the existing `addFavorite`/`removeFavorite` calls — no bucket-specific favorite API), with a heart icon
  toggled by cross-referencing `GET /api/favorites` client-side.
  **`player.Resolver.Unresolve` — mpd only ever sees the resolved URI, never the original**: confirmed by
  direct testing (see below) that a bucket-mode-resolved local file-server URL, once handed to mpd, becomes
  what mpd itself reports back through `Queue()`/`Status()` — so without reversing it, the Queue tab would show
  the daemon's own internal proxy address instead of the real track, and worse, `prefetchNext` (which finds
  "the next URL" via `Player.Queue()`) would treat that proxy address as a legitimate source and re-download
  its own served copy under a nonsense cache key. `modeResolver.Unresolve(uri)` strips `streamBaseURL` and
  looks the remaining filename up via `bucket.Store.URLForFilename` (the same persisted index `List`/`Forget`
  use) to recover the original URL; `Player.Queue`/`Status` call it on every mpd-sourced URL field before
  returning. A `Resolver` that never rewrites URLs (stream mode) just returns the URI unchanged.
  **Download progress**: `bucket.Store` tracks each in-flight `Download` (bytes received via a counting
  `io.Reader` wrapper, total from the response's `Content-Length` if the origin sent one) in a small
  mutex-guarded map, exposed via `DownloadProgress()`; `bucketAdapter.Downloads` (implementing `api.Bucket`)
  merges both stores' in-flight downloads, sorted by URL. Covers on-demand bucket-mode plays, background
  prefetching, and favorite archiving uniformly, since all three go through the same `Download`.
  `GET /api/bucket/downloads` still exists (harmless to call, e.g. for a one-off check), but the frontend no
  longer polls it — `web/src/hooks/useDaemonSocket.js` receives it pushed over `/ws` as a `"downloads"`
  envelope instead (see the `internal/ws.Hub` note above); shown both as a compact header badge (any tab)
  and detailed per-file progress bars on the Bucket tab.
  **Verified empirically, not by inspection** (both the mpd-permission finding above and this fix): real ID3
  tags (Title/Artist/Album) DO survive byte-for-byte through the local file server with no extra work needed —
  mpd reads them from the served copy exactly as it would from the original. Confirmed using a fully isolated,
  throwaway mpd instance (separate port, own config, `null` audio output) — **never the system's real mpd** —
  after two earlier incidents this session where ad hoc `mpc`/API testing against the live instance corrupted
  real queue state. Always use an isolated instance for anything beyond a read-only check.
- **Queue/Status title fallback** (`internal/player/title.go`'s `deriveTitleFromURL`): mpd's own `Title` tag is
  simply empty for a track it hasn't (or can't) read tags for — `Player.Queue`/`Status` were otherwise passing
  that "as mpd reports it" straight through, showing a blank field or the bare URL. `deriveTitleFromURL` builds
  a readable name from the URL's last path segment (percent-decoded, extension stripped, `-`/`_` → spaces —
  e.g. `.../02-Chandamaama%20%28D%29.mp3` → `"02 Chandamaama (D)"`) and is only ever applied when mpd's own
  Title is empty, so it never overrides real tag data. Same idempotent-for-plain-text design as
  `oledSanitize`/`oledTruncate` below — safe to run on a value that might already be a real title.
- **Media library** (`internal/search.Index.List`, `GET /api/library`, the Library tab): "remembers every URL
  added, referred at any point in future" turned out to need **no new persistence** — `internal/search`'s
  SQLite index already durably indexes every played/favorited/playlisted URL (`player.index`'s best-effort
  calls, pre-existing). The gap was purely presentational: `Search` requires an FTS5 `MATCH` query (which
  errors on `""`, so it can't be reused for "show everything"), so there was no way to just browse the
  collection. `List(limit, offset)` (`ORDER BY rowid DESC` — SQLite's own insertion order, newest first) fills
  that gap; `internal/indexer.Client.List` and `player.Player.Library` mirror `Search`'s shape exactly
  (same nil-indexer error, same limit semantics), and the Library tab paginates via a "Load more" button.
- **URL dedup and add-time metadata enrichment** (`internal/urlnorm`, `internal/metadata`,
  `internal/player.Player.index`): every URL-accepting entry point (`PlayURL`, `AddToQueue`, `AddFavorite`,
  `RemoveFavorite`, `AddToPlaylist`) normalizes the URL first (`urlnorm.Normalize`: lowercases scheme/host,
  strips a default port, fills in an empty path as `/` — deliberately *not* touching path/query/fragment,
  since those are often case-sensitive server-side and a query string can carry an auth token) so trivially
  different representations of the same track (`HTTP://Example.com:80/x.mp3` vs `http://example.com/x.mp3`)
  collapse onto the same favorite/playlist/history/index entry instead of creating a duplicate; an empty or
  unparseable URL is now rejected up front with a clear error rather than reaching mpd or the store at all.
  Separately, `Player.index` (the best-effort search-indexing hook shared by all four entry points) now calls
  `Indexer.Get(url)` first: if the URL is already indexed with a real artist/album, that entry is left alone
  (no re-index, no re-fetch — this is the "if it exists, reuse it" half); if it's brand new, a thin
  `IndexURL` call goes out synchronously (so the URL shows up in the Library immediately, even before any
  tags are known) and, if a `MetadataFetcher` is configured, a background goroutine (`enrichMetadata`, own
  panic recovery — same reasoning as `safeGo` below, since nothing else in this call chain would catch a
  panic in a detached goroutine) fetches real tags and re-indexes with them once they're in. Real
  `MetadataFetcher`: `internal/metadata.Fetcher`, which does a ranged GET of a URL's first 1MiB and parses
  embedded ID3/FLAC/Vorbis tags via `github.com/dhowden/tag` — this is what gives a URL real Artist/Album
  grouping in the Library as soon as it's added, not just after mpd happens to play it and report decoded
  tags back (a stream with no embedded tags, e.g. most internet radio, just keeps the existing
  `deriveTitleFromURL` fallback — `MetadataFetcher.Fetch`'s `ok=false` return means "nothing more to add",
  not an error). `internal/search.Index.Get`/`internal/indexer.Client.Get` (`GET /get?url=`, 404 on a miss)
  back this end to end; both `IndexURL` and these entry points' whole flow are exercised against a fake
  `Indexer`/`MetadataFetcher` in `internal/player/player_test.go` (no real search-indexer service needed).
- **Hash-based tab routing** (`web/src/hooks/useHashTab.js`): the active `Tabs` value is synced to
  `location.hash` (e.g. `#/queue`) instead of local-only `useState`, so a reload or browser back/forward keeps
  your place. Deliberately hash-based, not real paths with a router library: a hash fragment is never sent to
  the server, so `cmd/pi-streamer`'s static `http.FileServer` needs no "SPA fallback" route change for a hard
  refresh on a given tab to keep working — real paths would have needed exactly that.
- **Browser-side memory/CPU on a low-power device (Android especially)** — three compounding bugs, all fixed
  together: (1) Mantine's `Tabs` keeps every panel's children mounted forever by default (just hidden via
  CSS), so all nine tabs' components — each with their own effects, several with their own polling — were
  running *simultaneously* at all times regardless of which tab was actually visible. Fixed with
  `keepMounted={false}` on `App.jsx`'s `<Tabs>`: now only the active tab's component is ever mounted, and
  switching away tears it down (effects/timers and all) instead of leaving it running in the background.
  (2) `Bucket.jsx` had its *own* `useBucketDownloads()` poll in addition to the one already running in
  `App.jsx` for the header badge — two independent 1s HTTP-polling loops hitting the same endpoint at all
  times, compounding with (1) since `Bucket` was always mounted too. Fixed by passing `downloads` down from
  `App.jsx` as a prop instead (and, since then, superseded entirely by the WebSocket push described in the
  `internal/ws.Hub` note above — there's no polling left at all for this). (3) Even with (1)/(2) fixed, the
  once-a-second status tick re-renders `App`, which by default re-renders *every* child regardless of
  whether that child actually consumes `status` — wasteful for the seven tabs that don't. `Search`,
  `Favorites`, `Playlists`, `History`, `Bucket`, `Library`, and `Settings` are all wrapped in `React.memo` for
  this reason (`NowPlaying`/`Queue`/`PlayerBar` aren't — they genuinely need to re-render on every status
  tick, and `status` is a fresh object every message anyway, so memoizing them would be a no-op). Together,
  the app should now render close to nothing when idle (nothing playing, nothing downloading) instead of
  nine components' worth of background activity at all times — this is what "the UI broke" on a phone
  browser was actually pointing at, not any single obvious bug.
- **Panics in background goroutines used to crash the whole daemon** — `net/http` recovers a panic inside a
  request handler automatically (logs it, closes that connection, keeps running), but a bare `go func(){...}`
  spawned directly (prefetching, favorite archiving, the status ticker, the mpd idle watcher, the bucket file
  server) has no such safety net; an unrecovered panic there takes the entire process down. `cmd/pi-streamer`'s
  `safeGo(name, fn)` wraps every one of these with a `recover` that logs (`panic in %s: %v\n%s` plus a stack
  trace) instead of exiting — used everywhere a bare `go` used to appear in `main.go`/`bucket.go`.
- **OLED line-length backstop** (`cmd/pi-streamer/oled.go`): `arduino/control.ino` discards an oversized
  command *entirely* rather than gracefully shortening it (replying `ERR`, leaving that field unchanged) — a
  fallback title built from a full URL (see the title-fallback note above) routinely exceeds `TITLE_CAP`/
  `RX_CAP`. `updateOLEDTrack` caps Title/Artist/Album to `TITLE_CAP-1`/`TEXT_CAP-1` chars
  (`oledTruncate`, rune-safe) before sending; `oledManager.send` *also* checks the fully-assembled line against
  the wire limit (`truncateOLEDLine`) as a backstop catching any value — current or future field — that's still
  too long, not just these three.
- **Arduino OLED display** (`arduino/control.ino`, driven by `internal/serial`): a Uno + SSD1322 256x64 SPI
  display attached over USB, meant to mirror now-playing state (title/artist/album/elapsed/duration/state)
  physically. `internal/serial.Client` is a generic transport — `Open(port, baud)`, then `Send(line) (reply
  string, err error)` — chunking each write into 12-byte pieces with a 40ms gap (mirrors `arduino/test.py`;
  a classic Uno's small hardware RX buffer can't be trusted with a burst write) and polling reads with a
  short per-read timeout so a silent device times out in ~3s instead of blocking forever (a timed-out
  `go.bug.st/serial` `Read` returns `(0, nil)`, not an error). The OLED's own command vocabulary
  (`BEGIN`/`TITLE`/`ARTIST`/`ALBUM`/`DURATION`/`TIME`/`STATE`/`END`, one per line, each acknowledged with
  `OK`/`ERR`) is deliberately **not** modeled in `internal/serial` — that protocol knowledge (plus the
  live-reconnectable `oledManager` and its `sync.Mutex`-guarded `*serial.Client`) lives in
  `cmd/pi-streamer/oled.go`. `go.bug.st/serial` is pure Go (no CGO), verified to cross-compile clean for both
  `GOOS=linux GOARCH=arm` and `GOARCH=arm64`, same rationale as `modernc.org/sqlite` above.
- **`internal/config`**: a small on-disk JSON settings store (`config.Store`), reintroducing persistent state
  after the Drive removal above — currently holds just `OLED.Port`/`OLED.Baud`, but `Config` is structured so
  more settings can be added later without a new mechanism. `Store.Get`/`Set`/`Reload` are the whole API;
  `Set` persists to disk, `Reload` re-reads the file (picking up a hand-edit made directly on the Pi, e.g. over
  SSH) — neither is wired to *do* anything by itself. `cmd/pi-streamer/oled.go`'s `configAdapter` is what
  bridges `Store` to `internal/api.Config` and gives `Set`/`Reload` their live effect, by calling
  `oledManager.reconfigure` with the new port/baud on every change (an empty port disconnects). This is the
  **web UI's actual mechanism for configuring the OLED display**: `web/src/components/Settings.jsx` (the
  Settings tab) calls `GET/PUT /api/config` and `POST /api/config/reload`, plus `GET /api/oled/status` and
  `GET /api/oled/ports` (serial port auto-detection via `internal/serial.ListPorts`, wrapping
  `go.bug.st/serial.GetPortsList`) so the user picks a port from a dropdown rather than typing a device path.
  There are deliberately no separate connect/disconnect endpoints — connecting *is* the side effect of setting
  `oled.port` in config. Default path: `-config-path` (default `config.json`, written `0600`, gitignored).
- The Pi Zero 2W is resource-constrained: favor a lightweight daemon and frontend build. `cmd/pi-streamer`
  serves `web/dist` as static files at `/` (via the `-web-dir` flag, default `"web/dist"`) alongside `/api`
  and `/ws`, so `make web-build && make run` serves the whole app from one process. Not yet done: embedding
  `web/dist` into the Go binary (`go:embed`) for a single deployable artifact on the Pi — right now it needs
  `web/dist` present relative to wherever the binary runs.
- **Development workflow is cross-platform**: write and test the daemon on x86 (against a local `mpd`
  instance), then cross-compile to ARM for deployment to the Pi. Since `gompd` only talks to `mpd` over its
  TCP protocol, the daemon itself has no ARM/Pi-specific dependencies — an x86 `mpd` install is enough to
  exercise playback logic locally before shipping to the Pi. Verified: `make build-pi`/`build-pi64` both
  cross-compile clean with no CGO errors.
- Confirm which Raspberry Pi OS the Pi Zero 2W is running (32-bit `armhf` vs. 64-bit `arm64`) before deploying
  — Pi Zero 2W's Cortex-A53 supports both, and Raspberry Pi OS still defaults some images to 32-bit.
- **mpd needs an explicit `audio_output` block to make sound** — a fresh `mpd.conf` has none, so mpd
  auto-detects an output (often JACK) that isn't actually running, and playback silently "succeeds" (state
  shows `play`, no error surfaced to the API) with no audio. Fix: add an `audio_output { type "alsa"; device
  "hw:X,Y"; }` block to `/etc/mpd.conf` (find your card via `aplay -l`) and `sudo systemctl restart mpd`.
  Point at a raw ALSA device (`hw:X,Y`), not `device "default"` — on a system-level mpd service, `"default"`
  routes through the desktop user's PulseAudio/PipeWire session, which the service user can't reach. This is
  also the right target shape for the eventual Pi+DAC setup (raw ALSA to the DAC's card), not a dev-only
  workaround. `speaker-test -D hw:X,Y -c 2 -t wav -l 1` is the fastest way to test a card independent of mpd.

## Commands

All commands below are `make` targets (see `Makefile`); run `make help` for the full list.

```sh
# Common (architecture-independent)
make install-deps      # Debian/Ubuntu: installs Go toolchain + mpd/mpc (needs sudo)
make test              # go test ./... — unit tests only, no real mpd/SQLite server needed
make fmt               # go fmt ./...
make vet               # go vet ./...
make lint              # fmt + vet
make clean             # remove bin/

# x86 dev machine
make build              # native daemon binary -> bin/pi-streamer
make run                # build + run the daemon locally (needs a local mpd — see install-deps)
make build-indexer      # native search-indexer binary -> bin/search-indexer
make run-indexer        # build + run the search-indexer locally

# Raspberry Pi Zero 2W (ARM) — cross-compiled from x86, not built on-device
make build-pi            # GOOS=linux GOARCH=arm GOARM=6 -> bin/pi-streamer-arm (32-bit Raspberry Pi OS)
make build-pi64          # GOOS=linux GOARCH=arm64       -> bin/pi-streamer-arm64 (64-bit Raspberry Pi OS)
make build-all           # build + build-pi + build-pi64
make deploy-pi           # build-pi + web-build, ships both + installs/starts a systemd service on PI_HOST
make deploy-pi64         # same, for the arm64 binary
make install-deps-pi     # one-time: apt-get install mpd/mpc on PI_HOST over SSH (audio_output still manual)

# Frontend (web/, React + Vite)
make web-install        # npm --prefix web install
make web-build           # npm --prefix web run build
make web-test            # npm --prefix web run test --if-present
```

To run a single Go test: `go test ./internal/player/ -run TestPauseAndResume -v` (same pattern for any
package). `GOARM=6` targets the Pi Zero's ARMv6 baseline; Zero 2W's Cortex-A53 also supports ARMv7/64-bit
if the deployed OS image turns out to be 64-bit — override with `GOARM=7` or use `make build-pi64` instead.

`cmd/pi-streamer` flags beyond the earlier ones: `-config-path` (default `config.json`), `-bucket-dir`
(default `bucket-cache`), `-favorites-dir` (default `favorites`), `-bucket-stream-addr` (default
`127.0.0.1:8082` — the loopback-only file server mpd fetches cached bucket files from, see the bucket cache
architecture note) — these are just *where on disk*/*what address* things live; none of the OLED port/baud or
bucket mode/size/margin settings are flags at all, since they're
configured live through the web UI's Settings tab (or by hand-editing `config.json` and calling
`POST /api/config/reload`), not at startup, so no daemon restart is needed to plug in a display or switch
playback modes. The daemon runs fine with no OLED section configured (display integration simply skipped) and
defaults to stream mode if bucket settings are unconfigured. `make run` auto-loads a `.env` file from the repo
root if present, for any future secrets — none are currently needed (`.env` is gitignored; never commit real
credentials).

**Deploying to a real Pi** (`make deploy-pi`/`deploy-pi64`, `PI_HOST`/`PI_PATH` override the
`pi@raspberrypi.local`/`~/pi-streamer` defaults): builds the ARM binary + frontend, then runs
`scripts/deploy-pi.sh`, which `scp`s both to the Pi, renders `deploy/pi-streamer.service.tmpl` (substituting
the resolved remote path/user), and installs it as a systemd service (`sudo systemctl enable --now
pi-streamer`) — so it survives reboots and restarts on crash (`Restart=on-failure`). Deliberately **not**
scripted: mpd's own install (`make install-deps-pi` does the `apt-get` half) and its `audio_output` block
(hardware-specific — needs `aplay -l` on the Pi itself, see the mpd note above), and copying any local
`config.json`/`bucket-cache/`/`favorites/` — the OLED port name (and, now, any locally-cached audio) is
specific to what's physically plugged into/downloaded on the Pi, so it's left to create its own fresh via the
Settings tab rather than inheriting the dev machine's. `web/dist` lands at
`<PI_PATH>/web/dist`, matching `-web-dir`'s default (relative to the systemd unit's `WorkingDirectory`), so no
extra flag is needed for the frontend to be found. If `search-indexer` runs on a separate host (the intended
setup), pass `-indexer-addr` to `ExecStart` in the installed unit accordingly — the default
`http://127.0.0.1:8081` only works if it happens to run on the Pi too.
`scripts/deploy-pi.sh` opens one multiplexed SSH connection (`ControlMaster`/`ControlPath`/`ControlPersist`)
and reuses it for every `ssh`/`scp` call — without key-based auth set up, that means one password prompt for
the whole deploy instead of one per command; the control socket is torn down (`ssh -O exit`) on exit via a
trap, including on failure.
