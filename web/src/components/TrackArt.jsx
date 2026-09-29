import { useState } from 'react'
import { albumArtUrl } from '../api.js'
import { isYouTubeUrl } from '../isYouTubeUrl.js'
import { placeholderStyle } from '../placeholderArt.js'

// Album art for a track — a real image when one's confirmed to exist,
// otherwise a deterministic client-generated placeholder (see
// placeholderArt.js) instead of a generic gray icon.
//
// knownHasArt/resolvedPath (from useAlbumArtStatus, a batched
// POST .../albumart/query lookup) short-circuit the two already-known
// cases: `false` skips straight to the placeholder (no <img> request, no
// daemon round trip through mpd at all — the common case for internet
// radio and many plain audio files); `true` points the <img> straight at
// the static path the query already returned, skipping /api/albumart's
// resolve-then-redirect hop entirely, so *this* image request goes
// directly to the static file server and never touches the Go application
// at all. `undefined` (not yet known — a newly-added track the daemon
// hasn't resolved yet) falls back to the original try-then-placeholder
// path: an <img> pointed at /api/albumart (which resolves-and-redirects on
// first request), only falling back to the placeholder if that fails.
//
// label is the seed for the placeholder's color+monogram (album name is
// the best seed — same album always looks the same — falling back to
// title/url). Callers should key their row/card by `url` so a *different*
// track gets a fresh instance (and thus a fresh internal artFailed state)
// rather than reusing state across tracks.
//
// size is either a fixed pixel number (list rows, the track detail page —
// contexts with a definite, unchanging size) or the string '100%', which
// fills whatever width the parent gives it at a square aspect ratio via
// CSS aspect-ratio rather than a fixed height — used by the grid view's
// cards so the art actually scales with the grid cell's real width
// (responsive column counts, a wider Container on large screens) instead
// of sitting at a fixed size with empty space around it regardless of how
// much room the tile actually has.
function TrackArt({ url, label, artist, album, knownHasArt, resolvedPath, size, radius = 'md' }) {
  const [failed, setFailed] = useState(false)

  const showPlaceholder = !url || knownHasArt === false || failed
  const dimensions =
    size === '100%' ? { width: '100%', aspectRatio: '1 / 1', height: 'auto' } : { width: size, height: size }
  // radius=0 (the grid card's poster tile, which wants the *card's* own
  // rounding to be the only visible corner treatment) needs a real "0px",
  // not var(--mantine-radius-0) — that custom property doesn't exist, and
  // an undefined var() with no fallback makes the whole declaration
  // invalid rather than reliably resolving to zero.
  const radiusValue = radius === 0 || radius === '0' ? '0px' : `var(--mantine-radius-${radius})`

  if (showPlaceholder) {
    const { color, letters } = placeholderStyle(label || url || '')
    return (
      <svg
        viewBox="0 0 100 100"
        role="img"
        aria-label={label || 'No album art'}
        style={{ ...dimensions, display: 'block', flexShrink: 0, borderRadius: radiusValue }}
      >
        <rect width="100" height="100" fill={color} />
        <text
          x="50"
          y="52"
          textAnchor="middle"
          dominantBaseline="central"
          fontSize="38"
          fontFamily="system-ui, sans-serif"
          fontWeight="600"
          fill="white"
        >
          {letters}
        </text>
      </svg>
    )
  }

  // YouTube's hqdefault.jpg/sddefault.jpg (never mqdefault.jpg or
  // maxresdefault.jpg) are always a 4:3 *canvas* with a 16:9 video frame
  // centered inside it — a fixed, structural quirk of those two specific
  // thumbnail sizes, not per-video letterboxing, and it's exactly the
  // same 12.5%-top/12.5%-bottom black bar every time for a normal
  // widescreen video. object-fit: cover inside a *square* box never
  // touches that: scaling a 4:3 source to cover a square only ever needs
  // to crop the sides (the height already lands exactly on the box's
  // height with no scaling headroom left over), so the bars show straight
  // through untouched — confirmed, not just theoretical, since this was
  // still visible after a first attempt at a generic fixed zoom that
  // wasn't the exact right factor.
  //
  // The fix is an exact scale, not a guess: `resolvedPath` (from the
  // batched art-status query) is only ever mqdefault.jpg for a YouTube
  // URL — no bars, no extra zoom needed. Anything reaching this <img> any
  // other way (the not-yet-known fallback below, and every direct
  // GET /api/albumart caller, including PlayerBar/NowPlayingScreen's own
  // <img> tags) is always resolve()'s hqdefault.jpg, which does have the
  // bars. Scaling that by exactly 4/3 makes the true 16:9 content fill the
  // square vertically with no crop math left over — not an approximation.
  const usingKnownResolvedPath = Boolean(knownHasArt && resolvedPath)
  const debarYouTube = isYouTubeUrl(url) && !usingKnownResolvedPath

  return (
    <div style={{ ...dimensions, overflow: 'hidden', borderRadius: radiusValue, flexShrink: 0 }}>
      <img
        src={usingKnownResolvedPath ? resolvedPath : albumArtUrl(url, artist, album)}
        onError={() => setFailed(true)}
        alt="Album art"
        style={{
          width: '100%',
          height: '100%',
          display: 'block',
          objectFit: 'cover',
          transform: debarYouTube ? 'scale(1.3334)' : undefined,
        }}
      />
    </div>
  )
}

export default TrackArt
