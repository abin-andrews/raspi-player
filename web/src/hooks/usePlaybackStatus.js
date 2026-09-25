import { useEffect, useState } from 'react'
import { connectStatusSocket } from '../api.js'

// A dev-server restart (or a Pi reboot) closes the socket for a moment;
// retrying on a short fixed delay recovers automatically once it's back,
// without hammering the daemon while it's down.
const RECONNECT_DELAY_MS = 2000

// Single source of truth for live playback status. Waits for the
// WebSocket's first message before reporting ready=true — the daemon
// sends the current status immediately on connect (see internal/ws.Hub's
// `initial` parameter), so a reloading UI never has to show stale/default
// state while catching up: it simply doesn't render the real UI until it
// already has correct data.
//
// Call this once at the top of the tree (App.jsx) and pass status/ready
// down, so only one WebSocket connection exists per tab regardless of how
// many components need the status.
export function usePlaybackStatus() {
  const [status, setStatus] = useState(null)
  const [ready, setReady] = useState(false)
  const [error, setError] = useState(null)

  useEffect(() => {
    let cancelled = false
    let gotMessage = false
    let reconnectTimer = null
    let ws = null

    const connect = () => {
      ws = connectStatusSocket((s) => {
        if (cancelled) return
        gotMessage = true
        setStatus(s)
        setReady(true)
        setError(null)
      })

      ws.onerror = () => {
        if (!cancelled && !gotMessage) setError('Unable to connect to the daemon')
      }
      ws.onclose = () => {
        if (cancelled) return
        // Only surface an error if we closed before ever getting a message —
        // once ready, a later disconnect (e.g. the daemon restarting during
        // development) shouldn't yank the UI back to a loading screen; it
        // keeps showing the last known status and reconnects quietly.
        if (!gotMessage) setError('Unable to connect to the daemon')
        reconnectTimer = setTimeout(connect, RECONNECT_DELAY_MS)
      }
    }

    connect()

    return () => {
      cancelled = true
      if (reconnectTimer) clearTimeout(reconnectTimer)
      ws.close()
    }
  }, [])

  return { status, ready, error }
}
