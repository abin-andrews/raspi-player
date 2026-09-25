import { useEffect, useRef, useState } from 'react'
import { queryBucketCached } from '../api.js'

// Batches a list of URLs into one POST /api/bucket/query call and returns
// a { [url]: boolean } map of which are already cached locally — used to
// render a "cached" badge per track row (Queue/Search/Favorites/History)
// without one request per row. Re-queries whenever the URL list's content
// changes (compared by joined value, not reference, since callers usually
// derive a fresh array on every render from the same underlying data).
export function useCachedUrls(urls) {
  const [cached, setCached] = useState({})
  const key = urls.join('\n')
  const lastKey = useRef(null)

  useEffect(() => {
    if (key === lastKey.current) return
    lastKey.current = key
    if (urls.length === 0) {
      setCached({})
      return
    }
    let cancelled = false
    queryBucketCached(urls)
      .then((result) => {
        if (!cancelled) setCached(result ?? {})
      })
      .catch(() => {
        // Best-effort UI decoration — a failed lookup just means no
        // badges show up this render, not an error worth surfacing.
      })
    return () => {
      cancelled = true
    }
  }, [key, urls])

  return cached
}
