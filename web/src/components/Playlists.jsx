import { memo, useEffect, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Group,
  Select,
  Stack,
  Text,
  TextInput,
} from '@mantine/core'
import { IconPlayerPlay, IconPlus } from '@tabler/icons-react'
import { addToPlaylist, createPlaylist, getPlaylist, listPlaylists, playURL } from '../api.js'

function Playlists() {
  const [playlists, setPlaylists] = useState([])
  const [selected, setSelected] = useState(null)
  const [tracks, setTracks] = useState([])
  const [newPlaylistName, setNewPlaylistName] = useState('')
  const [trackUrl, setTrackUrl] = useState('')
  const [trackTitle, setTrackTitle] = useState('')
  const [error, setError] = useState(null)

  async function refreshPlaylists() {
    try {
      const names = await listPlaylists()
      setPlaylists(names ?? [])
    } catch (err) {
      setError(err.message)
    }
  }

  useEffect(() => {
    refreshPlaylists()
  }, [])

  async function refreshTracks(name) {
    if (!name) {
      setTracks([])
      return
    }
    try {
      const t = await getPlaylist(name)
      setTracks(t ?? [])
    } catch (err) {
      setError(err.message)
    }
  }

  useEffect(() => {
    refreshTracks(selected)
  }, [selected])

  async function handleCreate() {
    if (!newPlaylistName.trim()) return
    setError(null)
    try {
      await createPlaylist(newPlaylistName.trim())
      const name = newPlaylistName.trim()
      setNewPlaylistName('')
      await refreshPlaylists()
      setSelected(name)
    } catch (err) {
      setError(err.message)
    }
  }

  async function handleAddTrack() {
    if (!selected || !trackUrl.trim()) return
    setError(null)
    try {
      await addToPlaylist(selected, trackUrl.trim(), trackTitle.trim())
      setTrackUrl('')
      setTrackTitle('')
      await refreshTracks(selected)
    } catch (err) {
      setError(err.message)
    }
  }

  async function handlePlay(url) {
    setError(null)
    try {
      await playURL(url)
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
          label="New playlist name"
          value={newPlaylistName}
          onChange={(e) => setNewPlaylistName(e.currentTarget.value)}
          style={{ flex: 1 }}
        />
        <Button
          onClick={handleCreate}
          disabled={!newPlaylistName.trim()}
          leftSection={<IconPlus size={16} />}
        >
          Create
        </Button>
      </Group>

      <Select
        label="Playlist"
        placeholder="Select a playlist"
        data={playlists}
        value={selected}
        onChange={setSelected}
        clearable
      />

      {selected && (
        <>
          <Group align="flex-end">
            <TextInput
              label="URL"
              placeholder="https://example.com/stream.mp3"
              value={trackUrl}
              onChange={(e) => setTrackUrl(e.currentTarget.value)}
              style={{ flex: 1 }}
            />
            <TextInput
              label="Title (optional)"
              value={trackTitle}
              onChange={(e) => setTrackTitle(e.currentTarget.value)}
              style={{ flex: 1 }}
            />
            <Button
              onClick={handleAddTrack}
              disabled={!trackUrl.trim()}
              leftSection={<IconPlus size={16} />}
            >
              Add Track
            </Button>
          </Group>

          <Stack gap="xs">
            {tracks.length === 0 && (
              <Text c="dimmed" size="sm">
                No tracks in this playlist yet.
              </Text>
            )}
            {tracks.map((t, i) => (
              <Card key={`${t.url}-${i}`} withBorder padding="sm">
                <Group justify="space-between" wrap="nowrap">
                  <Stack gap={0} style={{ minWidth: 0 }}>
                    <Text fw={500} truncate="end">
                      {t.title || t.url}
                    </Text>
                    {t.title && (
                      <Text size="xs" c="dimmed" truncate="end">
                        {t.url}
                      </Text>
                    )}
                  </Stack>
                  <Button
                    size="xs"
                    variant="light"
                    onClick={() => handlePlay(t.url)}
                    leftSection={<IconPlayerPlay size={14} />}
                  >
                    Play
                  </Button>
                </Group>
              </Card>
            ))}
          </Stack>
        </>
      )}
    </Stack>
  )
}

// Memoized: takes no props from App, so it shouldn't re-render on the
// once-a-second status ticks that flow through the tree while playing.
export default memo(Playlists)
