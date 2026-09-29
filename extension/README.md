# Pi Streamer Remote (Chrome extension)

Right-click any link to play or queue it on your pi-streamer daemon — no need to open the web UI, and no
need to know the daemon's address ahead of time. Talks directly to the daemon's existing HTTP API
(`POST /api/play`, `POST /api/queue`, `GET /api/discover` — see `internal/api/handlers.go`); no other
daemon-side changes are needed.

## Install (unpacked)

1. Go to `chrome://extensions`, enable "Developer mode" (top right).
2. "Load unpacked" → select this `extension/` directory.
3. That's it — no configuration required for the common case. The first time you use it, it'll
   self-discover the daemon on your network (see below) and remember the address. If you'd rather set it
   explicitly (or discovery doesn't find it), click the extension's icon → "Configure daemon address".

## Use

- **On youtube.com**: hover any video thumbnail (home page, search results, channel pages, sidebar
  recommendations, Shorts) for a ▶/+ button pair overlaid directly on it — one click to play or queue that
  video, no need to open it or right-click first. This is `content.js`, injected only on
  `https://www.youtube.com/*`.
- **Right-click a link** on any other page → "Play now in Pi Streamer" / "Queue in Pi Streamer".
- **Popup** (click the toolbar icon): paste any URL to Play/Queue it directly, or open the options page.
- A native OS notification confirms success or failure for every action (`chrome.notifications`) — on
  success, it names the actual track (title, and artist/album when known), fetched via the daemon's
  `GET /api/track?url=`. A track added moments ago may still be enriching in the background, so the
  notification waits up to ~1.2s for a fuller answer before falling back to whatever's known.

## Self-discovery

`discover.js` tries a short list of common `.local` hostnames (`raspberrypi.local`, `pi-streamer.local`,
`pi.local`) — resolved by your OS's own mDNS resolver, which is why this works out of the box against a
stock Raspberry Pi OS install (its default hostname is `raspberrypi.local`) with no extra setup on the Pi
at all, and no special permissions here. A match only counts if it answers `GET /api/discover` with
`{"service": "pi-streamer"}` — never just "something answered on this port," since another LAN device
could be running an unrelated HTTP server on the same port.

This runs automatically: if no address is saved yet, or the saved one stops responding (e.g. the Pi's
DHCP lease changed), `background.js` reruns discovery and updates the saved address before retrying —
you only see this if it fails to find anything, via a notification pointing you at the options page. You
can also trigger it manually from the options page's "Discover automatically" button.

**A full subnet scan was considered and deliberately left out**: extensions can't use
`chrome.system.network` (that's restricted to packaged apps — confirmed against a real Chrome error, not
assumed), and the one workaround (forcing WebRTC to leak real local IPs via the `"privacy"` permission)
weakens a real anti-fingerprinting protection browser-wide for as long as the extension stays installed,
for a fallback that still wouldn't be guaranteed to work everywhere. If your Pi has a custom hostname or
mDNS just doesn't resolve on your network, enter the address manually in the options page instead —
simpler and no privacy tradeoff.

## Why no server changes were needed for the play/queue calls

Chrome exempts `fetch`/`XHR` made from an extension's own pages (background service worker, popup,
options page) from CORS enforcement for any origin covered by the manifest's `host_permissions` — that's
why this works against the daemon's plain HTTP API with no CORS headers added on the Go side. This only
covers requests made from the extension itself, never from an arbitrary web page's own script. The one
actual server-side addition is `GET /api/discover` — a small, static, dependency-free identity endpoint
(doesn't touch mpd at all, so it answers even if mpd itself isn't connected) purely so a scan can tell
"this is pi-streamer" apart from any other HTTP server on the LAN.

## YouTube thumbnail overlay (`content.js`)

Injected into every `https://www.youtube.com/*` page (`manifest.json`'s `content_scripts`, no extra
permission needed beyond that `matches` entry — `host_permissions` already covers it, but a content
script's injection is its own separate mechanism regardless). YouTube is a single-page app: thumbnails
load continuously as you scroll (infinite feeds) and navigating between pages never reloads the document
at all, so a one-time pass on load isn't enough — a `MutationObserver` watches for newly added thumbnails
the whole time the tab is open. Each thumbnail anchor (`a#thumbnail`/`a.ytd-thumbnail`, covering grid
tiles, search results, sidebar recommendations, playlist rows, and Shorts) gets its href canonicalized to
a plain `watch?v=`/`shorts/` URL and a small overlay (`content.css`) appended to its `ytd-thumbnail`
container, shown on hover. The overlay's buttons send the exact same `{type: 'send-to-streamer', url,
mode}` message `background.js` already handles for the context menu and popup — `background.js` itself
needed zero changes for this. Clicks use a capture-phase listener with `stopImmediatePropagation`, since
YouTube's own click handling on a thumbnail is attached very close to the anchor itself — a plain
bubbling listener isn't reliably early enough to stop it from also navigating to the video.

## Notes

- The daemon has no authentication, so this only works on your LAN (or wherever the daemon's address is
  actually reachable from the machine running Chrome) — same trust boundary as the web UI itself.
- `host_permissions` is broad (`http://*/*`, `https://*/*`) since the daemon's address (and the manually
  pasted URLs this extension plays/queues) can be anything on the LAN.
- Discovery assumes the daemon listens on port 8080 (`cmd/pi-streamer`'s default `-http-addr`) — edit
  `DISCOVERY_PORT` in `discover.js` if your setup differs.
