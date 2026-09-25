import { memo, useEffect, useState } from 'react'
import { Alert, Badge, Button, Card, Group, Stack, Text } from '@mantine/core'
import { IconHeart, IconPlayerPlay } from '@tabler/icons-react'
import { addFavorite, getLibrary, playURL } from '../api.js'

const PAGE_SIZE = 25

// Every URL ever played/favorited/playlisted, most-recently-added first —
// backed by the same persistent search index as the Search tab
// (internal/search, cmd/search-indexer), but browsable without typing a
// query. This is what "remembers every URL added, referred at any point in
// future" actually means in this app: no new storage was needed, since the
// index already persists everything indexed — this just exposes browsing
// it directly instead of requiring a search term.
function Library() {
  const [entries, setEntries] = useState([])
  const [offset, setOffset] = useState(0)
  const [hasMore, setHasMore] = useState(true)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)

  async function loadPage(nextOffset) {
    setLoading(true)
    setError(null)
    try {
      const page = await getLibrary(PAGE_SIZE, nextOffset)
      const results = page ?? []
      setEntries((prev) => (nextOffset === 0 ? results : [...prev, ...results]))
      setOffset(nextOffset + results.length)
      setHasMore(results.length === PAGE_SIZE)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    loadPage(0)
  }, [])

  async function handlePlay(url) {
    setError(null)
    try {
      await playURL(url)
    } catch (err) {
      setError(err.message)
    }
  }

  async function handleAddFavorite(url, title) {
    setError(null)
    try {
      await addFavorite(url, title)
    } catch (err) {
      setError(err.message)
    }
  }

  return (
    <Stack>
      {error && (
        <Alert color="red" title="Error" withCloseButton onClose={() => setError(null)}>
          {error}
        </Alert>
      )}

      <Text size="sm" c="dimmed">
        Every URL you've played, favorited, or added to a playlist, most-recently-added first —
        browse here instead of searching when you just want to see what's already in your
        collection.
      </Text>

      <Stack gap="xs">
        {entries.length === 0 && !loading && (
          <Text c="dimmed" size="sm">
            Nothing indexed yet — play, favorite, or playlist a URL and it'll show up here.
          </Text>
        )}
        {entries.map((e) => (
          <Card key={e.url} withBorder padding="sm">
            <Group justify="space-between" wrap="nowrap">
              <Stack gap={0} style={{ minWidth: 0 }}>
                <Text fw={500} truncate="end">
                  {e.title || e.url}
                </Text>
                {e.title && (
                  <Text size="xs" c="dimmed" truncate="end">
                    {e.url}
                  </Text>
                )}
                {e.tags && (
                  <Group gap={4} mt={4}>
                    {e.tags.split(',').filter(Boolean).map((tag) => (
                      <Badge key={tag} size="xs" variant="light">
                        {tag}
                      </Badge>
                    ))}
                  </Group>
                )}
              </Stack>
              <Group gap="xs" wrap="nowrap">
                <Button
                  size="xs"
                  variant="light"
                  leftSection={<IconPlayerPlay size={14} />}
                  onClick={() => handlePlay(e.url)}
                >
                  Play
                </Button>
                <Button
                  size="xs"
                  variant="light"
                  leftSection={<IconHeart size={14} />}
                  onClick={() => handleAddFavorite(e.url, e.title)}
                >
                  Add to Favorites
                </Button>
              </Group>
            </Group>
          </Card>
        ))}
      </Stack>

      {hasMore && (
        <Button variant="light" onClick={() => loadPage(offset)} loading={loading}>
          Load more
        </Button>
      )}
    </Stack>
  )
}

// Memoized: takes no props from App, so it shouldn't re-render on the
// once-a-second status ticks that flow through the tree while playing.
export default memo(Library)
