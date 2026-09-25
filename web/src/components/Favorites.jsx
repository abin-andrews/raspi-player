import { memo, useEffect, useState } from 'react'
import { ActionIcon, Alert, Badge, Button, Card, Group, Stack, Text, TextInput } from '@mantine/core'
import { IconPlayerPlay, IconPlus, IconTrash } from '@tabler/icons-react'
import { addFavorite, listFavorites, playURL, removeFavorite } from '../api.js'
import { useCachedUrls } from '../hooks/useCachedUrls.js'

function Favorites() {
  const [favorites, setFavorites] = useState([])
  const [url, setUrl] = useState('')
  const [title, setTitle] = useState('')
  const [error, setError] = useState(null)
  const cached = useCachedUrls(favorites.map((f) => f.url))

  async function refresh() {
    try {
      const favs = await listFavorites()
      setFavorites(favs ?? [])
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
      await addFavorite(url.trim(), title.trim())
      setUrl('')
      setTitle('')
      await refresh()
    } catch (err) {
      setError(err.message)
    }
  }

  async function handleRemove(favUrl) {
    setError(null)
    try {
      await removeFavorite(favUrl)
      await refresh()
    } catch (err) {
      setError(err.message)
    }
  }

  async function handlePlay(favUrl) {
    setError(null)
    try {
      await playURL(favUrl)
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

      <Group align="flex-end">
        <TextInput
          label="URL"
          placeholder="https://example.com/stream.mp3"
          value={url}
          onChange={(e) => setUrl(e.currentTarget.value)}
          style={{ flex: 1 }}
        />
        <TextInput
          label="Title (optional)"
          value={title}
          onChange={(e) => setTitle(e.currentTarget.value)}
          style={{ flex: 1 }}
        />
        <Button onClick={handleAdd} disabled={!url.trim()} leftSection={<IconPlus size={16} />}>
          Add Favorite
        </Button>
      </Group>

      <Stack gap="xs">
        {favorites.length === 0 && (
          <Text c="dimmed" size="sm">
            No favorites yet.
          </Text>
        )}
        {favorites.map((f) => (
          <Card key={f.url} withBorder padding="sm">
            <Group justify="space-between" wrap="nowrap">
              <Stack gap={0} style={{ minWidth: 0 }}>
                <Group gap="xs" wrap="nowrap">
                  <Text fw={500} truncate="end">
                    {f.title || f.url}
                  </Text>
                  {cached[f.url] && (
                    <Badge size="xs" color="teal" variant="light">
                      Saved offline
                    </Badge>
                  )}
                </Group>
                {f.title && (
                  <Text size="xs" c="dimmed" truncate="end">
                    {f.url}
                  </Text>
                )}
              </Stack>
              <Group gap="xs" wrap="nowrap">
                <Button
                  size="xs"
                  variant="light"
                  onClick={() => handlePlay(f.url)}
                  leftSection={<IconPlayerPlay size={14} />}
                >
                  Play
                </Button>
                <ActionIcon color="red" variant="subtle" onClick={() => handleRemove(f.url)}>
                  <IconTrash size={16} />
                </ActionIcon>
              </Group>
            </Group>
          </Card>
        ))}
      </Stack>
    </Stack>
  )
}

// Memoized: takes no props from App, so it shouldn't re-render on the
// once-a-second status ticks that flow through the tree while playing.
export default memo(Favorites)
