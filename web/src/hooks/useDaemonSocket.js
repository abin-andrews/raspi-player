import { useEffect, useState } from 'react'
import { connectStatusSocket } from '../api.js'

// Reconnect backoff: starts fast (a dev-server restart or a Pi reboot
// should recover within a couple seconds) but backs off exponentially up
// to a cap — a fixed 2s retry forever meant that a phone with a flaky/out-
// of-range WiFi connection to the Pi would hammer new-WebSocket-plus-
// immediate-failure in a tight loop indefinitely, in the background, even
// while the tab wasn't focused. That's real, sustained CPU/battery/memory
// pressure from something that looks idle, which is exactly the "just
// having it open" symptom this backoff (plus the visibility handling
// below) is aimed at.
const BASE_RECONNECT_DELAY_MS = 1000
const MAX_RECONNECT_DELAY_MS = 30000

// Single source of truth for everything the daemon pushes over /ws:
// live playback status AND bucket-download progress, multiplexed onto one
// connection as {type, data} envelopes (see cmd/pi-streamer/main.go's
// wsMessage) so a low-power/mobile browser only ever holds open one
// WebSocket and one 1s status-ticker's worth of traffic, not a socket plus
// a separate HTTP-polling loop for downloads. Waits for the first "status"
// message before reporting ready=true — the daemon sends current state
// immediately on connect (see internal/ws.Hub's `initials`), so a
// reloading UI never has to show stale/default state while catching up: it
// simply doesn't render the real UI until it already has correct data.
//
// Call this once at the top of the tree (App.jsx) and pass status/
// downloads/ready down, so only one WebSocket connection exists per tab
// regardless of how many components need this state.
export function useDaemonSocket() {
  const [status, setStatus] = useState(null)
  const [downloads, setDownloads] = useState([])
  const [ready, setReady] = useState(false)
  const [error, setError] = useState(null)

  useEffect(() => {
    let cancelled = false
    let gotMessage = false
    let reconnectTimer = null
    let reconnectAttempt = 0
    let ws = null
    // True while we closed the socket ourselves because the tab went into
    // the background — onclose must not schedule a reconnect in that case,
    // or a backgrounded tab would keep reconnecting (and getting silently
    // dropped by the OS/browser) in a loop nobody can see.
    let suppressReconnect = false

    const scheduleReconnect = () => {
      if (suppressReconnect || cancelled) return
      const delay = Math.min(MAX_RECONNECT_DELAY_MS, BASE_RECONNECT_DELAY_MS * 2 ** reconnectAttempt)
      reconnectAttempt += 1
      reconnectTimer = setTimeout(connect, delay)
    }

    const connect = () => {
      if (cancelled) return
      ws = connectStatusSocket((envelope) => {
        if (cancelled || !envelope) return
        if (envelope.type === 'status') {
          gotMessage = true
          reconnectAttempt = 0
          setStatus(envelope.data)
          setReady(true)
          setError(null)
        } else if (envelope.type === 'downloads') {
          setDownloads(envelope.data ?? [])
        }
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
        scheduleReconnect()
      }
    }

    // Backgrounded tab: close the live connection (rather than let the
    // browser/OS eventually kill it out from under us) and stop
    // reconnecting until the tab is visible again. Reconnects immediately
    // (bypassing backoff) on foreground, so switching back never leaves a
    // stale UI waiting out whatever delay a previous failure had reached.
    function handleVisibilityChange() {
      if (document.visibilityState === 'hidden') {
        suppressReconnect = true
        if (reconnectTimer) {
          clearTimeout(reconnectTimer)
          reconnectTimer = null
        }
        ws?.close()
      } else {
        suppressReconnect = false
        reconnectAttempt = 0
        if (!ws || ws.readyState === WebSocket.CLOSED || ws.readyState === WebSocket.CLOSING) {
          connect()
        }
      }
    }

    document.addEventListener('visibilitychange', handleVisibilityChange)
    connect()

    return () => {
      cancelled = true
      document.removeEventListener('visibilitychange', handleVisibilityChange)
      if (reconnectTimer) clearTimeout(reconnectTimer)
      ws?.close()
    }
  }, [])

  return { status, downloads, ready, error }
}
