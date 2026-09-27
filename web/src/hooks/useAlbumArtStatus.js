import { useEffect, useRef, useState } from 'react'
import { queryAlbumArt } from '../api.js'

// Batches a list of URLs into one POST /api/albumart/query call and
// returns a { [url]: { hasArt, path } } map (see internal/api.ArtStatus) —
// hasArt=true entries include the static path to fetch the art from
// directly (served by a plain http.FileServer, bypassing /api/albumart's
// resolve-then-redirect dance entirely since it's already known); a url
// simply absent from the map means "unknown, never checked" (the daemon
// hasn't resolved it yet). Same batching pattern as useCachedUrls.js.
// Callers should treat a missing entry as "fall back to the real <img>
// pointed at /api/albumart and reacting to onError" — the whole point of
// this hook is to avoid that trial-and-error/extra redirect hop for URLs
// already known one way or the other.
export function useAlbumArtStatus(urls) {
  const [status, setStatus] = useState({})
  const key = urls.join('\n')
  const lastKey = useRef(null)

  useEffect(() => {
    if (key === lastKey.current) return
    lastKey.current = key
    if (urls.length === 0) {
      setStatus({})
      return
    }
    let cancelled = false
    queryAlbumArt(urls)
      .then((result) => {
        if (!cancelled) setStatus(result ?? {})
      })
      .catch(() => {
        // Best-effort UI decoration — a failed lookup just means every
        // url falls back to the try-then-placeholder path this render.
      })
    return () => {
      cancelled = true
    }
  }, [key, urls])

  return status
}
