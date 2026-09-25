import { memo, useState } from 'react'
import {
  Alert,
  Badge,
  Button,
  Card,
  Group,
  Stack,
  Text,
  TextInput,
} from '@mantine/core'
import { IconHeart, IconPlayerPlay, IconSearch } from '@tabler/icons-react'
import { addFavorite, playURL, search } from '../api.js'

// Note: adding a search result to a specific named playlist is intentionally
// not supported here (would need a playlist-picker control). Only "Play" and
// "Add to Favorites" are offered as actions on results.
function Search() {
  const [query, setQuery] = useState('')
  const [results, setResults] = useState([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [hasSearched, setHasSearched] = useState(false)

  async function handleSearch() {
    const q = query.trim()
    if (!q) return
    setError(null)
    setLoading(true)
    try {
      const res = await search(q, 25)
      setResults(res ?? [])
      setHasSearched(true)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  function handleKeyDown(e) {
    if (e.key === 'Enter') handleSearch()
  }

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

      <Group align="flex-end">
        <TextInput
          label="Search"
          placeholder="Search played/favorited tracks"
          leftSection={<IconSearch size={16} />}
          value={query}
          onChange={(e) => setQuery(e.currentTarget.value)}
          onKeyDown={handleKeyDown}
          style={{ flex: 1 }}
        />
        <Button onClick={handleSearch} loading={loading} disabled={!query.trim()}>
          Search
        </Button>
      </Group>

      <Stack gap="xs">
        {!hasSearched && (
          <Text c="dimmed" size="sm">
            Search your played/favorited tracks
          </Text>
        )}
        {hasSearched && results.length === 0 && (
          <Text c="dimmed" size="sm">
            No results for "{query.trim()}".
          </Text>
        )}
        {results.map((r) => (
          <Card key={r.url} withBorder padding="sm">
            <Group justify="space-between" wrap="nowrap">
              <Stack gap={0} style={{ minWidth: 0 }}>
                <Text fw={500} truncate="end">
                  {r.title || r.url}
                </Text>
                {r.title && (
                  <Text size="xs" c="dimmed" truncate="end">
                    {r.url}
                  </Text>
                )}
                {Array.isArray(r.tags) && r.tags.length > 0 && (
                  <Group gap={4} mt={4}>
                    {r.tags.map((tag) => (
                      <Badge key={tag} size="xs" variant="light">
                        {tag}
                      </Badge>
                    ))}
                  </Group>
                )}
                {typeof r.tags === 'string' && r.tags.trim() && (
                  <Text size="xs" c="dimmed" mt={4}>
                    {r.tags}
                  </Text>
                )}
              </Stack>
              <Group gap="xs" wrap="nowrap">
                <Button
                  size="xs"
                  variant="light"
                  leftSection={<IconPlayerPlay size={14} />}
                  onClick={() => handlePlay(r.url)}
                >
                  Play
                </Button>
                <Button
                  size="xs"
                  variant="light"
                  leftSection={<IconHeart size={14} />}
                  onClick={() => handleAddFavorite(r.url, r.title)}
                >
                  Add to Favorites
                </Button>
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
export default memo(Search)
