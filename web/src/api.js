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
export const albumArtUrl = (url) => `/api/albumart?url=${encodeURIComponent(url)}`

// Opens a WebSocket to the daemon's live playback-status feed. Calls
// onMessage with each parsed Status object as it arrives. Returns the raw
// WebSocket so the caller can close() it (e.g. in a useEffect cleanup).
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

// mpd's actual live playback queue — distinct from the app's saved named
// Playlists above.
export const getQueue = () => request('GET', '/api/queue')
export const addToQueue = (url) => request('POST', '/api/queue', { url })
export const removeFromQueue = (id) => request('DELETE', `/api/queue/${id}`)
export const moveInQueue = (id, position) => request('POST', `/api/queue/${id}/move`, { position })
export const playQueueItem = (id) => request('POST', `/api/queue/${id}/play`)
export const clearQueue = () => request('DELETE', '/api/queue')

// Daemon settings (internal/config), currently just the OLED display's
// serial connection. setConfig replaces the whole object — always send back
// getConfig()'s result with your edits applied, not a partial patch.
export const getConfig = () => request('GET', '/api/config')
export const setConfig = (cfg) => request('PUT', '/api/config', cfg)
export const reloadConfig = () => request('POST', '/api/config/reload')

export const getOledStatus = () => request('GET', '/api/oled/status')
export const getOledPorts = () => request('GET', '/api/oled/ports')
// The allowed baud rates, straight from the backend (internal/config.AllowedBauds)
// so this dropdown can never drift out of sync with what setConfig() will accept.
export const getOledBauds = () => request('GET', '/api/oled/bauds')
