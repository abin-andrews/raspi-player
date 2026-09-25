import { useEffect, useState } from 'react'
import { connectStatusSocket } from '../api.js'

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

    const ws = connectStatusSocket((s) => {
      if (cancelled) return
      gotMessage = true
      setStatus(s)
      setReady(true)
      setError(null)
    })

    ws.onerror = () => {
      if (!cancelled) setError('Unable to connect to the daemon')
    }
    ws.onclose = () => {
      // Only surface an error if we closed before ever getting a message —
      // once ready, a later disconnect shouldn't yank the UI back to a
      // loading screen (the browser's WebSocket auto-reconnect story is a
      // separate concern from this initial-sync fix).
      if (!cancelled && !gotMessage) {
        setError('Unable to connect to the daemon')
      }
    }

    return () => {
      cancelled = true
      ws.close()
    }
  }, [])

  return { status, ready, error }
}
