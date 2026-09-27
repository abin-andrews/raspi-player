import { useState } from 'react'
import { albumArtUrl } from '../api.js'
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
function TrackArt({ url, label, artist, album, knownHasArt, resolvedPath, size, radius = 'md' }) {
  const [failed, setFailed] = useState(false)

  const showPlaceholder = !url || knownHasArt === false || failed

  if (showPlaceholder) {
    const { color, letters } = placeholderStyle(label || url || '')
    return (
      <svg
        width={size}
        height={size}
        viewBox="0 0 100 100"
        role="img"
        aria-label={label || 'No album art'}
        style={{ flexShrink: 0, borderRadius: 'var(--mantine-radius-' + radius + ')' }}
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

  return (
    <img
      src={knownHasArt && resolvedPath ? resolvedPath : albumArtUrl(url, artist, album)}
      onError={() => setFailed(true)}
      alt="Album art"
      style={{
        width: size,
        height: size,
        objectFit: 'cover',
        borderRadius: 'var(--mantine-radius-' + radius + ')',
        flexShrink: 0,
      }}
    />
  )
}

export default TrackArt
