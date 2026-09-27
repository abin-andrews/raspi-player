// Thin fetch wrappers over the daemon's HTTP command API (internal/api/router.go).
// All paths are relative so this works against both the Vite dev proxy and
// the daemon's own static-served production build.

async function request(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  if (!res.ok) {
    let message = `${method} ${path} failed: ${res.status}`
    try {
      const data = await res.json()
      if (data.error) message = data.error
    } catch {
      // response had no JSON body; keep the generic message
    }
    throw new Error(message)
  }
  if (res.status === 204) return undefined
  const text = await res.text()
  return text ? JSON.parse(text) : undefined
}

export const playURL = (url) => request('POST', '/api/play', { url })
export const pause = () => request('POST', '/api/pause')
export const resume = () => request('POST', '/api/resume')
export const getStatus = () => request('GET', '/api/status')
export const seek = (seconds) => request('POST', '/api/seek', { seconds })
export const seekRelative = (seconds) => request('POST', '/api/seek/relative', { seconds })
export const next = () => request('POST', '/api/next')
export const previous = () => request('POST', '/api/previous')
export const setVolume = (volume) => request('POST', '/api/volume', { volume })

// Not a JSON fetch — returns the URL directly for use as an <img src>.
// artist/album are optional hints used only for a MusicBrainz/Cover Art
// Archive fallback lookup server-side if mpd itself has nothing.
export const albumArtUrl = (url, artist, album) => {
  const params = new URLSearchParams({ url })
  if (artist) params.set('artist', artist)
  if (album) params.set('album', album)
  return `/api/albumart?${params.toString()}`
}

// Cheap cache lookup (never a fetch) reporting, for each of urls, whether
// art is already known to exist — a url this has no answer for yet
// (never fetched, or a past fetch errored) is simply omitted from the
// result, distinguishing "confirmed no art" (false) from "don't know yet".
export const queryAlbumArt = (urls) => request('POST', '/api/albumart/query', { urls })

// Bypasses whatever's already known and always re-resolves — e.g. a file
// was re-tagged with new art since it was last resolved, or you want to
// retry the MusicBrainz fallback now that it's configured.
export const refreshAlbumArt = (url, artist, album) =>
  request('POST', '/api/albumart/refresh', { url, artist, album })

// Kicks off a background scan of the whole library, pre-fetching and
// caching art (including confirmed-negative results) for every entry not
// already known — an explicit "warm the cache" action rather than only
// discovering art lazily, one track at a time, as each is first viewed.
// Returns immediately; the scan itself runs in the background (progress
// arrives over /ws as a "jobs" envelope — see useDaemonSocket.js).
export const warmAlbumArt = () => request('POST', '/api/albumart/warm')

// Every currently-tracked background job (running or recently finished) —
// normally received pushed over /ws instead (see useDaemonSocket.js), but
// available for a one-off check.
export const getJobs = () => request('GET', '/api/jobs')

// Candidate Title/Artist/Album matches for query, via the same
// MusicBrainz lookup the album art fallback already uses (see
// refreshAlbumArt) — surfaced in the Library edit form as suggestions to
// pick from, never applied automatically. Returns { suggestions: [...] }.
export const suggestMetadata = (query) => request('POST', '/api/albumart/suggest', { query })

// Sets/clears a custom album art fallback, fetched from any URL that
// serves an image (not necessarily one the daemon otherwise knows how to
// find art from) — scope is "track" (key: the track's URL), "album", or
// "artist" (key: the album/artist name). Once set, it's used ahead of
// (track scope) or as a fallback behind (album/artist scope)
// auto-resolution until cleared — see the album art architecture notes.
export const setCustomArt = (scope, key, imageUrl) =>
  request('POST', '/api/albumart/custom', { scope, key, imageUrl })
export const clearCustomArt = (scope, key) => request('DELETE', '/api/albumart/custom', { scope, key })

// Opens a WebSocket to the daemon's live push feed. Calls onMessage with
// each parsed {type, data} envelope as it arrives (type is "status" or
// "downloads" — see cmd/pi-streamer/main.go's wsMessage/useDaemonSocket.js,
// which does the actual dispatching). Returns the raw WebSocket so the
// caller can close() it (e.g. in a useEffect cleanup).
export function connectStatusSocket(onMessage) {
  const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const ws = new WebSocket(`${scheme}//${location.host}/ws`)
  ws.onmessage = (event) => {
    try {
      onMessage(JSON.parse(event.data))
    } catch {
      // ignore malformed/non-status messages
    }
  }
  return ws
}

export const listFavorites = () => request('GET', '/api/favorites')
export const addFavorite = (url, title) => request('POST', '/api/favorites', { url, title })
export const removeFavorite = (url) =>
  request('DELETE', `/api/favorites?url=${encodeURIComponent(url)}`)

export const listPlaylists = () => request('GET', '/api/playlists')
export const createPlaylist = (name) => request('POST', '/api/playlists', { name })
export const getPlaylist = (name) => request('GET', `/api/playlists/${encodeURIComponent(name)}`)
export const addToPlaylist = (name, url, title) =>
  request('POST', `/api/playlists/${encodeURIComponent(name)}`, { url, title })

export const getHistory = (limit = 50) => request('GET', `/api/history?limit=${limit}`)

export const search = (query, limit = 25) =>
  request('GET', `/api/search?q=${encodeURIComponent(query)}&limit=${limit}`)

// Every URL ever played/favorited/playlisted, most-recently-indexed first —
// no query needed, unlike search(). The "media library" browsing view.
export const getLibrary = (limit = 25, offset = 0) =>
  request('GET', `/api/library?limit=${limit}&offset=${offset}`)

// Upserts a library entry by URL — the same call for both adding a new
// entry and editing an existing one (resubmit with the same url, changed
// fields). Distinct from playURL/addFavorite/addToPlaylist/addToQueue,
// which index a URL only as a side effect of doing something else.
export const addToLibrary = (url, title, artist, album, tags) =>
  request('POST', '/api/library', { url, title, artist, album, tags })
export const removeFromLibrary = (url) =>
  request('DELETE', `/api/library?url=${encodeURIComponent(url)}`)

// mpd's actual live playback queue — distinct from the app's saved named
// Playlists above.
export const getQueue = () => request('GET', '/api/queue')
export const addToQueue = (url) => request('POST', '/api/queue', { url })
export const removeFromQueue = (id) => request('DELETE', `/api/queue/${id}`)
export const moveInQueue = (id, position) => request('POST', `/api/queue/${id}/move`, { position })
export const playQueueItem = (id) => request('POST', `/api/queue/${id}/play`)
export const clearQueue = () => request('DELETE', '/api/queue')

// Daemon settings (internal/config): the OLED display's serial connection
// and the local audio bucket's mode/size limits. setConfig replaces the
// whole object — always send back getConfig()'s result with your edits
// applied, not a partial patch.
export const getConfig = () => request('GET', '/api/config')
export const setConfig = (cfg) => request('PUT', '/api/config', cfg)
export const reloadConfig = () => request('POST', '/api/config/reload')

export const getOledStatus = () => request('GET', '/api/oled/status')
export const getOledPorts = () => request('GET', '/api/oled/ports')
// The allowed baud rates, straight from the backend (internal/config.AllowedBauds)
// so this dropdown can never drift out of sync with what setConfig() will accept.
export const getOledBauds = () => request('GET', '/api/oled/bauds')

// The local audio-file cache (internal/bucket, via cmd/pi-streamer's
// bucketAdapter): usage of both the evictable playback cache and the
// permanent favorites archive, and a batched "is this URL cached" query so
// a rendered list of tracks needs one request for all its badges.
export const getBucketStatus = () => request('GET', '/api/bucket/status')
export const queryBucketCached = (urls) => request('POST', '/api/bucket/query', { urls })
// Every file currently in the evictable playback cache (not the favorites
// archive — that's browsed via listFavorites instead).
export const getBucketList = () => request('GET', '/api/bucket/list')
// In-flight downloads (on-demand plays, background prefetch, and favorite
// archiving all show up here) — meant to be polled while any are active.
export const getBucketDownloads = () => request('GET', '/api/bucket/downloads')
