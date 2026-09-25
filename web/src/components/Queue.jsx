import { memo, useEffect, useRef, useState } from 'react'
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
import { notifications } from '@mantine/notifications'
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
import { useCachedUrls } from '../hooks/useCachedUrls.js'

// How long a freshly-added row stays visually highlighted after Add to
// Queue succeeds — long enough to catch the eye, short enough not to
// linger and look like a stuck/error state.
const HIGHLIGHT_MS = 3000

function Queue({ status }) {
  const [queue, setQueue] = useState([])
  const [url, setUrl] = useState('')
  const [error, setError] = useState(null)
  const [adding, setAdding] = useState(false)
  const [highlightId, setHighlightId] = useState(null)
  const cached = useCachedUrls(queue.map((t) => t.url))
  const rowRefs = useRef({})
  // Stable per-track-id ref callbacks (created once, reused across
  // renders) — an inline `ref={(el) => ...}` arrow function is a *new*
  // function every render, and React treats a changed ref-callback
  // identity as "detach the old one, attach the new one," so every row's
  // DOM ref was being torn down and rebuilt on every single render. That
  // was invisible functionally (rowRefs.current ended up correct either
  // way) but was pure churn — and this component used to re-render every
  // second purely from the status prop ticking (see the memo comparator
  // on this component's export), so it added up to a lot of pointless
  // work over a long playback session.
  const rowRefCallbacks = useRef({})
  function rowRef(id) {
    if (!rowRefCallbacks.current[id]) {
      rowRefCallbacks.current[id] = (el) => {
        if (el) rowRefs.current[id] = el
        else delete rowRefs.current[id]
      }
    }
    return rowRefCallbacks.current[id]
  }

  // Prune stale entries once the queue itself changes, so a long session
  // with many tracks added/removed over time doesn't leave an ever-growing
  // set of unused per-id callback closures sitting in rowRefCallbacks.
  useEffect(() => {
    const liveIds = new Set(queue.map((t) => t.id))
    for (const id of Object.keys(rowRefCallbacks.current)) {
      if (!liveIds.has(Number(id))) delete rowRefCallbacks.current[id]
    }
  }, [queue])

  async function refresh() {
    try {
      const q = await getQueue()
      setQueue(q ?? [])
      return q ?? []
    } catch (err) {
      setError(err.message)
      return []
    }
  }

  useEffect(() => {
    refresh()
  }, [])

  // Scroll the just-added row into view as soon as it's rendered, so a
  // track appended to the end of a long queue doesn't silently land
  // off-screen with no visible confirmation it was added.
  useEffect(() => {
    if (highlightId == null) return
    rowRefs.current[highlightId]?.scrollIntoView({ behavior: 'smooth', block: 'center' })
    const timer = setTimeout(() => setHighlightId(null), HIGHLIGHT_MS)
    return () => clearTimeout(timer)
  }, [highlightId])

  async function handleAdd() {
    const submitted = url.trim()
    if (!submitted || adding) return
    setError(null)
    setAdding(true)
    try {
      await addToQueue(submitted)
      setUrl('')
      const q = await refresh()
      const sortedNow = [...q].sort((a, b) => a.position - b.position)
      // mpd appends new adds to the end of the queue, so the last entry
      // once sorted by position is the one that was just added.
      const added = sortedNow[sortedNow.length - 1]
      notifications.show({
        color: 'green',
        title: 'Added to queue',
        message: added?.title || added?.url || submitted,
      })
      if (added) {
        setHighlightId(added.id)
      }
    } catch (err) {
      setError(err.message)
      notifications.show({ color: 'red', title: 'Could not add to queue', message: err.message })
    } finally {
      setAdding(false)
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
          onKeyDown={(e) => e.key === 'Enter' && handleAdd()}
          disabled={adding}
          style={{ flex: 1 }}
        />
        <Button
          onClick={handleAdd}
          loading={adding}
          disabled={!url.trim()}
          leftSection={<IconPlus size={16} />}
        >
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
          const isHighlighted = track.id === highlightId
          return (
            <Card
              key={track.id}
              ref={rowRef(track.id)}
              withBorder
              padding="sm"
              style={{
                transition: 'background-color 0.6s ease, border-left-color 0.6s ease',
                backgroundColor: isHighlighted
                  ? 'var(--mantine-color-green-light)'
                  : undefined,
                ...(isPlaying
                  ? { borderLeft: '3px solid var(--mantine-color-blue-6)' }
                  : undefined),
              }}
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
                    {cached[track.url] && (
                      <Badge size="xs" color="teal" variant="light">
                        Cached
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

// Memoized with a custom comparator: this component only ever reads
// status.songId (to highlight the currently-playing row) — not
// elapsed/duration — so without this it was re-rendering, re-sorting the
// queue, and rebuilding every row's DOM every single second while
// something played, for no visible benefit at all. Now it only re-renders
// on an actual song change (plus, as with any component, its own internal
// state changes — memo only gates re-renders triggered by the parent).
export default memo(Queue, (prev, next) => prev.status?.songId === next.status?.songId)
