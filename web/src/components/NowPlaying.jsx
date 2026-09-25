import { useEffect, useState } from 'react'
import {
  Alert,
  Badge,
  Box,
  Button,
  Group,
  Slider,
  Stack,
  Text,
  TextInput,
  ThemeIcon,
} from '@mantine/core'
import { IconMusic, IconPlayerPause, IconPlayerPlay } from '@tabler/icons-react'
import { albumArtUrl, pause, playURL, resume, seek } from '../api.js'
import { formatTime } from '../format.js'

const ART_SIZE = 72

function stateColor(state) {
  if (state === 'play') return 'green'
  if (state === 'pause') return 'yellow'
  return 'gray'
}

function NowPlaying({ status }) {
  const [url, setUrl] = useState('')
  const [error, setError] = useState(null)
  const [busy, setBusy] = useState(false)
  const [artFailed, setArtFailed] = useState(false)
  const [dragValue, setDragValue] = useState(null)

  useEffect(() => {
    setArtFailed(false)
  }, [status?.song])

  async function handlePlay() {
    if (!url.trim()) return
    setBusy(true)
    setError(null)
    try {
      await playURL(url.trim())
      setUrl('')
    } catch (err) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }

  async function handlePause() {
    setError(null)
    try {
      await pause()
    } catch (err) {
      setError(err.message)
    }
  }

  async function handleResume() {
    setError(null)
    try {
      await resume()
    } catch (err) {
      setError(err.message)
    }
  }

  async function handleSeekEnd(value) {
    setError(null)
    try {
      await seek(value)
    } catch (err) {
      setError(err.message)
    } finally {
      setDragValue(null)
    }
  }

  const hasSong = Boolean(status?.song)
  const showArt = hasSong && !artFailed
  const duration = status?.duration ?? 0
  const canSeek = duration > 0
  const elapsed = dragValue !== null ? dragValue : (status?.elapsed ?? 0)

  const primaryLine = status?.title || status?.song || 'Nothing playing'

  return (
    <Stack>
      {error && (
        <Alert color="red" title="Error" withCloseButton onClose={() => setError(null)}>
          {error}
        </Alert>
      )}

      <Group align="flex-end">
        <TextInput
          label="Stream URL"
          placeholder="https://example.com/stream.mp3"
          value={url}
          onChange={(e) => setUrl(e.currentTarget.value)}
          onKeyDown={(e) => e.key === 'Enter' && handlePlay()}
          style={{ flex: 1 }}
        />
        <Button
          onClick={handlePlay}
          loading={busy}
          disabled={!url.trim()}
          leftSection={<IconPlayerPlay size={18} />}
        >
          Play
        </Button>
      </Group>

      <Stack gap="xs">
        <Group>
          <Badge color={stateColor(status?.state)}>{status?.state ?? 'unknown'}</Badge>
        </Group>

        <Group align="flex-start" wrap="nowrap">
          {showArt ? (
            <img
              src={albumArtUrl(status.song)}
              onError={() => setArtFailed(true)}
              alt="Album art"
              style={{
                width: ART_SIZE,
                height: ART_SIZE,
                objectFit: 'cover',
                borderRadius: 8,
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
              <IconMusic size={28} />
            </ThemeIcon>
          )}

          <Stack gap={2} style={{ flex: 1, minWidth: 0 }}>
            <Text fw={600} truncate="end">
              {primaryLine}
            </Text>
            {status?.artist && (
              <Text c="dimmed" size="sm" truncate="end">
                {status.artist}
              </Text>
            )}
            {status?.album && (
              <Text c="dimmed" size="xs" truncate="end">
                {status.album}
              </Text>
            )}

            {canSeek && (
              <Box mt="xs">
                <Slider
                  value={elapsed}
                  max={duration}
                  min={0}
                  label={(value) => formatTime(value)}
                  onChange={setDragValue}
                  onChangeEnd={handleSeekEnd}
                />
                <Text c="dimmed" size="xs" mt={4}>
                  {formatTime(elapsed)} / {formatTime(duration)}
                </Text>
              </Box>
            )}
          </Stack>
        </Group>

        <Group>
          <Button
            variant="light"
            onClick={handlePause}
            disabled={status?.state !== 'play'}
            leftSection={<IconPlayerPause size={18} />}
          >
            Pause
          </Button>
          <Button
            variant="light"
            onClick={handleResume}
            disabled={status?.state === 'play'}
            leftSection={<IconPlayerPlay size={18} />}
          >
            Resume
          </Button>
        </Group>
      </Stack>
    </Stack>
  )
}

export default NowPlaying
