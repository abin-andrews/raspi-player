import { ActionIcon, Group, Slider, Stack, Text, ThemeIcon } from '@mantine/core'
import {
  IconChevronUp,
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
import { albumArtUrl } from '../api.js'
import { usePlayerControls } from '../hooks/usePlayerControls.js'

const ART_SIZE = 48

// The persistent mini player, shown on every screen. Clicking the
// art/title area (not the transport buttons themselves) opens the
// full-screen NowPlayingScreen for the bigger view — onExpand is optional
// so this component still renders standalone if a caller has no use for
// that (there's no other caller today, but nothing here should require
// it).
function PlayerBar({ status, onExpand, hideVolumeControl }) {
  const {
    showArt,
    setArtFailed,
    duration,
    canSeek,
    elapsed,
    isPlaying,
    volume,
    primaryLine,
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
  } = usePlayerControls(status)

  return (
    <Group justify="space-between" wrap="nowrap" h="100%" px="md">
      <Group
        wrap="nowrap"
        gap="sm"
        style={{ minWidth: 0, flex: '0 1 240px', cursor: onExpand ? 'pointer' : undefined }}
        onClick={onExpand}
        role={onExpand ? 'button' : undefined}
        aria-label={onExpand ? 'Open now playing' : undefined}
      >
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
        {onExpand && (
          <ActionIcon variant="subtle" color="gray" size="sm" style={{ flexShrink: 0 }} tabIndex={-1}>
            <IconChevronUp size={14} />
          </ActionIcon>
        )}
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

      {/* Fixed minWidth (not flex: 1) reserves the same footprint whether
          or not the volume control renders inside it, so hiding it never
          shifts the centered transport controls — matches the slot's width
          when the mute icon + 100px slider are actually shown. */}
      <Group wrap="nowrap" gap="xs" justify="flex-end" style={{ flex: '0 0 auto', minWidth: 140 }}>
        {!hideVolumeControl && (
          <>
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
          </>
        )}
      </Group>
    </Group>
  )
}

export default PlayerBar
