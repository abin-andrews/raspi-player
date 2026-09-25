import { useEffect, useState } from 'react'
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Group,
  Stack,
  Text,
  TextInput,
} from '@mantine/core'
import {
  IconChevronDown,
  IconChevronUp,
  IconPlayerPlay,
  IconPlus,
  IconTrash,
  IconTrashX,
} from '@tabler/icons-react'
import {
  addToQueue,
  clearQueue,
  getQueue,
  moveInQueue,
  playQueueItem,
  removeFromQueue,
} from '../api.js'
import { formatTime } from '../format.js'

function Queue({ status }) {
  const [queue, setQueue] = useState([])
  const [url, setUrl] = useState('')
  const [error, setError] = useState(null)

  async function refresh() {
    try {
      const q = await getQueue()
      setQueue(q ?? [])
    } catch (err) {
      setError(err.message)
    }
  }

  useEffect(() => {
    refresh()
  }, [])

  async function handleAdd() {
    if (!url.trim()) return
    setError(null)
    try {
      await addToQueue(url.trim())
      setUrl('')
      await refresh()
    } catch (err) {
      setError(err.message)
    }
  }

  async function handlePlay(id) {
    setError(null)
    try {
      await playQueueItem(id)
      await refresh()
    } catch (err) {
      setError(err.message)
    }
  }

  async function handleRemove(id) {
    setError(null)
    try {
      await removeFromQueue(id)
      await refresh()
    } catch (err) {
      setError(err.message)
    }
  }

  async function handleMove(id, position) {
    setError(null)
    try {
      await moveInQueue(id, position)
      await refresh()
    } catch (err) {
      setError(err.message)
    }
  }

  async function handleClear() {
    if (!window.confirm('Clear the entire queue?')) return
    setError(null)
    try {
      await clearQueue()
      await refresh()
    } catch (err) {
      setError(err.message)
    }
  }

  const sorted = [...queue].sort((a, b) => a.position - b.position)

  return (
    <Stack>
      {error && (
        <Alert color="red" title="Error" withCloseButton onClose={() => setError(null)}>
          {error}
        </Alert>
      )}

      <Group align="flex-end">
        <TextInput
          label="URL"
          placeholder="https://example.com/stream.mp3"
          value={url}
          onChange={(e) => setUrl(e.currentTarget.value)}
          style={{ flex: 1 }}
        />
        <Button onClick={handleAdd} disabled={!url.trim()} leftSection={<IconPlus size={16} />}>
          Add to Queue
        </Button>
        <Button
          onClick={handleClear}
          disabled={sorted.length === 0}
          color="red"
          variant="light"
          leftSection={<IconTrashX size={16} />}
        >
          Clear Queue
        </Button>
      </Group>

      <Stack gap="xs">
        {sorted.length === 0 && (
          <Text c="dimmed" size="sm">
            Queue is empty — add a URL above, or play something from Favorites/Search/History.
          </Text>
        )}
        {sorted.map((track, i) => {
          const isPlaying = track.id === status?.songId
          return (
            <Card
              key={track.id}
              withBorder
              padding="sm"
              style={
                isPlaying
                  ? { borderLeft: '3px solid var(--mantine-color-blue-6)' }
                  : undefined
              }
            >
              <Group justify="space-between" wrap="nowrap">
                <Stack gap={0} style={{ minWidth: 0 }}>
                  <Group gap="xs" wrap="nowrap">
                    <Text fw={500} truncate="end">
                      {track.title || track.url}
                    </Text>
                    {isPlaying && (
                      <Badge size="xs" color="blue" variant="light">
                        Now Playing
                      </Badge>
                    )}
                  </Group>
                  {track.artist && (
                    <Text size="xs" c="dimmed" truncate="end">
                      {track.artist}
                      {track.duration > 0 ? ` · ${formatTime(track.duration)}` : ''}
                    </Text>
                  )}
                  {!track.artist && track.duration > 0 && (
                    <Text size="xs" c="dimmed" truncate="end">
                      {formatTime(track.duration)}
                    </Text>
                  )}
                </Stack>
                <Group gap="xs" wrap="nowrap">
                  <ActionIcon
                    variant="light"
                    onClick={() => handlePlay(track.id)}
                    aria-label="Play"
                  >
                    <IconPlayerPlay size={16} />
                  </ActionIcon>
                  <ActionIcon
                    variant="subtle"
                    disabled={i === 0}
                    onClick={() => handleMove(track.id, track.position - 1)}
                    aria-label="Move up"
                  >
                    <IconChevronUp size={16} />
                  </ActionIcon>
                  <ActionIcon
                    variant="subtle"
                    disabled={i === sorted.length - 1}
                    onClick={() => handleMove(track.id, track.position + 1)}
                    aria-label="Move down"
                  >
                    <IconChevronDown size={16} />
                  </ActionIcon>
                  <ActionIcon
                    color="red"
                    variant="subtle"
                    onClick={() => handleRemove(track.id)}
                    aria-label="Remove"
                  >
                    <IconTrash size={16} />
                  </ActionIcon>
                </Group>
              </Group>
            </Card>
          )
        })}
      </Stack>
    </Stack>
  )
}

export default Queue
