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
entries. The library is now fully manageable, not just browsable: `POST /api/library` upserts an entry by
URL (add a new one, or edit an existing one by resubmitting the same URL with changed fields — optionally
triggering the same synchronous metadata auto-fetch as above if title/artist/album are all left blank) and
`DELETE /api/library` removes one from the search index only (favorites/history/queue/bucket are untouched).
The web UI has been consolidated from 9 tabs down to 2 real tabs plus a settings screen — **Library**
(find/select/manage everything: tracks/albums/artists/playlists/favorites/recently-played, search, edit,
delete — see Architecture notes) and **Queue** (mpd's live playback ordering) as `Tabs.Tab`s centered in
the header, and **Settings** (config + the old Bucket tab's cache browsing) as a full-screen `Modal`
opened from a cog `ActionIcon` at the header's top right rather than a third tab — see Architecture notes
for why. The old standalone Search and Now Playing tabs are gone (Search merged into Library; Now
Playing's one unique job — paste a URL, play it immediately — briefly moved into an always-visible
quick-action bar in Library, then that too was removed outright, since `PlayerBar` already shows
current-track info everywhere; adding a brand-new URL now happens via the Queue tab's own URL input or by
playing/favoriting/playlisting one, same as it always could). The web UI's tab is still synced to
`location.hash` (`useHashTab`, no router dependency) so it survives a reload
and browser back/forward. Album art itself is never held in the Go process's memory (a 512MB Pi Zero 2W
constraint) — `internal/artstore` resolves it once (mpd first, falling back to `internal/coverart`'s
MusicBrainz + Cover Art Archive lookup when mpd has nothing and artist/album are known) and persists it to
disk, served back out by a genuine `http.FileServer` mount rather than any application code on repeat
requests; a per-track "Re-fetch art" action (`POST /api/albumart/refresh`) forces a re-resolve on demand,
and a Settings-tab "warm" button pre-resolves the whole library as a background job. `internal/jobs` is a
new, deliberately generic in-memory job tracker (currently used only by that warm scan) whose progress is
pushed to the frontend over the same WebSocket as a third envelope type, `"jobs"`, alongside the existing
`"status"`/`"downloads"`. The Library tab's Tracks view uses real Previous/Next pagination rather than an
accumulating "Load more" list, and editing an existing entry opens in a `Modal` (add still uses an inline
`Collapse`) so the edit form is visible regardless of how far down a paginated list the row being edited
was — the edit form also has a "Look up title/artist/album" action (`internal/coverart.Fetcher.
SearchMetadata`, the same MusicBrainz client the album art fallback uses) offering candidate metadata
matches as clickable suggestions, never applied automatically. `internal/ytdlp` is a new addition: a
submitted YouTube URL (`youtube.com`/`youtu.be`) is detected and its audio-only stream extracted via the
`yt-dlp` CLI, then handled exactly like any other bucket-mode download — cached, evicted, prefetched, and
URL-reversed the same way — regardless of whether "stream"/"bucket" mode is configured, since mpd can't
stream a video-hosting page directly under either one. See Architecture notes for how the bucket-mode
pieces fit together. Update this file as the project grows; don't let it drift from reality.

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
                        Contains/Download/DownloadVia/Stats/List/Remove). A persisted filename->URL index
                        (.index.json) lives alongside the cached files themselves, since the filename is a
                        one-way content hash — without it List couldn't report which URL each entry is.
                        Two independent instances back two different concepts — the evictable
                        "download-then-stream" playback cache, and a separate, non-evictable permanent
                        favorites archive — both respecting a shared disk-space safety margin. DownloadVia
                        is Download's general form for a source that isn't a plain HTTP GET (e.g.
                        internal/ytdlp extracting YouTube audio via a subprocess) — see Architecture notes
                        for both.
internal/ytdlp/         Detects YouTube video URLs (IsYouTubeURL) across every commonly used URL shape
                        (VideoID: watch links, youtu.be share links, embed/Shorts/live links, with
                        arbitrary extra query params) and extracts their audio-only stream to a local file
                        via the yt-dlp CLI (Extractor.ExtractAudio) — shells out to an already-installed
                        yt-dlp binary, never vendors or reimplements any of its extraction logic itself.
                        Also fetches a video's thumbnail (ThumbnailFetcher, a plain HTTP GET against
                        YouTube's predictable image URLs — no yt-dlp/API call needed) and title
                        (TitleFetcher, via YouTube's oEmbed endpoint) independently of audio extraction —
                        see Architecture notes for how all of this plugs into the bucket cache/mode-aware
                        resolver, the album art system, and metadata enrichment respectively.
internal/artstore/      Disk-backed store for resolved album art, content-addressed by source URL —
                        Open/Lookup/Put, a persisted {filename, hasArt} index (.index.json, same pattern as
                        internal/bucket's), same reasoning as internal/bucket for why the index lives on
                        disk. Deliberately not a copy of internal/bucket: art has a "confirmed no art"
                        state internal/bucket doesn't model, and is served back out by a static
                        http.FileServer (cmd/pi-streamer/main.go) rather than any download-and-stream
                        proxying. See Architecture notes.
internal/coverart/      Fetcher.Fetch(ctx, artist, album): MusicBrainz release search + Cover Art Archive
                        front-cover lookup, a fallback source for album art when mpd itself has nothing
                        embedded. Rate-limits only the MusicBrainz search call (its documented usage
                        policy); the Cover Art Archive fetch itself isn't throttled. Tested against
                        httptest fake servers only — never a live third-party call in this repo's tests.
                        See Architecture notes for how cmd/pi-streamer/albumart.go's artAdapter uses it.
internal/jobs/          Generic in-memory background job tracker (Manager.Start/List, a Handle for
                        SetTotal/Advance progress reporting) — panic-recovering goroutines, oldest-finished
                        pruning (never prunes a running job). Currently used only by the album art warm
                        scan (cmd/pi-streamer/albumart.go's Warm), but written generically so any future
                        long-running operation can reuse it. See Architecture notes.

web/                    React (Vite) + Mantine player UI, 2 header tabs plus a settings screen (see
                        Architecture notes for the full rationale behind cutting this down from 9, and for
                        why Settings specifically isn't a third tab): Library.jsx (find/select/manage
                        everything — Tracks/Albums/Artists/Playlists/Favorites/Recent sub-views, search,
                        edit/delete, grid/list toggle; composes LibraryEntryRow.jsx/
                        LibraryEntryGridCard.jsx/LibraryEntryForm.jsx/TrackArt.jsx, plus Playlists.jsx and
                        History.jsx rendered as sub-views), Queue.jsx (mpd's live playback queue), and
                        Settings.jsx (OLED + bucket config, plus Bucket.jsx's cache-browsing content
                        rendered inline under a "Storage" section) — opened via a cog ActionIcon at the
                        header's top right into a full-screen Modal rather than being one of the Tabs.
                        Library/Queue's Tabs.List is centered in the header (not a bottom icon row) — see
                        Architecture notes for how Tabs wraps AppShell.Main and the header to make that
                        layout possible. A persistent PlayerBar (transport/skip/volume) is the entire
                        footer now that it doesn't share that space with a nav row; clicking its art/title
                        area (not the transport buttons) opens NowPlayingScreen.jsx, a full-screen expanded
                        player (large art, title/artist/album, transport, volume, a back chevron) in its
                        own Modal — both components share their actual playback state/handlers via
                        web/src/hooks/usePlayerControls.js rather than duplicating them, since they're two
                        layouts over the same status prop and API calls, not two separate playback
                        implementations. Icons via @tabler/icons-react. The active tab is synced to
                        location.hash
                        (web/src/hooks/useHashTab.js — no router dependency, no server-side change needed
                        since a hash fragment is never sent to the server), so a reload or browser
                        back/forward doesn't lose your place.
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
extension/               A Manifest V3 Chrome extension ("Pi Streamer Remote"): a content script
                        (content.js/content.css) overlays a Play/Queue button pair directly on every
                        YouTube video thumbnail; right-clicking any other link offers the same Play/Queue
                        pair via the context menu; a popup covers pasting a URL manually. All three funnel
                        through background.js's one sendToStreamer, talking to the daemon's existing HTTP
                        API. Self-discovers the daemon's address on the LAN via mDNS hostname probing
                        rather than requiring it be typed in (a full subnet scan was attempted and
                        deliberately dropped — extensions can't use chrome.system.network) — the one actual
                        daemon-side addition this needed is a small identifying endpoint, GET /api/discover,
                        plus GET /api/track for naming the track in success notifications. See Architecture
                        notes and extension/README.md for install/usage.
```

## Architecture notes for future work

- Keep playback logic (talking to mpd via `gompd`) and the HTTP API layer separate in the Go daemon — the
  daemon is the only thing with direct `mpd` access; the frontend never talks to mpd directly. `internal/api`
  depends on the `Player` interface it defines itself (not `internal/player`'s concrete type), so handlers
  stay testable against a fake.
- `internal/ws.Hub` broadcasts playback-state changes to all connected UI clients so multiple browser
  tabs/devices stay in sync without each one polling — important given the Pi Zero 2W's limited resources
  (and the browser's — see the "browser-side watcher/memory" note below). `Hub` itself is payload-agnostic
  (`Broadcast([]byte)`); `cmd/pi-streamer/main.go` multiplexes three kinds of state over the one `/ws`
  connection as `{"type": "status"|"downloads"|"jobs", "data": ...}` envelopes (`wsMessage`,
  `marshalWSMessage(type, data)`) rather than opening a second socket or falling back to HTTP polling for
  any of them:
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
  - **jobs**: `internal/jobs.Manager`'s `onChange` callback (fired on every `Start`/`SetTotal`/`Advance`/
    finish) pushes `jobsAdapter.List()`'s full snapshot on every change — unlike downloads there's no
    `reflect.DeepEqual` diffing, since a job's `done` counter changes on essentially every tick anyway while
    one is running, so the dedup check would rarely save a send. See the generic job system note below.
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
- **`PlayURL` jumps to the new track immediately, front-of-queue** (`internal/player.Player.PlayURL`,
  `internal/mpdclient.Client.AddGetID`/`MoveInQueue`/`PlayQueueItem`) — an earlier version just did mpd
  `add`+`play`, which only ever resumed/started mpd's existing "current song" pointer (wherever the queue
  already was), not the URL just appended to the *end* of it; a repeated "Play" click looked like it just
  silently queued the track behind whatever was already playing, since nothing actually jumped to it. Fixed
  by using mpd's own primitives for this directly: `AddGetID` (`addid`) appends and returns the new queue
  id, `MoveInQueue(id, 0)` (`moveid`) repositions it to the front, and `PlayQueueItem(id)` (`playid`) jumps
  playback to it — which switches instantly (verified against a real, isolated mpd instance: `songid`/
  `elapsed` both changed to the new track immediately, no explicit `Stop()` needed first, since `playid`
  itself stops whatever was playing as part of switching). `AddToQueue` deliberately keeps the old plain
  `Add` behavior — it's "add without playing," so jumping to the front and playing would defeat the point.
- **Next/Previous still operate on mpd's queue as-is, unaffected by the above**: Next/Previous are thin
  wrappers over mpd's own `Next()`/`Previous()` over whatever the queue currently contains, in whatever
  order it's in — `PlayURL`'s front-of-queue jump changes *where* a newly played track lands, not how
  Next/Previous navigate once it's there.
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
  `GET /api/track?url=` (`Player.TrackInfo` — best-available title/artist/album for a URL on demand,
  without playing/queuing it; used by `extension/`'s notifications, see below),
  `POST /api/library {url,title,artist,album,tags}` (upsert by url — add a new entry or edit an existing
  one by resubmitting the same url with changed fields; `Player.AddToLibrary` tries a synchronous
  `MetadataFetcher` fetch first if title/artist/album are all empty) and `DELETE /api/library?url=`
  (`Player.RemoveFromLibrary` — removes from the search index only, never favorites/history/queue/bucket),
  `GET /api/albumart?url=` (resolves art via mpd on first request and **302-redirects** to a static path —
  see the album art architecture note below for why this changed from serving raw bytes directly; any error
  or confirmed no-art returns a plain `404`, not a JSON error body — art-not-found is the common case for
  radio streams, and the frontend's `<img onError>` fallback is the only path it needs),
  `POST /api/albumart/query {urls: [...]}` (cache-only, batched "is this known, and if so what static path"
  lookup — never fetches), `POST /api/albumart/refresh {url,artist,album}` (bypasses the cache and always
  re-resolves, e.g. after a re-tag or to retry the MusicBrainz fallback — see the album art architecture
  note below), `POST /api/albumart/warm` (kicks off a background full-library resolve as a tracked
  `internal/jobs` job, 202), `POST /api/albumart/suggest {query}` (candidate Title/Artist/Album matches for
  the Library edit form's "Look up" action, via the same MusicBrainz client the art fallback already uses —
  see the metadata-suggestion note below), `GET /api/jobs` (every tracked job, same shape pushed over the
  `"jobs"` WS envelope — see the generic job system note below), and the mpd-queue routes:
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
  **`DownloadVia` — the same cache for a source that isn't a plain HTTP GET**: `Download`'s whole shape (make
  room within the size cap/safety margin, atomically rename into place, update the URL index) is generic over
  *how* the bytes were obtained — only the HTTP GET part is specific to `Download` itself. `DownloadVia(ctx,
  url, fetch)` factors that out: `fetch` is handed a directory (the store's own `dir`, so the final rename
  stays on one filesystem) and must create a file there however it needs to, returning that file's path —
  used by `internal/ytdlp`'s YouTube extraction (see the YouTube-URL note below), and generically available
  for any future "mpd can't fetch this and it's not a plain HTTP GET" source. The one real wrinkle:
  `Download`'s `keyFor(url)` guesses the cached file's extension *from the URL itself* (fine for an ordinary
  HTTP URL, whose path usually does end in something like `.mp3`), but a source like a YouTube watch link
  carries no such information at all — so `DownloadVia` instead preserves whichever extension `fetch`'s own
  output file actually has (`filepath.Ext(fetchedPath)`), and `Contains`/`Lookup`/`Remove` were all changed to
  find a cached entry by glob-matching its content hash prefix (`hashFor(url)+".*"`) rather than assuming
  `keyFor`'s URL-guessed extension is necessarily the real one — needed for any of them to ever find a
  `DownloadVia`-stored entry at all, since its extension isn't derived from the URL the same way. **Found and
  fixed in the same pass, unrelated to `DownloadVia` itself**: `keyFor` used to compute its content hash on
  its own already-truncated local copy of `url` (truncated at `?`/`#`, done to guess the extension) rather
  than the original full URL — meaning a URL with a query string hashed differently depending on whether
  the code path truncated first, a latent bug that happened to never matter before because every caller went
  through the same single `keyFor` call, but would have caused exactly the same "just extracted, but not
  found" mismatch the moment `hashFor` (used directly by `DownloadVia`/`find`, on the *untruncated* URL) was
  introduced alongside it. `TestKeyForIsStableAndFilesystemSafe` (a pre-existing test, using a URL with `?`/
  `#`/`/` in its query string) caught this immediately once `find` started disagreeing with `keyFor`.
- **YouTube URL support via yt-dlp** (`internal/ytdlp`, `modeResolver.resolveYouTube` in `cmd/pi-streamer/
  bucket.go`): mpd has no idea what to do with a `youtube.com`/`youtu.be` URL under *any* playback mode —
  there's no media file to stream directly at that URL, only a video-hosting page — so `modeResolver.Resolve`
  checks `ytdlp.IsYouTubeURL(url)` *before* the stream-vs-bucket mode branch and, if it matches, always routes
  through `resolveYouTube` regardless of the configured mode: a YouTube link download-then-streams even in
  `"stream"` mode, since "stream directly" simply isn't an option for it. `IsYouTubeURL` matches by hostname
  (`youtube.com` with an optional `www`/`m`/`music` subdomain, or `youtu.be`) rather than searching the whole
  URL string, so something that merely contains "youtube" somewhere isn't mistaken for a real video link.
  `resolveYouTube` reuses the *ordinary bucket playback cache* (the same `*bucket.Store` stream/bucket mode
  already share) via `Lookup`/`DownloadVia` — a YouTube URL extracted once stays cached exactly like any
  bucket-mode download, including LRU eviction, the safety margin, and (since `Unresolve`/`prefetchNext`
  already operate on `bucket.Store` generically, not on *how* an entry got there) working correctly with
  prefetching and the Queue/Status URL-reversal path with no extra code at all. `ytdlp.Extractor.ExtractAudio`
  shells out to the `yt-dlp` CLI (`-f bestaudio --no-playlist --embed-metadata -o <dir>/ytdlp-<ts>.%(ext)s
  <url>`) — never vendors or reimplements any of yt-dlp's own extraction logic — and glob-matches
  `ytdlp-<ts>.*` afterward to find whatever container yt-dlp actually picked (varies by video: opus/webm,
  m4a, etc.), since the daemon has no need to pin down one specific format when mpd/ffmpeg can decode any of
  them directly. `--embed-metadata` has yt-dlp tag the downloaded file with the video's own title via ffmpeg
  as part of downloading it — deliberately not a separate "fetch the title" round trip — so mpd's ordinary
  tag-reading picks up a real title through the exact same path every other track's `Title` already comes
  through, with zero extra plumbing on this daemon's side; if embedding fails for any reason (e.g. ffmpeg
  missing), yt-dlp itself only warns rather than failing the download, so the track still plays, just falling
  back to the usual URL-derived title like any other untitled track. `yt-dlp`/`ffmpeg` are expected to
  already be installed and on `PATH` (`make install-deps`/`install-deps-pi` do this via `apt-get install
  yt-dlp ffmpeg`; `-ytdlp-path` overrides the binary location if it's installed somewhere else) — a missing
  binary simply fails that one play/queue request (`exec.Command` reports "executable file not found"), not
  a reason to refuse starting the daemon at all, the same graceful-degradation stance as a missing OLED or
  unconfigured indexer elsewhere in this codebase. Tested via a fake shell script standing in for the real
  `yt-dlp` binary (`internal/ytdlp/ytdlp_test.go`, `cmd/pi-streamer/bucket_test.go`) that creates a file
  matching whatever `-o` template it's given without touching the network at all — never a live call to the
  real YouTube/yt-dlp in this repo's test suite, same reasoning as the MusicBrainz tests elsewhere.
  **`VideoID` — extracting the video ID across every URL shape a real "share/copy link" button can
  produce**: `IsYouTubeURL` only checks the *host*, which is enough to decide "route this through yt-dlp
  extraction," but the thumbnail/title lookups below need the actual 11-character video ID, and a bare host
  check says nothing about *which* video. `VideoID(rawURL) (string, bool)` handles every commonly-seen
  shape: an ordinary watch link (`?v=<id>`, on `youtube.com`/`m.youtube.com`/`music.youtube.com`), a
  share/short link (`youtu.be/<id>`), an embed link (`/embed/<id>`), a Shorts link (`/shorts/<id>`), and a
  livestream link (`/live/<id>`) — all with arbitrary extra query parameters (a share link's `?si=...`
  tracking token, a timestamp's `&t=42s`, a playlist's `&list=...`) simply ignored rather than tripping up
  extraction, since real "copy link" buttons commonly attach exactly these. Deliberately returns `("",
  false)` rather than guessing for a URL `IsYouTubeURL` still accepts but that doesn't identify a single
  video at all (a bare channel or playlist-only URL) — there's no video to build a thumbnail URL or fetch a
  title for in that case.
  **Album art: the video thumbnail, computed directly, never fetched or cached by this daemon**
  (`ytdlp.HQThumbnailURL`, checked first thing in both `artAdapter.resolve` and `artAdapter.Query` in
  `cmd/pi-streamer/albumart.go`) — `artAdapter.resolve`'s general path (try `mpd.AlbumArt`, fall back to
  MusicBrainz) is actively the *wrong* thing to try for a YouTube URL: mpd never has the *original*
  `youtube.com` URL under any queue entry at all (it only ever sees the resolved local-proxy URL the
  extracted audio was served from — see `modeResolver` above), so `AlbumArt(youtubeURL)` would always come
  back empty, and the MusicBrainz fallback needs artist/album, which a YouTube track typically doesn't have
  until/unless a user edits it in. An earlier version fetched the thumbnail's bytes itself (`ThumbnailFetcher`,
  trying `maxresdefault.jpg` then falling back to `hqdefault.jpg`) and persisted them to `internal/artstore`
  exactly like mpd/MusicBrainz art — replaced by simply returning YouTube's own public CDN URL
  (`https://i.ytimg.com/vi/<id>/hqdefault.jpg`, guaranteed to exist for every real video) directly: there was
  never anything to fetch-and-cache in the first place, since the thumbnail is already a stable, permanent,
  publicly reachable URL computed purely from the video ID, with zero network I/O of this daemon's own.
  `resolve` returns it before even checking the disk cache or `force`/Refresh (the answer never changes, so
  those concepts don't apply), and `Query` — normally cache-only, never fetching — returns it too for any
  *unknown* URL, specifically so a freshly queued/played video's thumbnail shows up immediately without
  waiting on a first `GET /api/albumart` round trip to populate anything. A track's own custom art (checked
  first in both) still wins over this, same as ever. `ThumbnailFetcher` itself, now fully unused, was deleted
  outright, along with its tests — see `internal/ytdlp/ytdlp.go`'s history if the byte-fetching approach is
  ever needed again (e.g. to also mirror the image for an offline/no-egress deployment).
  **Two different thumbnail sizes for two different contexts** — `resolve` actually uses
  `ytdlp.HQThumbnailURL` (480×360) while `Query` uses `ytdlp.DefaultThumbnailURL` (120×90, YouTube's
  smallest generated size, same "exists for every video" guarantee as hqdefault): `Query` backs *list*
  views (Library/Queue/search results, batched via `useAlbumArtStatus`) showing many thumbnails at once,
  where the smaller size means less bandwidth/faster loading for something displayed small anyway;
  `resolve` (via `GET /api/albumart`, i.e. `albumArtUrl()`) backs the few places a *single* track's art is
  shown large — the "now playing" view (`PlayerBar`'s mini art, `NowPlayingScreen`'s big art) and the
  Library track detail page, both of which call `albumArtUrl`/hit `TrackArt`'s no-batched-query fallback
  path directly rather than going through `Query` at all.
  **Title: YouTube's oEmbed endpoint, independent of extraction/embedding** (`TitleFetcher`, wired into
  `metadataAdapter.Fetch` in `cmd/pi-streamer/metadata.go`) — `--embed-metadata` (above) already gets a real
  title onto the *extracted audio file* for mpd's own tag-reading to pick up once the track is actually
  played, but that's downstream of playback and doesn't populate the *search index* at add-time the way
  `internal/metadata.Fetcher` does for an ordinary tagged file. `internal/metadata.Fetcher` itself is useless
  for a YouTube URL regardless (it does a ranged HTTP GET expecting an audio container; a `youtube.com` URL
  is an HTML page), so `metadataAdapter.Fetch` checks `ytdlp.IsYouTubeURL(url)` first and, if true, asks
  YouTube's oEmbed endpoint (`https://www.youtube.com/oembed?url=...&format=json`) for the title directly —
  a single lightweight, unauthenticated GET returning a small JSON document, deliberately *not* a yt-dlp
  subprocess call, since a title alone doesn't need yt-dlp's much heavier format-resolution/download
  machinery. This runs through the exact same `player.enrichMetadata` add-time path every other track's
  metadata enrichment already does (see the URL-dedup/metadata-enrichment note below) — no changes needed in
  `internal/player` at all, since `MetadataFetcher` was already the abstraction point for "how do we learn
  more about this URL," and `metadataAdapter` (the real implementation) is where the YouTube-specific
  routing belongs. `titleFetcher`/`audioTagFetcher` (in `cmd/pi-streamer/metadata.go`) are narrow interfaces
  over `*ytdlp.TitleFetcher`/`*internal/metadata.Fetcher` respectively, matching the "package defines the
  interface it needs" pattern used throughout this codebase — specifically so tests can fake both and never
  make a real network call (an earlier draft of this test skipped that and made a genuine request to a real
  `youtu.be` URL from the test suite by accident, caught by its suspicious ~1.4s runtime before being fixed).
  **Frontend: a YouTube badge wherever a track is shown** (`web/src/isYouTubeUrl.js`) — a small JS port of
  `ytdlp.IsYouTubeURL`'s same host-based check (kept in sync intentionally: if the Go version ever
  recognizes a new host, this one should too), used to render a compact `IconBrandYoutube` next to a
  track's title in `LibraryEntryRow.jsx` (list view), as a small overlay badge on the art itself in
  `LibraryEntryGridCard.jsx` (grid view), in `Queue.jsx`'s rows, and in `NowPlayingScreen.jsx`'s title line
  — everywhere a track is displayed *except* `PlayerBar.jsx`'s mini bar, deliberately: that view is already
  the most space-constrained one (art + title + artist + an expand chevron all competing for a ~240px
  column), and the same track is one tap away from the full-screen view that does show it.
- **Queue/Status title fallback** (`internal/player/title.go`'s `deriveTitleFromURL`): mpd's own `Title` tag is
  simply empty for a track it hasn't (or can't) read tags for — `Player.Queue`/`Status` were otherwise passing
  that "as mpd reports it" straight through, showing a blank field or the bare URL. `deriveTitleFromURL` builds
  a readable name from the URL's last path segment (percent-decoded, extension stripped, `-`/`_` → spaces —
  e.g. `.../02-Chandamaama%20%28D%29.mp3` → `"02 Chandamaama (D)"`) and is only ever applied when mpd's own
  Title is empty, so it never overrides real tag data. Same idempotent-for-plain-text design as
  `oledSanitize`/`oledTruncate` below — safe to run on a value that might already be a real title.
- **`mpdclient.Status.Song` is always the URI, never a display title** — an earlier version preferred mpd's
  own `Title` tag for `Song` (falling back to the file URI only when no `Title` existed), which quietly broke
  every URL-keyed use of it for any track with an embedded title (i.e. most of them): album art lookup by URL
  (`PlayerBar.jsx`/`NowPlayingScreen.jsx`'s `albumArtUrl(status.song)`) and "is this the currently playing
  track" row highlighting (`Library.jsx`'s `entry.url === status.song`) were both silently comparing against
  a title string instead of a URL. `Title` already exists as `Status`'s own separate field for display, so
  the fallback was pure redundancy with a real cost, not a deliberate design choice; `GompdClient.Status` now
  always sets `status.Song = song["file"]` unconditionally. Caught while investigating the OLED-metadata bug
  below — not itself what was reported, but the same `Status()` code path, and a real correctness bug once
  found. Verified live against a real (isolated) mpd instance with an actually-tagged file (`ffmpeg`-generated,
  embedded Title/Artist/Album) — `GET /api/status` confirmed `song` stayed the real URL throughout, not the
  title. `internal/mpdclient.FakeClient` never had this bug (its `Song` field is just whatever's set
  directly), which is exactly why the whole unit test suite never caught it — same class of gap as the
  synchronous-indexing and album-art-mutex bugs before it, only ever found via live testing.
- **Now-playing *and queued* metadata is enriched from the search index, not just mpd's own file tags**
  (`Player.lookupEnrichment`/`refreshEnrichment`/`invalidateEnrichment` in `internal/player/player.go`,
  used by both `enrichStatus` and `Queue`): reported bug — editing a track's Title/Artist/Album via the
  Library tab (`AddToLibrary`, which only ever updates the search index, never the file itself) saved
  successfully, but the OLED (and, it turned out, `PlayerBar`/`NowPlayingScreen`/`GET /api/status`
  generally) kept showing the *original* embedded file tags, since `Player.Status()` previously passed
  mpd's `CurrentSong` Title/Artist/Album straight through with no cross-reference to the index at all.
  Fixed by having `Status()` override Title/Artist/Album with the index's values for the current
  `status.Song`, whenever the index has a non-empty value for that field (an empty index field leaves
  mpd's own tag alone, rather than blanking it out — see
  `TestStatusEnrichmentDoesNotBlankFieldsTheIndexLeavesEmpty`).
  **Generalized to `Queue` too, on a follow-up request** ("queue should have the same logic as track for
  the title") — `Queue()` previously had *no* index cross-reference at all, only mpd's own tag or
  `deriveTitleFromURL`'s fallback, which produces useless results for some sources (a bare YouTube watch
  link's path is just `/watch`). The single-slot cache (`statusURL`/`statusResult`/`statusResolved`) that
  originally backed only `enrichStatus` doesn't generalize to `Queue`, which can have many
  simultaneously-displayed tracks — replaced with `enrichCache map[string]enrichedResult`, keyed by URL,
  shared by both `Status` and `Queue`; `lookupEnrichment(url)` is the one shared entry point either calls.
  **Deliberately never pruned**: an earlier per-song single slot self-evicted just by being overwritten on
  every song change, but a personal-scale set of distinct URLs accumulated over one daemon run (each cache
  entry a few small strings) isn't worth adding LRU eviction for.
  **Does not call `Indexer.Get` on every `Status()`/`Queue()` call**, even though `Status()` is polled once
  a second while playing (`cmd/pi-streamer`'s ticker) — that would gate the status ticker's throughput on
  the search-indexer service's own latency/availability on every single tick, reintroducing exactly the
  class of bug already fixed once for the play-time indexing path (see the URL-dedup/metadata-enrichment
  note below). Instead: a lookup only ever fires the *first* time a given URL is asked about at all
  (`lookupEnrichment`'s cache-miss branch), and even then runs in its own goroutine so it can never block
  the caller — the call that first sees a new URL returns mpd's own (unenriched) tags/URL-derived fallback
  immediately, and a later call picks up the enriched values once the goroutine resolves (typically well
  under either caller's poll interval). **Editing a track's metadata needed its own separate fix**: since
  `lookupEnrichment`'s cache only ever gets populated once per URL and has no other trigger to refresh
  itself, an edit would otherwise never show up at all once cached — `AddToLibrary` calls
  `invalidateEnrichment(url)` after a successful `IndexURL`, which — only if `url` is already cached at
  all — kicks a fresh background lookup immediately. Verified live end-to-end against the isolated mpd
  instance: played an `ffmpeg`-tagged file, confirmed `GET /api/status` showed its embedded tags, edited
  its Artist/Album via `POST /api/library` while it was still playing, and confirmed `GET /api/status`
  reflected the edit immediately, with no track change needed.
  **A second, unrelated race in the same cache caused a real "title never resolves" bug**: `enrichMetadata`
  (the background goroutine that fetches a brand-new URL's real title/artist/album — see the URL-dedup/
  metadata-enrichment note below) never called `invalidateEnrichment` after successfully re-indexing with
  the real data. `lookupEnrichment`'s *very first* call for a URL (typically `Status`/`Queue`'s first poll
  right after `PlayURL`/`AddToQueue` returns) races `indexAsync`'s own two-step flow: if that first
  `lookupEnrichment` call's background `refreshEnrichment` reads the index *before* `enrichMetadata`'s real
  update lands, it caches `indexAsync`'s thin, title-less `IndexURL` call as `resolved: true` — and since a
  URL is only ever looked up once, nothing would ask the index again for it, permanently stuck on
  `deriveTitleFromURL`'s fallback (e.g. a bare "watch" for a YouTube URL, whose path is just `/watch`)
  regardless of how long `enrichMetadata`'s own fetch took. This is what a report of "why is there a delay
  resolving the YouTube title" traced back to — not a slow fetch, a lost race with no recovery. Fixed by
  having `enrichMetadata` call `p.invalidateEnrichment(url)` after its own successful `IndexURL`, the exact
  same recovery `AddToLibrary` already had for a manual edit.
- **Media library** (`internal/search.Index.List`, `GET /api/library`, the Library tab): "remembers every URL
  added, referred at any point in future" turned out to need **no new persistence** — `internal/search`'s
  SQLite index already durably indexes every played/favorited/playlisted URL (`player.index`'s best-effort
  calls, pre-existing). The gap was purely presentational: `Search` requires an FTS5 `MATCH` query (which
  errors on `""`, so it can't be reused for "show everything"), so there was no way to just browse the
  collection. `List(limit, offset)` (`ORDER BY rowid DESC` — SQLite's own insertion order, newest first) fills
  that gap; `internal/indexer.Client.List` and `player.Player.Library` mirror `Search`'s shape exactly
  (same nil-indexer error, same limit semantics), and the Library tab paginates via Previous/Next controls
  (see the pagination note below — this replaced an earlier "Load more" button).
  The library is now manageable too, not just browsable — see `Delete`/`AddToLibrary`/`RemoveFromLibrary`
  in the HTTP surface note above and the unified Library UI note below.
- **Unified Library tab + mobile-first nav** (`web/src/components/Library.jsx`, `App.jsx`): the app
  had grown to 9 tabs (Search, Now Playing, Queue, Favorites, Playlists, History, Bucket, Library,
  Settings), each added independently over many phases — too many for a phone, and Search/Library were
  near-duplicate read-only views with no way to add/edit/delete anything. Consolidated to 2 real tabs plus
  a settings screen (see the header-layout note further below for why Settings specifically became a
  Modal, not a third tab):
  - **Library** — the one-stop hub for finding/selecting/managing media. **Adding a brand-new URL no longer
    happens from this tab at all** — two successive UI elements for it (first a bare-URL quick-action bar
    with its own Play/Add-to-queue buttons, covering the old standalone Now Playing tab's one unique job;
    then, after that was removed, an "Add a track" button opening an inline form) were both removed by
    explicit request. A new URL still enters the library the same way it always could without either of
    those: through the Queue tab's own URL input (`POST /api/queue`) or by playing/favoriting/playlisting
    one — all of which already best-effort index it (see the URL-dedup/metadata-enrichment note below)
    without Library needing its own separate entry point for the same thing. **Editing** an existing entry
    still works exactly as before, just via a `Modal` rather than an inline `Collapse` — clicking "Edit" on a
    row far down a long/paginated list used to just silently change content back at the top of the page,
    with no visible feedback at the point of the click at all (the "editing doesn't work" bug that was
    reported and fixed). A `Modal` is always front-and-center regardless of scroll position, so
    `editingUrl !== null` opens `LibraryEntryForm` in a `Modal` (`handleEdit` pre-fills `formValues` from the
    row already in hand — **no new "get one entry" endpoint was needed**, since `List`/`Search` results
    already carry full `title`/`artist`/`album`/
    `tags`). `LibraryEntryForm.jsx` itself dropped its `editing` prop once this was its only caller — the
    URL field is now unconditionally disabled and the submit button unconditionally reads "Save changes,"
    since there's no add path left for it to branch on. A search box (absorbing the old
    standalone Search tab) is a *separate render path* from the paginated browse list (`searchResults !==
    null` takes over rendering entirely) — v1 keeps search results flat/unpaginated (a generous limit, no
    pagination) specifically to avoid changing `internal/search.Search`'s signature; revisit only if that
    turns out to matter. Below that, a horizontally-scrollable `Chip.Group` picks the sub-view: **Tracks**
    (real Previous/Next pagination, not "Load more" — see the pagination note below) / **Albums** /
    **Artists** (client-side `groupBy`, see the note below for what it groups over) / **Playlists** /
    **Recent** (render `Playlists.jsx`/`History.jsx` largely unchanged — both were already zero-prop,
    self-contained components, so relocating them here needed no rework) / **Favorites** (see the note
    below — favoriting itself happens via a ♥ toggle on *any* track row everywhere, not a separate
    add-by-URL form the old `Favorites.jsx` had; that file, along with `Search.jsx` and `NowPlaying.jsx`,
    is deleted). A
    `SegmentedControl` (List/Grid, enabled only for Tracks/Albums/Artists — Playlists/Favorites/Recent stay
    list-only) switches `LibraryEntryRow.jsx` (icon-only actions — five actions in a row would overflow a
    phone-width `Card` as labeled buttons) for `LibraryEntryGridCard.jsx` (album art via the new shared
    `TrackArt.jsx`, same `onError`-fallback-icon pattern as `PlayerBar`/old `NowPlaying`, actions —
    including "Re-fetch art", see the album art note below — collapsed behind one `Menu` to keep tiles
    clean). `Library` takes `status` as a prop now (to highlight the currently-playing row) but is memoized
    with a custom comparator on `status?.song` only — same reasoning as `Queue.jsx`'s own comparator, so it
    doesn't re-render every second from elapsed-time ticks it doesn't care about.
  - **Tracks pagination: Previous/Next, not "Load more"** — the original design accumulated pages into one
    ever-growing `entries` array with no sense of where you were in it (and no way to go back). `loadPage
    (nextPage)` now *replaces* `entries` with exactly one page (`getLibrary(PAGE_SIZE, nextPage *
    PAGE_SIZE)`) and tracks `page`/`hasNext` instead of `offset`/`hasMore`; `hasNext` is a heuristic (a full
    page came back, so there's *probably* another) since `internal/search.List` doesn't return a total
    count — the same heuristic "Load more" used, just now driving a bounded Previous/Next control pair
    instead of unbounded accumulation. This is a purely frontend change — no backend signature changed,
    since `List(limit, offset)` already supported arbitrary offsets.
  - **Albums/Artists/Favorites group over the whole library, not the current Tracks page** — an earlier
    version of this component had Albums/Artists/Favorites all `groupBy`/cross-reference over `entries`
    (whichever single page Tracks happened to be showing), which looked broken in practice: a library with
    more than one page of tracks would have most albums/artists never show up at all, because the newest
    page tends to be dominated by just-added tracks whose metadata hasn't been enriched yet (see the
    metadata-enrichment note below), pushing everything else off that page — reported as "not showing any
    tracks related to an artist or album." Fixed with a second, separate fetch (`allEntries`,
    `loadAllEntries`, a single `getLibrary(ALL_ENTRIES_LIMIT=2000, 0)` call, not a paginating loop — the
    same "personal-scale library" scope call this codebase already makes for `SEARCH_LIMIT`) kept in sync
    with the Tracks page's own pagination but not driven by it: reloaded on the same add/edit/delete events
    as `entries`, but never replaced by `loadPage`. Albums/Artists group over `allEntries`; Favorites
    cross-references `allEntries` by URL instead of `entries`. `entries`/`page`/`hasNext` still exist and
    still drive Tracks' own Previous/Next pagination — this is a second, independent piece of state, not a
    replacement for it.
  - **Albums/Artists are real drill-down pages, not an inline dump of every group** — album→tracks,
    artist→tracks, album→artist, and artist→album are all independently navigable, not just "everything
    grouped on one screen." `Library`'s `activeFilter` state (`{type: 'artist'|'album', value} | null`)
    decides which of two renderers `renderBrowseContent` calls for the Albums/Artists sub-views:
    - **`renderEntityIndex(type)`** (no `activeFilter`, or a filter of the other type) — a clickable
      directory: every distinct album/artist name in `allEntries` (via `groupBy`) as a `Card` showing the
      name and track count (grid mode adds a `TrackArt` thumbnail — the album's first track's art for
      Albums, an artist-name-seeded placeholder with no `url` for Artists, since there's no such thing as
      "artist art" from mpd). Tracks with no album/artist tag are excluded from the directory itself (there's
      nothing to link to) but their count is still surfaced in a small note, so they're never silently
      unaccounted for. Clicking a card calls `handleArtistClick`/`handleAlbumClick`.
    - **`renderEntityDetail(type, value)`** (`activeFilter.type === type`) — the actual "page": a header
      (type + name + a "Back" button that clears `activeFilter`), then, for an **album**, every distinct
      artist appearing on it as clickable `Anchor`s ("By ...") followed by its full track list; for an
      **artist**, their tracks grouped by album (`groupBy` again, this time scoped to just that artist's
      tracks) with each album name itself a clickable `Anchor` onward to *that* album's detail page — so
      artist→album→tracks is reachable without a second navigation hop, and a "Singles / no album" group
      (not clickable — nothing to link to) catches tracks with no album tag.
    A track's artist/album text is *also* directly clickable from anywhere it's rendered — `LibraryEntryRow.
    jsx`/`LibraryEntryGridCard.jsx` render it as an `Anchor` (`component="button"`, so it's a real,
    keyboard-accessible control, not an inert `<a>` with no `href`) rather than static `Text`, wired to the
    same `handleArtistClick`/`handleAlbumClick` — so this works from Tracks, Favorites, and search results
    alike, not just from inside the Albums/Artists tabs themselves. Switching sub-views directly via the
    `Chip.Group` row goes through `handleSubViewChange` instead of `setSubView` directly, specifically to
    clear `activeFilter` first — otherwise picking, say, Artists from the chip row right after a drill-down
    would land on whatever artist's detail page was last open instead of the index.
  - **Track detail page** (`renderTrackDetail`, `openTrack`/`openTrackEntry`) — the same "detail page, not
    just a filtered list" treatment applied one level down: clicking a track's title (now an `Anchor`, not
    static `Text`, on both `LibraryEntryRow.jsx`/`LibraryEntryGridCard.jsx`) or its "View track details"
    menu item opens a page with larger art, the track's tags/raw URL, clickable Artist/Album links (the
    same `handleArtistClick`/`handleAlbumClick` as everywhere else), and every per-track action as a full
    button (Play/Queue/Favorite/Edit/Re-fetch art/Delete) rather than an icon. Reachable from *any* sub-view
    a track can appear in (Tracks, an album/artist detail page's own track list, Favorites, search results)
    — `openTrack` stores just the clicked entry, but the page actually renders `openTrackEntry`, a fresh
    `allEntries.find(url)` lookup on every render (falling back to the stored entry if not found there yet),
    so editing the very track whose detail page is open updates it immediately instead of showing stale
    data. Deleting the open track clears `openTrack` (`handleDelete` checks `cur?.url === entry.url`) rather
    than leaving the detail page open on a track that no longer exists. Rendered by conditionally
    overriding *both* the search-results branch and the sub-view browse branch in the main return (checked
    first, before either) — so it can be opened while a search is active or while inside an album/artist's
    detail page without losing that context (closing it via "Back" returns to whichever was showing).
    **Menu shortcuts bypass this page entirely**: every row/card's overflow menu also has "Go to artist"/
    "Go to album" items (alongside "View track details") that call `handleArtistClick`/`handleAlbumClick`
    directly — jumping straight to the artist/album detail page without opening the track detail page
    first, for when that's all the user actually wanted. `LibraryEntryRow.jsx` gained this overflow `Menu`
    specifically for this (it previously had no menu at all, just five bare `ActionIcon`s) — Play/Queue/
    Favorite stay as one-tap icons since they're the most common actions; View details/Go to artist/Go to
    album/Edit/Delete moved behind the menu so the row doesn't need eight icons to fit a phone width.
  - **Queue** — unchanged; kept separate because live playback ordering is a genuinely distinct, frequently
    used job from browsing/managing the library.
  - **Settings** — absorbs the old Bucket tab's cache-browsing content (`<Bucket downloads={downloads} />`
    rendered inline under a "Storage" section) since it's fundamentally a storage/admin concern, alongside
    the bucket *config* section that already lived here. `Settings` takes `downloads` as a prop, same as
    `Bucket` used to receive directly — unchanged by the header-layout rework below, just where it's
    mounted from changed (a `Modal`'s children now, not a `Tabs.Panel`'s).
  **Navigation lives in the header, not a bottom icon bar** — an earlier version put Library/Queue/Settings
  in a bottom icon row (the standard mobile-app pattern, thumb-reachable above the mini-player, Spotify/
  Apple Music-style) with `Tabs` wrapping *both* `AppShell.Main` and `AppShell.Footer`. Replaced by explicit
  request with: Library/Queue as `Tabs.Tab`s centered in the header (`Tabs` now wraps `AppShell.Main` and
  the header instead of the footer), and **Settings pulled out of the tab set entirely**, opened via a cog
  `ActionIcon` at the header's top right into a full-screen `Modal` (`settingsOpen` state in `App.jsx`,
  `transitionProps={{ transition: 'slide-left' }}`) — it's a config/admin screen visited occasionally, not
  a primary destination that needs equal billing with Library/Queue, and removing it from the tab set is
  what freed enough header space for centered tabs to fit at all. Centering is pure flexbox, not a Mantine
  prop: the header's `Group` has an empty `flex: 1` spacer on the left and a `flex: 1` (`justify="flex-end"`)
  group of badges + the cog icon on the right, so the `Tabs.List` in between sits centered regardless of how
  wide either side's actual content is — two equal-`flex` siblings always split remaining space evenly,
  independent of their own content width. `PlayerBar` is now the *entire* footer (no more nav row sharing
  it), so the footer height dropped from 132px to 80px to match. `keepMounted={false}` is preserved exactly
  as before (see the browser-memory note below) on the now-2-tab `Tabs` — the underlying "don't leave an
  inactive tab's effects/timers running" reasoning is unchanged, just for fewer tabs.
- **Full-screen Now Playing view** (`web/src/components/NowPlayingScreen.jsx`, `web/src/hooks/
  usePlayerControls.js`) — `PlayerBar`'s mini bar is deliberately compact (art/title/artist truncated to
  fit a 80px footer), which is fine for "what's playing at a glance" but not for actually looking at the
  art or reading a long title/artist/album. Clicking the mini bar's art/title area (a `role="button"` wrapper
  around just that section — the transport buttons next to it keep their own independent click targets, so
  pressing Play doesn't also expand the screen) opens `NowPlayingScreen` in a full-screen `Modal`
  (`nowPlayingOpen` state in `App.jsx`, `transitionProps={{ transition: 'slide-up' }}` — sliding up from the
  mini bar's position at the bottom, the standard mobile-music-app gesture/animation for this exact
  interaction) with `withCloseButton={false}`, since the screen has its own back affordance (a chevron-down
  `ActionIcon` at the top) rather than relying on `Modal`'s generic dialog chrome — the goal is an immersive
  full-screen player, not a dialog that happens to be full-screen. **Both `PlayerBar` and `NowPlayingScreen`
  are different layouts over the exact same playback state**, not two separate playback implementations:
  `usePlayerControls(status)` holds everything that has to live somewhere regardless of which layout is
  showing it (a dragged-but-not-yet-committed seek/volume slider value, whether album art failed to load,
  the last non-zero volume for mute/unmute) and every handler that touches it (play/pause, next/previous,
  relative/absolute seek, volume, mute toggle) — extracted from `PlayerBar` (which used to own all of this
  directly) specifically so `NowPlayingScreen` wouldn't need its own separate copy of the same logic just to
  render it bigger. Nothing about the daemon's WebSocket-pushed `status` changed for this — both components
  still just receive it as a prop from `App.jsx`, same as before.
  **`usePlayerControls`'s `playPausePending`** — `PlayerBar`/`NowPlayingScreen`'s play/pause `ActionIcon`
  passes it straight to Mantine's own `loading` prop (which `ActionIcon` supports natively — no custom
  spinner markup needed), set for the duration of `handlePlayPause`'s `pause`/`resume` call, so the button
  itself shows it's working rather than sitting inert while a request is in flight.
- **Play buttons show a spinner while a fresh `PlayURL` is in flight** (`Library.jsx`'s `pendingPlayUrl`,
  passed to `LibraryEntryRow`/`LibraryEntryGridCard`/the track detail page's Play `Button` as `isPending`/
  `loading`) — a *newly submitted* YouTube URL's `POST /api/play` can take a real, visible while to respond
  (the daemon extracts its audio via yt-dlp before mpd ever starts playing — see the YouTube-URL
  architecture note above), unlike `PlayerBar`'s play/pause toggle (`playPausePending`, above), which only
  ever pauses/resumes whatever's *already* loaded and is never this slow. `pendingPlayUrl` tracks the one
  URL currently submitting (not a generic boolean), so only the specific row/card/button that was actually
  clicked shows the spinner — every other row's Play icon stays as-is. Each component uses Mantine's own
  `loading` prop directly (`ActionIcon`/`Button` both support it natively) rather than hand-rolling a
  `Loader` swap; the grid card's Play action lives behind its overflow `Menu` (which closes on click, so a
  spinner there wouldn't be visible), so its *menu-trigger* `ActionIcon` (the "..." dots) shows loading
  instead — visible on the card itself, and doubling as a guard against reopening the menu mid-request.
- **URL dedup and add-time metadata enrichment** (`internal/urlnorm`, `internal/metadata`,
  `internal/player.Player.index`): every URL-accepting entry point (`PlayURL`, `AddToQueue`, `AddFavorite`,
  `RemoveFavorite`, `AddToPlaylist`) normalizes the URL first (`urlnorm.Normalize`: lowercases scheme/host,
  strips a default port, fills in an empty path as `/` — deliberately *not* touching path/query/fragment,
  since those are often case-sensitive server-side and a query string can carry an auth token) so trivially
  different representations of the same track (`HTTP://Example.com:80/x.mp3` vs `http://example.com/x.mp3`)
  collapse onto the same favorite/playlist/history/index entry instead of creating a duplicate; an empty or
  unparseable URL is now rejected up front with a clear error rather than reaching mpd or the store at all.
  Separately, `Player.index` (the best-effort search-indexing hook shared by all four entry points) calls
  `Indexer.Get(url)` first: if the URL is already indexed with a real artist/album, that entry is left alone
  (no re-index, no re-fetch — this is the "if it exists, reuse it" half); if it's brand new, an `IndexURL`
  call goes out (so the URL shows up in the Library, even before any tags are known) and, if a
  `MetadataFetcher` is configured, `enrichMetadata` fetches real tags and re-indexes with them once they're
  in. Real `MetadataFetcher`: `internal/metadata.Fetcher`, which does a ranged GET of a URL's first 1MiB and
  parses embedded ID3/FLAC/Vorbis tags via `github.com/dhowden/tag` — this is what gives a URL real
  Artist/Album grouping in the Library as soon as it's added, not just after mpd happens to play it and
  report decoded tags back (a stream with no embedded tags, e.g. most internet radio, just keeps the existing
  `deriveTitleFromURL` fallback — `MetadataFetcher.Fetch`'s `ok=false` return means "nothing more to add",
  not an error). `internal/search.Index.Get`/`internal/indexer.Client.Get` (`GET /get?url=`, 404 on a miss)
  back this end to end; both `IndexURL` and these entry points' whole flow are exercised against a fake
  `Indexer`/`MetadataFetcher` in `internal/player/player_test.go` (no real search-indexer service needed).
  **`Player.index` runs entirely in its own goroutine** (`indexAsync`, own panic recovery — same reasoning as
  `safeGo` below), not just the metadata-enrichment tail end of it — this was a real regression introduced
  when the `Get` check above was added: a brand-new URL now costs *two* sequential blocking HTTP round-trips
  to the search-indexer service (`Get` then `IndexURL`) where it used to cost one, and while `index` was still
  called synchronously at the end of `PlayURL`/`AddToQueue`/etc., that meant a slow or unreachable
  search-indexer directly delayed the moment those calls returned — up to the client's 5s timeout, twice, for
  a URL played for the first time specifically (a known URL only pays the `Get` cost once and returns early).
  Verified with an isolated daemon/mpd against a deliberately unresponsive stand-in indexer (a raw TCP
  listener that accepts but never replies): before this fix `PlayURL` blocked for the full timeout; after,
  `POST /api/play` returns in single-digit milliseconds regardless, and `GET /api/status` immediately
  afterward already shows the new track playing with full metadata (mpd decodes tags synchronously as part of
  its own `play`, independent of anything in `internal/player`). This is *not* an mpd-side issue — a direct
  test (`mpc add`+`mpc play` against a deliberately 4s-delayed stream, status polled concurrently from a
  separate connection) confirmed mpd's own `add`/`play` return immediately and don't block other connections;
  `GompdClient`'s per-call mutex was never the bottleneck here.
- **Hash-based tab routing** (`web/src/hooks/useHashTab.js`): the active `Tabs` value is synced to
  `location.hash` (e.g. `#/queue`) instead of local-only `useState`, so a reload or browser back/forward keeps
  your place. Deliberately hash-based, not real paths with a router library: a hash fragment is never sent to
  the server, so `cmd/pi-streamer`'s static `http.FileServer` needs no "SPA fallback" route change for a hard
  refresh on a given tab to keep working — real paths would have needed exactly that.
- **Library's search query, sub-view pill, and grid/list toggle persist across reloads** (`web/src/hooks/
  useLocalStorageState.js`) — a small `[value, setValue]` hook with the same shape/functional-update
  support as `useState` (so it drops in as a replacement) but backed by `localStorage`, read once via a
  lazy initializer (no flash of the default before the stored value loads) and written on every change;
  `localStorage` access is wrapped in try/catch since it can throw (private browsing, disabled storage) —
  falls back to the given default and simply doesn't persist rather than crashing the tab. `Library.jsx`
  uses it for `query` (`library.query`), `subView` (`library.subView` — which pill: Tracks/Albums/Artists/
  Playlists/Favorites/Recent), and `viewMode` (`library.viewMode` — grid/list) — genuine personal
  preferences, unlike `page`/`activeFilter`, which stay as plain `useState`: landing back on whichever
  Tracks page or drilled-into album/artist you happened to be viewing on a *previous* visit isn't obviously
  desirable the way "remember which pill/view/search I was using" is, and `activeFilter` in particular is
  already cleared by `handleSubViewChange` whenever the pill itself changes (see the drill-down navigation
  note above) — persisting it would fight that same logic on every reload. Restoring `query`'s text alone
  wouldn't restore what was actually on screen, so the mount effect (`useEffect(..., [])`) also re-runs
  `handleSearch()` once if the restored query is non-empty — deliberately not a dependency-array-driven
  effect (that would re-search on every keystroke instead of once at startup), which is why it triggers the
  same `react-hooks(exhaustive-deps)` lint warning `useHashTab.js` already does for the same "intentionally
  mount-only" reason; both are left as accepted, understood warnings rather than suppressed.
  `web/src/components/Settings.jsx` also has a "Library" section with a `SegmentedControl` for the same
  `library.viewMode` key, via the same hook — not a second, separate setting, but a second entry point to
  the one preference: Settings is where you'd go to set a deliberate default, Library's own toggle is where
  you'd go to just glance at grid view for a moment, and either one changes what the other shows/starts on
  next, since they're reading and writing the same `localStorage` key.
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
- **Album art: resolved once via mpd, then served as a static file, never held in the Go process's memory**
  (`internal/artstore`, `cmd/pi-streamer/albumart.go`'s `artAdapter`, `internal/api`'s `Art` interface) — this
  runs on a 512MB Raspberry Pi Zero 2 W, so an in-memory image cache of *any* bounded size is permanent
  pressure on an already-tiny budget; a first pass at this feature did exactly that (a small in-process LRU)
  before being replaced with this disk-backed design. Mechanics: `gompd`'s `ReadPicture`/`AlbumArt` fetch
  mpd's binary response in a loop, one command per chunk (mpd caps each response around a few KB), so a
  single embedded-art fetch can be dozens of round trips — `GompdClient.AlbumArt` therefore uses its own
  **dedicated mpd connection** (`artConn`/`artMu`, separate from `conn`/`mu`; falls back to sharing the
  primary connection if a second `Dial` fails, rather than failing startup over it), so an in-flight art
  fetch can never block the 1s status ticker's `Status()` calls (which share the primary connection) for its
  whole multi-round-trip duration — this was a real, verified contributor to UI/OLED stalls, on top of the
  unrelated synchronous-indexing bug described in the URL-dedup note above. `fetchAlbumArt` also unwraps
  `mpd.Error` (a genuine ACK response — mpd is alive and definitively said "no cover/no such song") into a
  `(nil, nil)` "confirmed no art" result, distinct from an actual connection/protocol error — this matters
  because mpd's own answer for "no art" **is** an ACK error, not just empty data, so treating any non-nil
  error as "don't cache" would never cache the single most common case (most internet radio has no art at
  all). `internal/artstore.Store` persists resolved art to disk, content-addressed by URL (SHA-256 + an
  extension picked via `http.DetectContentType`), with a tiny on-disk JSON index (`url -> {filename,
  hasArt}` — no image bytes, just metadata, and it survives daemon restarts, so a warmed cache doesn't need
  re-warming after every restart). `GET /api/albumart?url=` resolves (fetching+persisting on first request)
  and **302-redirects** to `/art/<filename>` with a long `Cache-Control` on the redirect itself (redirects
  are cacheable per RFC 7234 when they say so, and modern browsers honor that) — so a *repeat* request for
  the same track's art never even reaches this handler again, let alone mpd. `/art/` itself is a plain
  `http.FileServer(http.Dir(artStore.Dir()))` mounted directly in `main.go` (same pattern as `web/dist`'s own
  static serving) — genuinely static file serving for the actual image bytes, no application code involved
  at all on that path. `POST /api/albumart/query {urls}` is `internal/bucket`'s `Query` pattern applied
  here — a batched, cache-only (never-fetches) lookup returning `{hasArt, path}` per known url, letting the
  frontend skip straight to the static path (bypassing even the redirect hop) for anything already resolved,
  and skip straight to a placeholder (no `<img>` request at all) for anything confirmed to have none.
  `POST /api/albumart/warm` (`artAdapter.Warm`) walks the whole library pre-resolving anything unknown — an
  explicit "initiate" action (a button in the Settings tab) rather than only ever discovering art lazily,
  one track at a time, as each is first viewed; tracked via `internal/jobs` (see the generic job system note
  below) rather than a bare `atomic.Bool` overlap guard, so its progress is now visible in the UI instead of
  running silently in the background.
  **Frontend**: `web/src/placeholderArt.js` generates a deterministic color+monogram SVG from a hash of the
  seed string (album name, falling back to title/url) — pure client-side JS, no network round trip at all —
  used by `TrackArt.jsx` instead of a generic gray icon whenever art is confirmed absent (`knownHasArt ===
  false`, from a batched `useAlbumArtStatus` query) or an `<img>` actually fails to load. `TrackArt` renders
  the real `<img>` pointed straight at the query's returned static `path` when known, `/api/albumart?url=`
  (the resolve-and-redirect entry point) only for a not-yet-known url.
- **Album art fallback: MusicBrainz + the Cover Art Archive** (`internal/coverart.Fetcher`) — mpd can only
  ever report art that's actually embedded in a file's own tags; a huge share of tracks (anything with a
  bare/incomplete tag, or a lossy re-encode that dropped embedded art) have real cover art available online
  that mpd will never see. `artAdapter.resolve` (`cmd/pi-streamer/albumart.go`) only ever falls back to it
  when mpd's own `AlbumArt` call returned a **confirmed-empty** result (not on a transient error — see the
  `mpd.Error`-unwrapping note above, that distinction is exactly why the confirmed/error split exists) *and*
  both `artist`/`album` are known (a MusicBrainz release lookup needs at least those two to have any chance
  of matching; without them the fallback is skipped entirely, not attempted with empty strings). `Fetcher.
  Fetch(ctx, artist, album)`: `lookupReleaseID` queries MusicBrainz's search API
  (`/ws/2/release/?query=artist:"..." AND release:"..."`) for a release MBID, then `fetchFrontCover` asks
  the Cover Art Archive (`coverartarchive.org/release/<mbid>/front`) for the actual image bytes — two
  separate services, only the first of which is rate-limited by MusicBrainz's own usage policy.
  `waitForRateLimit` (a `sync.Mutex`-guarded `lastRequest` timestamp, `minInterval` defaulting to ~1.1s)
  throttles calls to the MusicBrainz search endpoint specifically, honoring their documented ≥1 req/sec
  limit — the Cover Art Archive fetch itself is not throttled, since it isn't covered by that policy. Every
  outgoing request sets a descriptive `User-Agent` (required by MusicBrainz's API terms; an unidentified
  client can be blocked outright). A lookup miss or fetch failure returns cleanly (empty bytes, no error, or
  an error that `artAdapter.resolve` only logs) rather than ever failing the whole `Resolve`/`Refresh`
  call — mpd's own confirmed-no-art answer is still a valid, cacheable result on its own; an unreachable
  third-party service should never turn that into an error response. Tested entirely against `httptest` fake
  servers for both endpoints (`internal/coverart/coverart_test.go`) — never a live call to the real
  MusicBrainz/Cover Art Archive services in this repo's test suite.
- **Re-resolving album art on demand** (`Refresh`, `POST /api/albumart/refresh`): `artAdapter.resolve`'s
  `force` parameter is what separates `Resolve` (checks the store first, skips mpd/MusicBrainz entirely if
  already known — the normal fast path) from `Refresh` (always re-fetches, overwriting whatever was
  cached) — useful when a file's been re-tagged with new art since it was last resolved, or when the
  MusicBrainz fallback is being tried for the first time on a track that was resolved (and cached as
  "no art") before that fallback existed. Exposed per-track as a "Re-fetch art" `Menu.Item` on
  `LibraryEntryGridCard.jsx`; `Library.jsx`'s `handleRefreshArt` calls it and stores the result in a local
  `artOverrides` map, layered on top of the batched `useAlbumArtStatus` query result
  (`{...queriedArtStatus, ...artOverrides}`) — the batched query only re-runs when the *set* of URLs on
  screen changes, not when one already-known answer is deliberately overwritten, so without this override
  the UI would keep showing the stale pre-refresh answer until some unrelated re-query happened to run.
- **Custom art as an explicit fallback tier, at track/album/artist scope** (`artstore.Store.PutCustom`/
  `IsCustom`/`Remove`, `artAdapter.SetCustomArt`/`ClearCustomArt`/`lookupCustomFallback`,
  `POST`/`DELETE /api/albumart/custom`) — for when auto-resolution (mpd, YouTube thumbnail, MusicBrainz)
  doesn't find the right art, or anything at all: the user supplies any URL that serves an image (fetched
  generically, no host restriction — it doesn't have to be YouTube/MusicBrainz/anything else this codebase
  otherwise knows how to pull art from) and it's recorded under one of three scopes:
  - **`"track"`** (key: the track's own URL) — always wins, checked *before* the force/cache gate in
    `resolve` so it's never silently replaced, not even by `Refresh`. `ClearCustomArt` is the only way back
    to auto-resolution for that track.
  - **`"album"`**/**`"artist"`** (key: the album/artist name, hashed under a synthetic
    `customArtKeyForAlbum`/`customArtKeyForArtist` key — never a real URL, so it can't collide with any
    track's own entry in the same content-addressed store) — only ever consulted as a *fallback*, when a
    track's own auto-resolution comes up with nothing; album is checked before artist (the more specific of
    the two). This is the actual "fallback system in case" auto-resolution fails, applied uniformly to every
    track sharing that album/artist rather than needing to be set per-track.
  `internal/artstore`'s `entry.Custom` field (persisted in the index alongside `Filename`/`HasArt`) is what
  makes this distinguishable from an auto-resolved entry at all — `IsCustom` is the only new accessor
  needed; `Put`/`Lookup`/`Query` themselves stay unaware that "custom" is even a concept. **Frontend**:
  `CustomArtControl.jsx` (a URL input + set/clear `ActionIcon`s) is shared verbatim across the
  track/album/artist detail pages in `Library.jsx` rather than duplicated three times, since the
  interaction is identical for all three scopes — only the `scope`/`artKey`/`label` props differ.
- **Metadata suggestions in the Library edit form, "baked into" the album art fetch system rather than a
  separate lookup client** (`internal/coverart.Fetcher.SearchMetadata`, `artAdapter.Suggest`,
  `POST /api/albumart/suggest`) — the same `*coverart.Fetcher` `artAdapter` already held for the art
  fallback gained a second method hitting MusicBrainz's *recording* search endpoint (`/recording/?query=`,
  distinct from `Fetch`'s *release* search) rather than wiring up a separate MusicBrainz client for this;
  `coverArtFetcher`'s interface (`cmd/pi-streamer/albumart.go`) grew `SearchMetadata` alongside `Fetch`
  accordingly. Each MusicBrainz "recording" result maps to one `coverart.MetadataSuggestion{Title, Artist,
  Album}` (`Title` direct, `Artist` from the first artist-credit, `Album` from the first release's title —
  a recording can appear on several releases; only the first is offered rather than fanning out one
  suggestion per release). `internal/api.Art` grew `Suggest(query) ([]MetadataSuggestion, error)` (its own
  mirrored type, same decoupling as `ArtStatus`/`Job`); `artAdapter.Suggest` returns `(nil, nil)` if no
  `coverArt` fetcher is configured at all, the same graceful-skip the art fallback itself already has.
  **Frontend**: `LibraryEntryForm.jsx`'s "Look up title/artist/album" button (shown for every edit, not
  just add — there is no add path left, see the unified Library UI note above) queries with whatever's
  already typed into Title/Artist/Album (joined into one string), falling back to a name derived from the
  URL's last path segment (`deriveQueryFromURL`, a JS cousin of the backend's own `deriveTitleFromURL`) when
  all three are still blank. Results render as clickable cards below the button; clicking one fills the
  three fields (only overwriting a field the suggestion actually has a value for) without saving —
  **suggestions are never applied automatically**, since MusicBrainz's free-text search returns its best
  guesses, not confirmed corrections, and the user still reviews/edits/Saves as normal afterward. Tested
  against `httptest` fake servers (`internal/coverart/coverart_test.go`, `cmd/pi-streamer/albumart_test.go`)
  — never a live call to the real MusicBrainz service, same as the existing art-fallback tests.
- **Generic background job tracking** (`internal/jobs`, new): `Warm` above is deliberately not a bare `go
  func(){}` reporting nothing — the previous design had no way to show progress or even confirm a warm scan
  was still running versus silently having died. `jobs.Manager.Start(name, func(h *jobs.Handle) error)`
  spawns the work in its own panic-recovering goroutine (same reasoning as `safeGo`, but returning a
  `*jobs.Job` handle immediately rather than just logging-and-swallowing) and tracks it as a `Job{ID, Name,
  Status: "running"|"done"|"error", Total, Done, StartedAt, EndedAt}`; the work body calls `h.SetTotal(n)`/
  `h.Advance(delta)` to report progress (both `Job` fields are zero-valued/omitted from JSON until first
  set, which is what lets the frontend distinguish "still discovering how much there is to do" —
  indeterminate progress bar — from "N of M done"). `Manager.List()` returns every tracked job, pruning the
  oldest *finished* ones once a cap is hit (`maxFinished`) — a running job is never pruned regardless of how
  long the list gets, only completed ones age out, so a slow scan can never be silently dropped from the
  list while still in flight. Deliberately in-memory only (job history doesn't need to survive a daemon
  restart) and deliberately generic — `Warm` is its first and only caller today, but any other future
  long-running operation (a bulk re-index, a bulk metadata re-fetch) can reuse the same `Manager` rather than
  inventing its own progress-tracking/goroutine-safety scheme. `internal/api` doesn't import `internal/jobs`
  directly — same "own copy of the shape, translate at the boundary" decoupling already used for
  `BucketStatus`/`ArtStatus`: `api.Job` is `internal/api`'s own mirrored struct, and `cmd/pi-streamer/jobs.go`'s
  `jobsAdapter` translates `jobs.Job` → `api.Job` for `GET /api/jobs` and the `"jobs"` WS envelope
  (`onChange` callback wired in `main.go`, pushed exactly like the existing `"status"`/`"downloads"`
  envelopes — see the `internal/ws.Hub` note above). `web/src/hooks/useDaemonSocket.js` exposes `jobs` as
  another piece of socket state; `App.jsx` shows a header `Badge` for any currently-`"running"` job, and
  `Settings.jsx` renders a specific progress bar for the "Warm album art cache" job by name/status match
  (`animated` — indeterminate — while `total` hasn't been set yet) and disables the warm button while one's
  already in flight, rather than relying on the backend to reject an overlapping call.
- **Panics in background goroutines used to crash the whole daemon** — `net/http` recovers a panic inside a
  request handler automatically (logs it, closes that connection, keeps running), but a bare `go func(){...}`
  spawned directly (prefetching, favorite archiving, the status ticker, the mpd idle watcher, the bucket file
  server) has no such safety net; an unrecovered panic there takes the entire process down. `cmd/pi-streamer`'s
  `safeGo(name, fn)` wraps every one of these with a `recover` that logs (`panic in %s: %v\n%s` plus a stack
  trace) instead of exiting — used everywhere a bare `go` used to appear in `main.go`/`bucket.go`.
- **Chrome extension talks straight to the daemon's HTTP API** (`extension/`, Manifest V3): a link's
  context menu ("Play now in Pi Streamer" / "Queue in Pi Streamer" — the *only* UI surface, deliberately;
  an earlier draft also had "play the current tab"/keyboard-shortcut variants, removed by explicit
  request since links are the actual use case) plus a popup for pasting a URL manually, both reusing
  `POST /api/play`/`POST /api/queue` (the exact same `{url}` JSON body `web/src/api.js` already sends) —
  no new endpoint needed for the play/queue calls themselves. The daemon has no CORS headers configured
  anywhere (`internal/api/router.go` — confirmed, not assumed), which would normally block a cross-origin
  `fetch` from any other origin — but Chrome exempts `fetch`/`XHR` made from an extension's *own* pages
  (its background service worker, popup, or options page) from CORS enforcement for any origin covered by
  the manifest's `host_permissions`, so this works against the plain HTTP API unmodified. That exemption
  only applies to requests the extension itself makes from those contexts — it is not a way for an
  arbitrary web page's own script to bypass CORS, and doesn't weaken the daemon's no-CORS-configured
  stance for anything else. `background.js`'s `sendToStreamer` is the one place that calls the daemon at
  all (the context menu and `popup.js`, the latter via a `chrome.runtime.sendMessage` round trip) — kept
  singular so the discovery-fallback/notification logic below exists exactly once.
  **Self-discovery, not a typed-in address** (`extension/discover.js`) — a Chrome extension has no mDNS/
  DNS-SD *browsing* API (no way to enumerate `_http._tcp` services), so `discoverStreamer()` tries a short
  list of common `.local` hostnames (`raspberrypi.local`/`pi-streamer.local`/`pi.local`): resolving a
  *specific, already-known* `.local` name is something the OS's own mDNS resolver does for free on any
  `fetch()`, which is why this works unmodified against a stock Raspberry Pi OS install (default hostname
  `raspberrypi.local`, Avahi already running) with zero setup on the Pi side and zero extra permissions
  here. `probeCandidate` requires a positive identification via `GET /api/discover` returning
  `{"service": "pi-streamer"}` — never accepting "something answered on this port," since an unrelated LAN
  device could be running any HTTP server on 8080.
  **A full subnet sweep was attempted, then deliberately dropped**: `chrome.system.network.
  getNetworkInterfaces()` would be the natural way to learn the extension's own subnet for a broader scan,
  but that API is restricted to packaged apps and isn't usable from a regular extension at all — confirmed
  against a real Chrome error ("'system.network' is only allowed for packaged apps"), not assumed from
  docs. The one workaround (forcing WebRTC's ICE candidates to leak real local IPs, via
  `chrome.privacy.network.webRTCIPHandlingPolicy` and the `"privacy"` permission) was raised and explicitly
  declined: it disables a real anti-fingerprinting protection *browser-wide*, for every tab, for as long as
  the extension stays installed — not a cost worth paying for a fallback that still isn't guaranteed to
  work on every OS/network. Anything mDNS doesn't resolve (a custom Pi hostname, a network without mDNS)
  falls back to typing the address into `options.html` manually — plain, no privacy tradeoff, and already
  needed as a fallback regardless. **The one real daemon-side addition**:
  `handleDiscover`/`discoveryResponse` (`internal/api/handlers.go`, registered as `GET /api/discover` in
  `router.go`) — deliberately static and dependency-free, touching neither `Player` nor mpd, so it answers
  instantly even if mpd itself isn't connected (every *other* endpoint either needs a live mpd connection
  or at least the `Player` wiring; discovery needs to work regardless, since "is this even pi-streamer" has
  to be answerable before anything else is known about the daemon's state). `sendToStreamer` treats
  discovery as automatic, self-healing fallback, not just a manual options-page action: no saved address at
  all, or a fetch to the saved one failing with a network-level error specifically (a `TypeError` — the
  daemon's unreachable at that address — as opposed to an application error, where the daemon responded
  and just rejected the URL, which rediscovery can't fix) triggers `discoverStreamer()`, and a hit is saved
  back to `chrome.storage.sync` and the original request retried — covering the daemon's address changing
  (DHCP lease renewal) without the user ever noticing. `options.html`'s "Discover automatically" button
  runs the identical function for a manual on-demand trigger, and "Test connection" also hits
  `GET /api/discover` now rather than `GET /api/queue`, for the same "positively identify pi-streamer, not
  just anything that answered" reasoning, and because it doesn't require mpd to be connected either.
  **Feedback via `chrome.notifications`**, not a toolbar badge: a context-menu click has no popup open to
  show a status message in, so `notify()` posts a native OS notification (`"notifications"` permission)
  confirming success/failure for every `sendToStreamer` call — including "nothing found on this network,"
  which also opens the options page so the user isn't left guessing why nothing happened.
  **The success notification names the actual track** (`fetchTrackInfo`/`describeTrack`/`notifySuccess`),
  not a generic "Now playing."/"Added to queue." — backed by the one other daemon-side addition this
  extension needed, `GET /api/track?url=` → `Player.TrackInfo` (`internal/player/player.go`): a synchronous,
  on-demand title/artist/album lookup for a URL the caller already knows, distinct from `Status`/`Queue`'s
  job of describing whatever mpd is currently doing — checks the search index (`Indexer.Get`), falling back
  to `deriveTitleFromURL` for the title alone if unindexed, and never triggers a fetch/enrichment itself.
  Because a URL *just* submitted may still be enriching in the background (`indexAsync`/`enrichMetadata` —
  see above), `fetchTrackInfo` gives it one short second chance (a 1.2s wait, then one retry) if the first
  answer has no artist and no album at all, rather than settling immediately for a thin, still-enriching
  result.
  **YouTube thumbnail overlay** (`extension/content.js`+`content.css`, a Manifest V3 content script matched
  to `https://www.youtube.com/*`) — right-clicking every individual video was still one context-menu round
  trip too many for the actual common case (browsing YouTube, wanting to send several videos to the
  daemon); this overlays a ▶/+ button pair directly on each thumbnail instead. YouTube is a single-page
  app — thumbnails load continuously while scrolling infinite feeds, and navigating between pages never
  reloads the document at all — so a one-time DOM pass on load isn't enough; a `MutationObserver` on
  `document.documentElement` watches for newly added thumbnails for as long as the tab stays open.
  `a#thumbnail`/`a.ytd-thumbnail` covers grid tiles, search results, sidebar recommendations, playlist
  rows, and Shorts uniformly; each anchor's `href` is canonicalized to a plain `watch?v=<id>`/`shorts/<id>`
  URL and marked processed (`data-pi-streamer-done`) so the observer never double-attaches an overlay to
  the same thumbnail as YouTube's own JS mutates the DOM around it. The overlay's buttons send the exact
  same `{type: 'send-to-streamer', url, mode}` message `background.js` already handles for the context
  menu and popup — `background.js` needed zero changes for this, it was already generic over *how* a
  request arrives. Click handling is capture-phase with `stopImmediatePropagation`, not just
  `preventDefault`/`stopPropagation`: YouTube attaches its own "navigate to this video" handling very close
  to the anchor itself, and a plain bubbling listener wasn't reliably early enough to stop it. This is the
  one piece of the extension that reaches into a third-party page's DOM at all — everything else
  (background/popup/options) only ever talks to the daemon's own API.
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
  after the Drive removal above — `OLED` (`Port`/`Baud`/`ElapsedUpdateIntervalSeconds`), `Bucket`, and `UI`
  (currently just `HideVolumeControl`), with `Config` structured so more settings can be added later without
  a new mechanism. `Store.Get`/`Set`/`Reload` are the whole API; `Set` persists to disk, `Reload` re-reads
  the file (picking up a hand-edit made directly on the Pi, e.g. over SSH) — neither is wired to *do*
  anything by itself. `cmd/pi-streamer/oled.go`'s `configAdapter` is what bridges `Store` to
  `internal/api.Config` and gives `Set`/`Reload` their live effect, by calling `oledManager.reconfigure` with
  the new port/baud on every change (an empty port disconnects) — `UI`'s fields have no such daemon-side
  effect at all; the frontend just reads them back via the same `GET /api/config` and applies them itself
  (see below). This is the **web UI's actual mechanism for configuring the OLED display**:
  `web/src/components/Settings.jsx` calls `GET/PUT /api/config` and `POST /api/config/reload`, plus
  `GET /api/oled/status` and `GET /api/oled/ports` (serial port auto-detection via `internal/serial.
  ListPorts`, wrapping `go.bug.st/serial.GetPortsList`) so the user picks a port from a dropdown rather than
  typing a device path. There are deliberately no separate connect/disconnect endpoints — connecting *is*
  the side effect of setting `oled.port` in config. Default path: `-config-path` (default `config.json`,
  written `0600`, gitignored).
  **`Settings.jsx` is now a sidebar, not one long scrolling page** — General (Library view mode, Album Art
  warming, the volume-control toggle below), OLED Display, and Cache (Audio Bucket config/usage plus
  `Bucket.jsx`'s storage browsing) are each their own section, picked via a `NavLink` list (`section` state,
  persisted the same way Library's own view state is) rather than Library's horizontally-scrolling `Chip`
  row — Settings is reached through a full-screen `Modal` with real width to spare, not Library's
  mobile-first bottom-nav context. Save/Reload stay in a shared header above the sidebar+content split
  (not duplicated per section), since one `PUT /api/config` call always covers every field regardless of
  which section is currently showing.
  **`OLED.ElapsedUpdateIntervalSeconds`** throttles only `cmd/pi-streamer/main.go`'s 1s status-ticker's own
  push to the display — the *idle-watcher*-triggered `broadcastStatus()` (a track/play/pause/volume change
  reported by mpd itself) is a separate, unconditional call, entirely unaffected by this setting, so a real
  change always reaches the display immediately regardless of how this is configured; only "just the
  elapsed seconds ticking, nothing else changed" respects the interval. Zero (unconfigured) means
  `config.DefaultElapsedUpdateIntervalSeconds` (1, the original hardcoded behavior).
  **`UI.HideVolumeControl`** hides the volume slider in `PlayerBar.jsx`/`NowPlayingScreen.jsx` — a
  web-UI-only preference (not touching the OLED at all, despite being asked for in the same breath as the
  interval setting above): the OLED's own 256×64 layout is already fully packed edge-to-edge (title/artist/
  album/progress bar/elapsed-time all using every available pixel row), so a volume indicator there would
  need careful repositioning that can't be visually verified without the real device — asked about directly
  and deliberately scoped to the web UI instead. `App.jsx` fetches `GET /api/config` once on mount and again
  whenever the Settings `Modal` closes (not pushed over `/ws` like status/downloads/jobs — this changes rarely
  enough that fetch-on-close is simpler than a fourth envelope type for it), passing `ui.hideVolumeControl`
  down to both components as a prop.
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
architecture note), `-ytdlp-path` (default `""`, resolving `yt-dlp` via `PATH` — the yt-dlp executable used
to extract audio from YouTube URLs, see the YouTube-URL architecture note; a submitted YouTube URL just
fails to play, per-request, if yt-dlp isn't installed at all, rather than the daemon refusing to start over
it) — these are just *where on disk*/*what address*/*what binary* things live; none of the OLED port/baud or
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
