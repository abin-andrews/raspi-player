// Shared LAN discovery logic for the Pi Streamer daemon — used by both
// background.js (an automatic fallback when the saved address stops
// responding) and options.js (the "Discover automatically" button).
//
// Chrome extensions have no mDNS/DNS-SD browsing API and no access to
// chrome.system.network (that's restricted to packaged apps, not regular
// extensions — confirmed against a real "'system.network' is only allowed
// for packaged apps" error, not assumed), so a full subnet sweep isn't an
// option here without a much heavier workaround (forcing WebRTC to leak
// real local IPs via the "privacy" permission, which weakens a real
// anti-fingerprinting protection browser-wide for as long as the extension
// is installed) — deliberately not done, given the tradeoff. Instead, this
// only tries common .local hostnames: resolving a *specific, already-known*
// hostname is something the OS's own mDNS resolver already does for free
// on any fetch(), which is why this works out of the box against a stock
// Raspberry Pi OS install (default hostname raspberrypi.local, Avahi
// already running) with zero extra setup on the Pi side and zero extra
// permissions here. probeCandidate only accepts a match that positively
// identifies itself via GET /api/discover — never just "something
// answered on this port," since another LAN device could be running an
// unrelated HTTP server on the same port. Anything not covered by these
// hostnames falls back to manual entry in the options page.
//
// Loaded as a classic (non-module) script in both contexts —
// background.js via importScripts, options.html via a plain <script> tag
// — so these stay ordinary global functions rather than needing
// import/export plumbing.

const DISCOVERY_PORT = 8080
const CANDIDATE_HOSTNAMES = ['raspberrypi.local', 'pi-streamer.local', 'pi.local', 'raspberrypi']
const PROBE_TIMEOUT_MS = 800

async function probeCandidate(host, port) {
  const baseUrl = `http://${host}:${port}`
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), PROBE_TIMEOUT_MS)
  try {
    const resp = await fetch(`${baseUrl}/api/discover`, { signal: controller.signal })
    if (!resp.ok) return null
    const data = await resp.json()
    return data?.service === 'pi-streamer' ? baseUrl : null
  } catch {
    return null
  } finally {
    clearTimeout(timer)
  }
}

// discoverStreamer tries every candidate hostname in parallel and returns
// the first match, or null if none of them are pi-streamer.
async function discoverStreamer() {
  const results = await Promise.all(CANDIDATE_HOSTNAMES.map((h) => probeCandidate(h, DISCOVERY_PORT)))
  return results.find(Boolean) || null
}
