import { ActionIcon, Group, Slider, Stack, Text, ThemeIcon } from '@mantine/core'
import {
  IconBrandYoutube,
  IconChevronDown,
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
import { isYouTubeUrl } from '../isYouTubeUrl.js'

const ART_SIZE = 280

// The full-screen "now playing" view — everything PlayerBar's mini bar
// shows, at a size actually meant to be looked at rather than glanced at:
// large album art, title/artist/album, a bigger transport row, and a
// close button to drop back to whatever was on screen before. Shares all
// of its actual playback state/handlers with PlayerBar via
// usePlayerControls — this is a different layout over the same status
// prop and API calls, not a separate playback implementation.
function NowPlayingScreen({ status, onClose, hideVolumeControl }) {
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
    <Stack h="100%" justify="space-between" py="md">
      <Group justify="flex-start">
        <ActionIcon variant="subtle" size="lg" onClick={onClose} aria-label="Back to player">
          <IconChevronDown size={24} />
        </ActionIcon>
      </Group>

      <Stack align="center" gap="md" style={{ flex: 1 }} justify="center">
        {showArt ? (
          <img
            src={albumArtUrl(status.song)}
            onError={() => setArtFailed(true)}
            alt="Album art"
            style={{
              width: ART_SIZE,
              height: ART_SIZE,
              maxWidth: '80vw',
              maxHeight: '40vh',
              objectFit: 'cover',
              borderRadius: 12,
              boxShadow: 'var(--mantine-shadow-lg)',
            }}
          />
        ) : (
          <ThemeIcon
            variant="light"
            color="gray"
            radius="md"
            style={{ width: ART_SIZE, height: ART_SIZE, maxWidth: '80vw', maxHeight: '40vh' }}
          >
            <IconMusic size={96} />
          </ThemeIcon>
        )}

        <Stack gap={4} align="center" style={{ maxWidth: '90vw' }}>
          <Group gap={6} wrap="nowrap" justify="center" style={{ maxWidth: '100%' }}>
            <Text size="xl" fw={700} truncate="end" ta="center">
              {primaryLine}
            </Text>
            {isYouTubeUrl(status?.song) && (
              <span title="From YouTube" style={{ display: 'inline-flex', flexShrink: 0 }}>
                <IconBrandYoutube size={20} color="var(--mantine-color-red-6)" />
              </span>
            )}
          </Group>
          {status?.artist && (
            <Text size="md" c="dimmed" truncate="end" ta="center" style={{ maxWidth: '100%' }}>
              {status.artist}
            </Text>
          )}
          {status?.album && (
            <Text size="sm" c="dimmed" truncate="end" ta="center" style={{ maxWidth: '100%' }}>
              {status.album}
            </Text>
          )}
        </Stack>
      </Stack>

      <Stack gap="lg" style={{ width: '100%', maxWidth: 480, alignSelf: 'center' }}>
        <Group gap="xs" wrap="nowrap" w="100%">
          <Text size="xs" c="dimmed" style={{ width: 40, textAlign: 'right' }}>
            {formatTime(elapsed)}
          </Text>
          <Slider
            style={{ flex: 1 }}
            value={canSeek ? elapsed : 0}
            max={canSeek ? duration : 1}
            min={0}
            disabled={!canSeek}
            label={canSeek ? (value) => formatTime(value) : null}
            onChange={setDragValue}
            onChangeEnd={handleSeekEnd}
          />
          <Text size="xs" c="dimmed" style={{ width: 40 }}>
            {formatTime(duration)}
          </Text>
        </Group>

        <Group gap="md" justify="center" wrap="nowrap">
          <ActionIcon variant="subtle" size="xl" onClick={handlePrevious} aria-label="Previous track">
            <IconPlayerTrackPrev size={26} />
          </ActionIcon>
          <ActionIcon
            variant="subtle"
            size="xl"
            onClick={() => handleSeekRelative(-10)}
            aria-label="Back 10 seconds"
          >
            <IconRewindBackward10 size={24} />
          </ActionIcon>
          <ActionIcon
            variant="filled"
            radius="xl"
            size={64}
            onClick={handlePlayPause}
            aria-label={isPlaying ? 'Pause' : 'Play'}
          >
            {isPlaying ? <IconPlayerPause size={28} /> : <IconPlayerPlay size={28} />}
          </ActionIcon>
          <ActionIcon
            variant="subtle"
            size="xl"
            onClick={() => handleSeekRelative(10)}
            aria-label="Forward 10 seconds"
          >
            <IconRewindForward10 size={24} />
          </ActionIcon>
          <ActionIcon variant="subtle" size="xl" onClick={handleNext} aria-label="Next track">
            <IconPlayerTrackNext size={26} />
          </ActionIcon>
        </Group>

        {!hideVolumeControl && (
          <Group gap="xs" wrap="nowrap" w="100%">
            <ActionIcon
              variant="subtle"
              onClick={handleMuteToggle}
              aria-label={volume > 0 ? 'Mute' : 'Unmute'}
            >
              {volume > 0 ? <IconVolume2 size={18} /> : <IconVolumeOff size={18} />}
            </ActionIcon>
            <Slider
              style={{ flex: 1 }}
              size="sm"
              value={dragVolume ?? volume}
              min={0}
              max={100}
              onChange={setDragVolume}
              onChangeEnd={handleVolumeChangeEnd}
              label={(value) => `${value}%`}
            />
          </Group>
        )}
      </Stack>
    </Stack>
  )
}

export default NowPlayingScreen
