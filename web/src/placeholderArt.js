// Deterministic "no album art" placeholder: a solid-color tile with a 1-2
// letter monogram, both derived from a hash of the seed string (album
// name, falling back to title/url) — same seed always produces the same
// color+letters, entirely client-side (no network round trip, no server
// involvement at all), which is the whole point: instant, and correct by
// construction for a track with no real art rather than a generic icon.

function hashString(str) {
  let hash = 0
  for (let i = 0; i < str.length; i++) {
    hash = (hash << 5) - hash + str.charCodeAt(i)
    hash |= 0 // force a 32-bit int
  }
  return Math.abs(hash)
}

// Fixed saturation/lightness tuned to stay readable (white text on top)
// across the whole hue wheel, in both light and dark mode.
const SATURATION = 45
const LIGHTNESS = 40

export function placeholderStyle(seed) {
  const s = seed && seed.trim() ? seed.trim() : '?'
  const hash = hashString(s)
  const hue = hash % 360
  const letters = (s.replace(/[^a-zA-Z0-9]/g, '').slice(0, 2) || '?').toUpperCase()
  return { color: `hsl(${hue}, ${SATURATION}%, ${LIGHTNESS}%)`, letters }
}
