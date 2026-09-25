import { memo, useEffect, useRef, useState } from 'react'
import { ActionIcon, Alert, Badge, Card, Group, Progress, Stack, Text, Title } from '@mantine/core'
import { IconDownload, IconHeart, IconHeartFilled } from '@tabler/icons-react'
import { addFavorite, getBucketList, listFavorites, removeFavorite } from '../api.js'
import { formatBytes } from '../format.js'

// Browses the evictable playback cache's contents (see internal/bucket) —
// distinct from the Favorites tab, which lists the app's own saved
// favorite records (internal/store), not files on disk. A track can be
// favorited straight from here, which archives it permanently and
// separately (see CLAUDE.md's bucket cache notes) — this tab's own list
// doesn't change as a result, since that's a different store with its own
// eviction rules.
//
// downloads comes from App.jsx's single useDaemonSocket() connection
// (pushed by the daemon over /ws, also used for the header badge) rather
// than this component polling its own HTTP endpoint — no separate poll
// loop needed at all now that the daemon pushes changes as they happen.
function Bucket({ downloads = [] }) {
  const [entries, setEntries] = useState([])
  const [favoriteUrls, setFavoriteUrls] = useState(new Set())
  const [error, setError] = useState(null)
  const [pending, setPending] = useState(null)
  const wasDownloading = useRef(false)

  async function refresh() {
    try {
      const [list, favs] = await Promise.all([getBucketList(), listFavorites()])
      setEntries(list ?? [])
      setFavoriteUrls(new Set((favs ?? []).map((f) => f.url)))
    } catch (err) {
      setError(err.message)
    }
  }

  useEffect(() => {
    refresh()
  }, [])

  // A download finishing means the list is stale — refresh once it drops
  // back to empty, so a newly-cached file shows up without a manual reload.
  useEffect(() => {
    if (downloads.length > 0) {
      wasDownloading.current = true
    } else if (wasDownloading.current) {
      wasDownloading.current = false
      refresh()
    }
  }, [downloads.length])

  async function handleToggleFavorite(url) {
    setError(null)
    setPending(url)
    try {
      if (favoriteUrls.has(url)) {
        await removeFavorite(url)
      } else {
        await addFavorite(url, '')
      }
      await refresh()
    } catch (err) {
      setError(err.message)
    } finally {
      setPending(null)
    }
  }

  return (
    <Stack>
      {error && (
        <Alert color="red" title="Error" withCloseButton onClose={() => setError(null)}>
          {error}
        </Alert>
      )}

      <Title order={5}>Bucket Cache</Title>
      <Text size="sm" c="dimmed">
        Files currently downloaded in the evictable playback cache (see the Settings tab to
        change its size limit or switch modes). Favoriting a track here saves a separate,
        permanent copy — it won't disappear if this cache evicts it.
      </Text>

      {downloads.length > 0 && (
        <Stack gap="xs">
          <Text size="sm" fw={500}>
            Caching now
          </Text>
          {downloads.map((d) => {
            const pct = d.totalBytes > 0 ? Math.min(100, (100 * d.receivedBytes) / d.totalBytes) : null
            return (
              <Card key={d.url} withBorder padding="xs">
                <Group gap="xs" wrap="nowrap" mb={4}>
                  <IconDownload size={14} />
                  <Text size="xs" truncate="end" style={{ flex: 1 }}>
                    {d.url}
                  </Text>
                  <Text size="xs" c="dimmed">
                    {formatBytes(d.receivedBytes)}
                    {d.totalBytes > 0 ? ` / ${formatBytes(d.totalBytes)}` : ''}
                  </Text>
                </Group>
                <Progress value={pct ?? 100} size="sm" animated={pct === null} />
              </Card>
            )
          })}
        </Stack>
      )}

      <Stack gap="xs">
        {entries.length === 0 && (
          <Text c="dimmed" size="sm">
            Bucket is empty — nothing's been downloaded yet (or the daemon is in "Stream
            directly" mode).
          </Text>
        )}
        {entries.map((e) => {
          const isFavorite = favoriteUrls.has(e.url)
          return (
            <Card key={e.url || e.lastAccessed} withBorder padding="sm">
              <Group justify="space-between" wrap="nowrap">
                <Stack gap={0} style={{ minWidth: 0 }}>
                  <Text fw={500} truncate="end">
                    {e.url || '(unlabeled cache entry)'}
                  </Text>
                  <Group gap="xs">
                    <Badge size="xs" variant="light">
                      {formatBytes(e.sizeBytes)}
                    </Badge>
                    <Text size="xs" c="dimmed">
                      last played {new Date(e.lastAccessed).toLocaleString()}
                    </Text>
                  </Group>
                </Stack>
                <ActionIcon
                  variant={isFavorite ? 'filled' : 'subtle'}
                  color="red"
                  disabled={!e.url || pending === e.url}
                  onClick={() => handleToggleFavorite(e.url)}
                  aria-label={isFavorite ? 'Remove from favorites' : 'Add to favorites'}
                >
                  {isFavorite ? <IconHeartFilled size={16} /> : <IconHeart size={16} />}
                </ActionIcon>
              </Group>
            </Card>
          )
        })}
      </Stack>
    </Stack>
  )
}

// Memoized: only re-renders when its own state changes or `downloads`
// actually changes (the daemon only pushes a "downloads" message when the
// snapshot differs from the last one it sent) — not on the once-a-second
// status ticks that flow through the tree while playing.
export default memo(Bucket)
