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
on-disk JSON settings store backing that Settings tab (currently just the OLED port/baud) — the daemon's only
persistent/disk state right now, and deliberately structured so more settings can be added to it later.
Update this file as the project grows; don't let it drift from reality.

## Purpose

pi-streamer is a network audio player for a Raspberry Pi Zero 2W:

- A daemon runs on the Pi, drives `mpd`/`mpc` to play audio from arbitrary URLs, and outputs sound through
  an attached DAC.
- A web UI lets a user submit a URL to play, control playback (play/pause/etc.), and browse playlists,
  favorites, and play history.
- A separate system (not the Pi) hosts a SQLite (FTS5) database with search indexing over the URL
  collection, since the Pi Zero 2W is too resource-constrained to do fast search itself. It's exposed via
  its own HTTP API (`cmd/search-indexer`: `POST /index`, `GET /search`), and the Pi daemon calls it over the
  network via `internal/indexer` (an HTTP client — deliberately not importing `internal/search` directly, to
  keep `modernc.org/sqlite` out of the main daemon binary). The daemon best-effort indexes a URL whenever it's
  played, favorited, or added to a playlist (failures don't fail the primary action — indexing is a
  nice-to-have); `GET /api/search` on the daemon proxies to it.

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
internal/search/       SQLite/FTS5 search index (modernc.org/sqlite). Used by cmd/search-indexer.
internal/indexer/      HTTP client the daemon uses to talk to cmd/search-indexer (IndexURL, Search). Own
                        json-tagged Result type, independent of internal/search.Result (no sqlite dependency
                        pulled into the main daemon binary). player.Indexer interface wraps it for testability.
internal/serial/       Generic line-based serial transport (go.bug.st/serial, pure Go/no CGO — same ARM
                        cross-compilation constraint as internal/search's sqlite driver): Open a port at a
                        baud rate, Send a line, get back one reply line, ListPorts to enumerate devices.
                        Knows nothing about any specific device's command vocabulary — see Architecture
                        notes for the OLED protocol it carries.
internal/config/       Small on-disk JSON settings store (currently just the OLED port/baud) — Get/Set/
                        Reload. See Architecture notes; this is the app's only persistent/disk state.

web/                    React (Vite) + Mantine player UI: Now Playing, Queue, Search, Favorites, Playlists,
                        and History tabs, plus a persistent PlayerBar (transport/skip/volume) shown on every
                        tab. Icons via @tabler/icons-react.
                        web/src/hooks/usePlaybackStatus.js is the single WebSocket connection per tab (called
                        once in App.jsx, status passed down as a prop) — components needing live status must
                        NOT open their own connection or poll. App.jsx doesn't render any tab content at all
                        until that hook reports ready (see Architecture notes) — a loading screen shows until
                        then.
```

## Architecture notes for future work

- Keep playback logic (talking to mpd via `gompd`) and the HTTP API layer separate in the Go daemon — the
  daemon is the only thing with direct `mpd` access; the frontend never talks to mpd directly. `internal/api`
  depends on the `Player` interface it defines itself (not `internal/player`'s concrete type), so handlers
  stay testable against a fake.
- `internal/ws.Hub` broadcasts playback-state changes to all connected UI clients so multiple browser
  tabs/devices stay in sync without each one polling — important given the Pi Zero 2W's limited resources.
  Fed by two triggers in `cmd/pi-streamer/main.go`, both funneled through one `broadcastStatus()` closure:
  an `internal/mpdclient.StatusWatcher` (wraps `gompd`'s `idle`-protocol `Watcher` on its own dedicated mpd
  connection) broadcasting immediately on `"player"`/`"mixer"` subsystem changes, and a 1s ticker that
  broadcasts only while `state == "play"` (idle events alone only fire on transitions, not the continuous
  passage of time — needed for a smoothly moving progress bar). This means the *daemon* still polls mpd once
  a second while playing; what's eliminated is every browser tab independently polling the HTTP API.
  `Hub.ServeWS(w, r, initial []byte)` sends a newly connecting client its current status immediately (queued
  before registration, so it's guaranteed to arrive before any later broadcast) rather than leaving it to wait
  for the next unrelated state change — `web/src/hooks/usePlaybackStatus.js` gates on receiving this first
  message (`ready`) before `App.jsx` renders the real UI at all, so a page reload while something's playing
  never shows a stale/default flash that then jumps to correct values.
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
- `internal/api`'s HTTP surface beyond the original CRUD: `POST /api/seek {seconds}` (absolute),
  `POST /api/seek/relative {seconds}` (skip ±N, positive or negative), `POST /api/next`, `POST /api/previous`,
  `POST /api/volume {volume}` (clamped 0-100 in `internal/player`), `GET /api/search?q=&limit=`,
  `GET /api/albumart?url=` (raw image bytes, `Content-Type` via `http.DetectContentType`; any error or no-art
  returns a plain `404`, not a JSON error body — art-not-found is the common case for radio streams, and the
  frontend's `<img onError>` fallback is the only path it needs), and the mpd-queue routes:
  `GET /api/queue`, `POST /api/queue {url}` (adds without playing — distinct from `POST /api/play`),
  `DELETE /api/queue/{id}`, `POST /api/queue/{id}/move {position}`, `POST /api/queue/{id}/play`,
  `DELETE /api/queue` (clear all). There's no backend "mute" — the frontend
  remembers the last non-zero volume locally and toggles `POST /api/volume {0}` / restore, purely client-side.
  Settings routes (see `internal/config`/OLED notes below): `GET`/`PUT /api/config` (the whole settings
  object; `PUT` persists and applies live), `POST /api/config/reload` (re-read the file from disk),
  `GET /api/oled/status`, `GET /api/oled/ports` (serial port auto-detection for the UI's dropdown).
- **Google Drive integration removed** (was `internal/drive`, plus a Drive tab in the frontend and
  `-public-base-url`/`-drive-config-path`/`-drive-redirect-url` flags/`DRIVE_CLIENT_ID`/`DRIVE_CLIENT_SECRET`
  env vars in `main.go`): pulled out for now to drop `google.golang.org/api` (+ its gRPC/OpenTelemetry
  transitive deps), which had roughly **doubled** the daemon's ARM binary size (~9.8MB → ~21MB) for a feature
  most deployments don't use. May come back later behind a lighter-weight direct-REST-call client instead of
  the generated one. Its removal briefly left the daemon with no persistent/disk state at all; `internal/config`
  (below) has since reintroduced a small one, for OLED settings.
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
make deploy-pi           # build-pi + scp to PI_HOST (default pi@raspberrypi.local)
make deploy-pi64         # build-pi64 + scp to PI_HOST

# Frontend (web/, React + Vite)
make web-install        # npm --prefix web install
make web-build           # npm --prefix web run build
make web-test            # npm --prefix web run test --if-present
```

To run a single Go test: `go test ./internal/player/ -run TestPauseAndResume -v` (same pattern for any
package). `GOARM=6` targets the Pi Zero's ARMv6 baseline; Zero 2W's Cortex-A53 also supports ARMv7/64-bit
if the deployed OS image turns out to be 64-bit — override with `GOARM=7` or use `make build-pi64` instead.

`cmd/pi-streamer` flags beyond the earlier ones: `-config-path` (default `config.json`) — the OLED display's
serial port/baud aren't flags at all; they're configured live through the web UI's Settings tab (or by hand-
editing this file and calling `POST /api/config/reload`), not at startup, so no daemon restart is needed to
plug in a display, change ports, or turn it off. The daemon runs fine with no OLED section configured; the
display integration is simply skipped. `make run` auto-loads a `.env` file from the repo root if present, for
any future secrets — none are currently needed (`.env` is gitignored; never commit real credentials).
