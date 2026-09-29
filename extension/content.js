// Injected into youtube.com: overlays a Play/Queue button pair on every
// video thumbnail, so a video can go to Pi Streamer with one click instead
// of needing the link's right-click context menu. YouTube is a heavy
// single-page app — thumbnails are added continuously as the user scrolls
// (infinite feeds) and navigating between pages never reloads the document
// at all — so a MutationObserver, not a single pass on load, is what keeps
// this working while actually browsing. Reuses the exact same
// 'send-to-streamer' message background.js already handles for the
// context menu/popup — no background.js changes needed for this at all.

const PROCESSED_ATTR = 'data-pi-streamer-done'
const THUMBNAIL_ANCHOR_SELECTOR = 'a#thumbnail, a.ytd-thumbnail'

// videoUrlFromAnchor canonicalizes a thumbnail's href to a plain watch
// URL (or a Shorts URL) — the same shape internal/urlnorm.Normalize
// collapses any YouTube link to server-side, so this doesn't need to
// carry along tracking params like a raw copied link would.
function videoUrlFromAnchor(anchor) {
  let url
  try {
    url = new URL(anchor.href, location.href)
  } catch {
    return null
  }
  if (url.pathname === '/watch') {
    const id = url.searchParams.get('v')
    return id ? `https://www.youtube.com/watch?v=${id}` : null
  }
  if (url.pathname.startsWith('/shorts/')) {
    return `https://www.youtube.com${url.pathname}`
  }
  return null
}

function sendAction(url, mode, button) {
  button.classList.remove('pi-streamer-ok', 'pi-streamer-err')
  button.classList.add('pi-streamer-pending')
  chrome.runtime.sendMessage({ type: 'send-to-streamer', url, mode }, (result) => {
    button.classList.remove('pi-streamer-pending')
    button.classList.add(result?.ok ? 'pi-streamer-ok' : 'pi-streamer-err')
    setTimeout(() => button.classList.remove('pi-streamer-ok', 'pi-streamer-err'), 1500)
  })
}

function makeButton(kind, label, glyph, url) {
  const button = document.createElement('button')
  button.type = 'button'
  button.className = `pi-streamer-btn pi-streamer-${kind}`
  button.title = label
  button.textContent = glyph
  // capture-phase + stopImmediatePropagation: YouTube's own thumbnail
  // click handling is attached very close to the anchor itself, so a
  // plain bubbling listener isn't reliably enough to stop it from also
  // navigating to the video.
  button.addEventListener(
    'click',
    (e) => {
      e.preventDefault()
      e.stopPropagation()
      e.stopImmediatePropagation()
      sendAction(url, kind, button)
    },
    { capture: true },
  )
  return button
}

function overlayFor(url) {
  const overlay = document.createElement('div')
  overlay.className = 'pi-streamer-overlay'
  overlay.appendChild(makeButton('play', 'Play on Pi Streamer', '▶', url))
  overlay.appendChild(makeButton('queue', 'Queue on Pi Streamer', '+', url))
  return overlay
}

function processAnchor(anchor) {
  if (anchor.hasAttribute(PROCESSED_ATTR)) return
  const url = videoUrlFromAnchor(anchor)
  if (!url) return
  anchor.setAttribute(PROCESSED_ATTR, '1')

  // ytd-thumbnail is the actual sized/positioned box in every layout this
  // targets (grid tiles, search results, sidebar recommendations,
  // playlist rows) — anchoring the overlay there, not the <a> itself,
  // keeps it aligned to the visible thumbnail image regardless of
  // whatever else the anchor wraps.
  const container = anchor.closest('ytd-thumbnail') || anchor
  container.classList.add('pi-streamer-container')
  container.appendChild(overlayFor(url))
}

function scan(root) {
  if (root.matches?.(THUMBNAIL_ANCHOR_SELECTOR)) {
    processAnchor(root)
    return
  }
  root.querySelectorAll?.(THUMBNAIL_ANCHOR_SELECTOR).forEach(processAnchor)
}

scan(document)

const observer = new MutationObserver((mutations) => {
  for (const mutation of mutations) {
    for (const node of mutation.addedNodes) {
      if (node.nodeType === Node.ELEMENT_NODE) scan(node)
    }
  }
})
observer.observe(document.documentElement, { childList: true, subtree: true })
