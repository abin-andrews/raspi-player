// Service worker: owns the link context menu and the actual fetch() calls
// to the daemon's HTTP API (POST /api/play, POST /api/queue — see
// internal/api/handlers.go for the exact {url} body shape). Talking to the
// daemon from here (an extension page context), rather than from a
// content script injected into the target page, is what lets this work
// with no server-side changes at all: Chrome exempts fetch/XHR made from
// an extension's own pages from CORS enforcement for any origin covered by
// the manifest's host_permissions, so the daemon needs no CORS headers of
// its own.

importScripts('discover.js')

const MENU_PLAY_LINK = 'pi-streamer-play-link'
const MENU_QUEUE_LINK = 'pi-streamer-queue-link'

chrome.runtime.onInstalled.addListener(() => {
  chrome.contextMenus.create({
    id: MENU_PLAY_LINK,
    title: 'Play now in Pi Streamer',
    contexts: ['link'],
  })
  chrome.contextMenus.create({
    id: MENU_QUEUE_LINK,
    title: 'Queue in Pi Streamer',
    contexts: ['link'],
  })
})

async function getBaseUrl() {
  const { baseUrl } = await chrome.storage.sync.get('baseUrl')
  return (baseUrl || '').replace(/\/+$/, '')
}

async function notify(title, message) {
  try {
    await chrome.notifications.create({
      type: 'basic',
      iconUrl: 'icons/icon128.png',
      title,
      message,
    })
  } catch {
    // notifications can be disabled at the OS level; silently drop rather
    // than fail the whole action over a feedback mechanism.
  }
}

async function notifySuccess(baseUrl, url, mode) {
  const actionLabel = mode === 'queue' ? 'Added to queue' : 'Now playing'
  const info = await fetchTrackInfo(baseUrl, url)
  const { title, message } = describeTrack(actionLabel, info)
  await notify(title, message)
}

async function postToStreamer(baseUrl, path, url) {
  const resp = await fetch(baseUrl + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url }),
  })
  if (!resp.ok) {
    const body = await resp.text().catch(() => '')
    throw new Error(`daemon returned ${resp.status}${body ? `: ${body}` : ''}`)
  }
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

// fetchTrackInfo asks GET /api/track (see internal/api/handlers.go's
// handleTrackInfo) for url's title/artist/album, for describing it in the
// success notification rather than a generic "Now playing."/"Added to
// queue." A URL that was *just* submitted may still be getting enriched in
// the background (internal/player's indexAsync/enrichMetadata — a
// YouTube title fetch in particular is one more network round trip on top
// of that), so a first empty-looking answer (no artist and no album) is
// given one short second chance before settling for whatever came back.
async function fetchTrackInfo(baseUrl, url) {
  const lookup = async () => {
    try {
      const resp = await fetch(`${baseUrl}/api/track?url=${encodeURIComponent(url)}`)
      if (!resp.ok) return null
      return await resp.json()
    } catch {
      return null
    }
  }
  let info = await lookup()
  if (info && !info.artist && !info.album) {
    await sleep(1200)
    info = (await lookup()) || info
  }
  return info
}

// describeTrack turns a trackInfoResponse into a notification's
// title/message — a track title alone (no artist/album known yet, or
// ever, for an untagged stream) falls back to just the action label.
function describeTrack(actionLabel, info) {
  if (!info?.title) return { title: 'Pi Streamer', message: actionLabel }
  const context = [info.artist, info.album].filter(Boolean).join(' — ')
  return {
    title: actionLabel,
    message: context ? `${info.title}\n${context}` : info.title,
  }
}

// sendToStreamer is the one place that actually calls the daemon — every
// entry point (context menu, popup) funnels through this, so the
// discovery-fallback/notification logic only needs to exist once. If no
// address is configured yet, or the configured one no longer responds
// (e.g. the Pi's DHCP lease changed), it self-discovers a fresh one on the
// LAN (see discover.js) before giving up — this is what makes the daemon's
// address something the user only ever has to set once, if that.
async function sendToStreamer(url, mode) {
  const path = mode === 'queue' ? '/api/queue' : '/api/play'
  let baseUrl = await getBaseUrl()

  if (baseUrl) {
    try {
      await postToStreamer(baseUrl, path, url)
      await notifySuccess(baseUrl, url, mode)
      return { ok: true }
    } catch (err) {
      // Fall through to rediscovery only for a network-level failure
      // (fetch itself threw — daemon unreachable at this address); an
      // application-level error (daemon responded, just refused the URL)
      // means the address is fine and rediscovering wouldn't help.
      if (!(err instanceof TypeError)) {
        await notify('Pi Streamer — failed', err.message || String(err))
        return { ok: false, error: err.message || String(err) }
      }
    }
  }

  const discovered = await discoverStreamer()
  if (!discovered) {
    const message = baseUrl
      ? "Couldn't reach Pi Streamer, and nothing matching it was found on this network either."
      : 'No Pi Streamer address configured, and nothing was found on this network. Set one in the extension options.'
    await notify('Pi Streamer — not found', message)
    if (!baseUrl) chrome.runtime.openOptionsPage()
    return { ok: false, error: message }
  }

  await chrome.storage.sync.set({ baseUrl: discovered })
  try {
    await postToStreamer(discovered, path, url)
    await notifySuccess(discovered, url, mode)
    return { ok: true }
  } catch (err) {
    await notify('Pi Streamer — failed', err.message || String(err))
    return { ok: false, error: err.message || String(err) }
  }
}

chrome.contextMenus.onClicked.addListener((info) => {
  if (info.menuItemId === MENU_PLAY_LINK) sendToStreamer(info.linkUrl, 'play')
  if (info.menuItemId === MENU_QUEUE_LINK) sendToStreamer(info.linkUrl, 'queue')
})

// Lets popup.js reuse the exact same send path (discovery fallback,
// notifications, and all) instead of duplicating this logic.
chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  if (message?.type === 'send-to-streamer') {
    sendToStreamer(message.url, message.mode).then(sendResponse)
    return true
  }
})
