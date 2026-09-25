import { memo, useEffect, useState } from 'react'
import { Alert, Button, Card, Group, Stack, Text } from '@mantine/core'
import { IconPlayerPlay, IconRefresh } from '@tabler/icons-react'
import { getHistory, playURL } from '../api.js'

function formatPlayedAt(playedAt) {
  if (!playedAt) return ''
  const date = new Date(playedAt)
  if (Number.isNaN(date.getTime()) || date.getFullYear() <= 1) return ''
  return date.toLocaleString()
}

function History() {
  const [history, setHistory] = useState([])
  const [error, setError] = useState(null)

  async function refresh() {
    try {
      const h = await getHistory(50)
      setHistory(h ?? [])
    } catch (err) {
      setError(err.message)
    }
  }

  useEffect(() => {
    refresh()
  }, [])

  async function handlePlay(url) {
    setError(null)
    try {
      await playURL(url)
      await refresh()
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

      <Group justify="space-between">
        <Text size="sm" c="dimmed">
          Most recent plays
        </Text>
        <Button
          size="xs"
          variant="subtle"
          onClick={refresh}
          leftSection={<IconRefresh size={16} />}
        >
          Refresh
        </Button>
      </Group>

      <Stack gap="xs">
        {history.length === 0 && (
          <Text c="dimmed" size="sm">
            Nothing played yet.
          </Text>
        )}
        {history.map((t, i) => (
          <Card key={`${t.url}-${i}`} withBorder padding="sm">
            <Group justify="space-between" wrap="nowrap">
              <Stack gap={0} style={{ minWidth: 0 }}>
                <Text fw={500} truncate="end">
                  {t.title || t.url}
                </Text>
                <Text size="xs" c="dimmed" truncate="end">
                  {formatPlayedAt(t.playedAt)}
                </Text>
              </Stack>
              <Button
                size="xs"
                variant="light"
                onClick={() => handlePlay(t.url)}
                leftSection={<IconPlayerPlay size={14} />}
              >
                Play again
              </Button>
            </Group>
          </Card>
        ))}
      </Stack>
    </Stack>
  )
}

// Memoized: takes no props from App, so it shouldn't re-render on the
// once-a-second status ticks that flow through the tree while playing.
export default memo(History)
