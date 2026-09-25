import { useEffect, useRef, useState } from 'react'
import { ActionIcon, Group, Slider, Stack, Text, ThemeIcon } from '@mantine/core'
import {
  IconMusic,
  IconPlayerPause,
  IconPlayerPlay,
  IconPlayerTrackNext,
  IconPlayerTrackPrev,
  IconRewindBackward10,
  IconRewindForward10,
  IconVolume2,
  IconVolumeOff,
} from '@tabler/icons-react'
import { formatTime } from '../format.js'
import {
  albumArtUrl,
  next,
  pause,
  previous,
  resume,
  seek,
  seekRelative,
  setVolume,
} from '../api.js'

const ART_SIZE = 48

function PlayerBar({ status }) {
  const [artFailed, setArtFailed] = useState(false)
  const [dragValue, setDragValue] = useState(null)
  const [dragVolume, setDragVolume] = useState(null)
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
    try {
      if (isPlaying) {
        await pause()
      } else {
        await resume()
      }
    } catch (err) {
      console.error(err)
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

  return (
    <Group justify="space-between" wrap="nowrap" h="100%" px="md">
      <Group wrap="nowrap" gap="sm" style={{ minWidth: 0, flex: '0 1 240px' }}>
        {showArt ? (
          <img
            src={albumArtUrl(status.song)}
            onError={() => setArtFailed(true)}
            alt="Album art"
            style={{
              width: ART_SIZE,
              height: ART_SIZE,
              objectFit: 'cover',
              borderRadius: 6,
              flexShrink: 0,
            }}
          />
        ) : (
          <ThemeIcon
            variant="light"
            color="gray"
            radius="md"
            style={{ width: ART_SIZE, height: ART_SIZE, flexShrink: 0 }}
          >
            <IconMusic size={22} />
          </ThemeIcon>
        )}

        <Stack gap={0} style={{ minWidth: 0, flex: 1 }}>
          <Text size="sm" fw={600} truncate="end">
            {primaryLine}
          </Text>
          {status?.artist && (
            <Text size="xs" c="dimmed" truncate="end">
              {status.artist}
            </Text>
          )}
        </Stack>
      </Group>

      <Stack gap={4} align="center" style={{ flex: '1 1 auto', maxWidth: 480 }}>
        <Group gap="xs" justify="center" wrap="nowrap">
          <ActionIcon variant="subtle" onClick={handlePrevious} aria-label="Previous track">
            <IconPlayerTrackPrev size={18} />
          </ActionIcon>
          <ActionIcon
            variant="subtle"
            onClick={() => handleSeekRelative(-10)}
            aria-label="Back 10 seconds"
          >
            <IconRewindBackward10 size={18} />
          </ActionIcon>
          <ActionIcon
            variant="filled"
            radius="xl"
            size="lg"
            onClick={handlePlayPause}
            aria-label={isPlaying ? 'Pause' : 'Play'}
          >
            {isPlaying ? <IconPlayerPause size={18} /> : <IconPlayerPlay size={18} />}
          </ActionIcon>
          <ActionIcon
            variant="subtle"
            onClick={() => handleSeekRelative(10)}
            aria-label="Forward 10 seconds"
          >
            <IconRewindForward10 size={18} />
          </ActionIcon>
          <ActionIcon variant="subtle" onClick={handleNext} aria-label="Next track">
            <IconPlayerTrackNext size={18} />
          </ActionIcon>
        </Group>

        <Group gap="xs" wrap="nowrap" w="100%">
          <Text size="xs" c="dimmed" style={{ width: 36, textAlign: 'right' }}>
            {formatTime(elapsed)}
          </Text>
          <Slider
            style={{ flex: 1 }}
            size="sm"
            value={canSeek ? elapsed : 0}
            max={canSeek ? duration : 1}
            min={0}
            disabled={!canSeek}
            label={canSeek ? (value) => formatTime(value) : null}
            onChange={setDragValue}
            onChangeEnd={handleSeekEnd}
          />
          <Text size="xs" c="dimmed" style={{ width: 36 }}>
            {formatTime(duration)}
          </Text>
        </Group>
      </Stack>

      <Group wrap="nowrap" gap="xs" style={{ flex: '0 0 auto' }}>
        <ActionIcon
          variant="subtle"
          onClick={handleMuteToggle}
          aria-label={volume > 0 ? 'Mute' : 'Unmute'}
        >
          {volume > 0 ? <IconVolume2 size={18} /> : <IconVolumeOff size={18} />}
        </ActionIcon>
        <Slider
          w={100}
          size="sm"
          value={dragVolume ?? volume}
          min={0}
          max={100}
          onChange={setDragVolume}
          onChangeEnd={handleVolumeChangeEnd}
          label={(value) => `${value}%`}
        />
      </Group>
    </Group>
  )
}

export default PlayerBar
