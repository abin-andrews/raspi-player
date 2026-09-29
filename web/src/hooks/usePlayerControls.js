import { useEffect, useRef, useState } from 'react'
import { next, pause, previous, resume, seek, seekRelative, setVolume } from '../api.js'

// Shared playback-control state/handlers behind both PlayerBar (the
// persistent mini bar) and NowPlayingScreen (the full-screen expanded
// view) — they're just two different layouts over the same status prop
// and the same handful of API calls, so the state that has to live
// somewhere (a dragged-but-not-yet-committed slider value, whether art
// failed to load, the last non-zero volume for mute/unmute) and the
// handlers that touch it live here once instead of being duplicated
// between the two components.
export function usePlayerControls(status) {
  const [artFailed, setArtFailed] = useState(false)
  const [dragValue, setDragValue] = useState(null)
  const [dragVolume, setDragVolume] = useState(null)
  const [playPausePending, setPlayPausePending] = useState(false)
  const lastNonZeroVolumeRef = useRef(100)

  useEffect(() => {
    setArtFailed(false)
  }, [status?.song])

  useEffect(() => {
    if (status?.volume > 0) {
      lastNonZeroVolumeRef.current = status.volume
    }
  }, [status?.volume])

  const hasSong = Boolean(status?.song)
  const showArt = hasSong && !artFailed
  const duration = status?.duration ?? 0
  const canSeek = duration > 0
  const elapsed = dragValue !== null ? dragValue : (status?.elapsed ?? 0)
  const isPlaying = status?.state === 'play'
  const volume = status?.volume ?? 0
  const primaryLine = status?.title || status?.song || 'Nothing playing'

  async function handlePlayPause() {
    setPlayPausePending(true)
    try {
      if (isPlaying) {
        await pause()
      } else {
        await resume()
      }
    } catch (err) {
      console.error(err)
    } finally {
      setPlayPausePending(false)
    }
  }

  async function handlePrevious() {
    try {
      await previous()
    } catch (err) {
      console.error(err)
    }
  }

  async function handleNext() {
    try {
      await next()
    } catch (err) {
      console.error(err)
    }
  }

  async function handleSeekRelative(delta) {
    try {
      await seekRelative(delta)
    } catch (err) {
      console.error(err)
    }
  }

  async function handleSeekEnd(value) {
    try {
      await seek(value)
    } catch (err) {
      console.error(err)
    } finally {
      setDragValue(null)
    }
  }

  async function handleVolumeChangeEnd(value) {
    try {
      await setVolume(value)
    } catch (err) {
      console.error(err)
    } finally {
      setDragVolume(null)
    }
  }

  async function handleMuteToggle() {
    try {
      if (volume > 0) {
        await setVolume(0)
      } else {
        await setVolume(lastNonZeroVolumeRef.current || 100)
      }
    } catch (err) {
      console.error(err)
    }
  }

  return {
    hasSong,
    showArt,
    setArtFailed,
    duration,
    canSeek,
    elapsed,
    isPlaying,
    playPausePending,
    volume,
    primaryLine,
    dragValue,
    setDragValue,
    dragVolume,
    setDragVolume,
    handlePlayPause,
    handlePrevious,
    handleNext,
    handleSeekRelative,
    handleSeekEnd,
    handleVolumeChangeEnd,
    handleMuteToggle,
  }
}
