# pi-streamer

A self-hosted network audio player for a Raspberry Pi. Paste a URL (an internet radio stream, a direct
audio file, or a YouTube link) into a web UI or a Chrome extension, and it plays through whatever DAC/
speakers are attached to the Pi — with playlists, favorites, history, full-text search, and an optional
physical OLED display showing what's playing.

## Features

- **Play anything with a URL**: direct audio streams, internet radio, or YouTube links (audio is
  extracted via `yt-dlp` and cached locally).
- **Web UI** (React + Mantine): browse/search your library, manage playlists and favorites, see play
  history, control playback (play/pause/seek/volume) from any device on your network.
- **Chrome extension**: right-click any link — or, on youtube.com, just hover a video thumbnail — to
  play or queue it on the daemon directly, with no need to open the web UI at all. See
  [`extension/README.md`](extension/README.md).
- **Full-text search** over everything you've ever played, favorited, or playlisted, backed by a
  separate SQLite/FTS5 indexing service (so the Pi itself doesn't need to do the heavy lifting).
- **Album art**, resolved from the audio file itself, YouTube's thumbnail, or a MusicBrainz/Cover Art
  Archive lookup — with a manual override if none of those find the right image.
- **Offline/"download-then-stream" caching**: an LRU cache of downloaded audio as an alternative to
  streaming a URL live, plus a separate permanent archive for favorited tracks.
- **Optional physical display**: an Arduino + OLED screen (wired up over USB) mirrors now-playing
  info, configurable entirely from the web UI's Settings screen.

## Architecture

```
web (React) ─┐
              ├─► pi-streamer daemon (Go) ─► mpd ─► your speakers/DAC
extension ────┘         │
                         └─► search-indexer (Go, SQLite/FTS5) — runs on any machine, not necessarily the Pi
```

The daemon drives [`mpd`](https://www.musicpd.org/) over its network protocol, serves the web UI's HTTP+
WebSocket API, and optionally talks to a separate `search-indexer` service for full-text search (this can
run on a more capable machine than the Pi itself — the search index is the one thing this project
deliberately doesn't try to make the Pi Zero do). See [`CLAUDE.md`](CLAUDE.md) for the full internal
architecture, package-by-package layout, and the reasoning behind most of the non-obvious design
decisions — that file is written as a living design doc, not just AI-assistant instructions, so it's
worth reading if you're extending this project.

## Requirements

- A Raspberry Pi (developed against a Pi Zero 2 W, but anything running Linux + `mpd` works) with a DAC
  or any audio output `mpd` can drive via ALSA.
- [`mpd`](https://www.musicpd.org/)/`mpc`, [`yt-dlp`](https://github.com/yt-dlp/yt-dlp), and `ffmpeg`
  installed (`make install-deps`/`make install-deps-pi` handle this via `apt-get`).
- Go 1.25+ and Node.js (for building the daemon and the web UI) — only needed on your dev machine; the
  Pi only ever runs the compiled binary + static frontend files.

## Quickstart (development)

```sh
make install-deps      # Go toolchain + mpd/mpc/yt-dlp/ffmpeg (Debian/Ubuntu, needs sudo)
make web-install        # frontend dependencies
make dev                # builds + runs the daemon, search-indexer, and frontend dev server together
```

`make dev` needs a local `mpd` instance to control — point it at one with an `audio_output` block
configured in `mpd.conf` (see `CLAUDE.md`'s mpd note if playback silently produces no sound). Then open
the printed local URL in a browser.

Individual pieces, if you'd rather run them separately:

```sh
make build && make run          # daemon only (native binary)
make run-indexer                 # search-indexer only
make web-build                   # frontend production build, served by the daemon itself
```

Run a single Go test: `go test ./internal/player/ -run TestName -v`. The whole suite (`make test`) needs
no real `mpd`/SQLite server — every package is tested against fakes.

## Deploying to a real Pi

```sh
make build-pi              # or build-pi64 for a 64-bit Raspberry Pi OS image
make deploy-pi PI_HOST=pi@your-pi.local   # ships the binary + frontend, installs a systemd service
```

`make install-deps-pi` does the one-time `apt-get install` of `mpd`/`mpc`/`yt-dlp`/`ffmpeg` on the Pi over
SSH first. mpd's `audio_output` block (which ALSA device to actually play through) is hardware-specific
and left for you to configure by hand — see `CLAUDE.md`'s mpd notes for exactly why and how. Run
`make help` for the full command list.

## Browser extension

[`extension/`](extension/) is a separate, optional Chrome extension for playing/queuing links (including
a one-click overlay on YouTube thumbnails) without opening the web UI. It self-discovers the daemon on
your LAN — see [`extension/README.md`](extension/README.md) for installation and usage.

## License

MIT — see [`LICENSE`](LICENSE).
