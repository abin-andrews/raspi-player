import { AppShell, Center, Container, Loader, Stack, Tabs, Text, Title } from '@mantine/core'
import { IconSearch } from '@tabler/icons-react'
import { usePlaybackStatus } from './hooks/usePlaybackStatus.js'
import NowPlaying from './components/NowPlaying.jsx'
import Favorites from './components/Favorites.jsx'
import Playlists from './components/Playlists.jsx'
import History from './components/History.jsx'
import Search from './components/Search.jsx'
import Queue from './components/Queue.jsx'
import PlayerBar from './components/PlayerBar.jsx'

function App() {
  const { status, ready, error } = usePlaybackStatus()

  // Don't render the real UI until the WebSocket has delivered current
  // state: this is what makes a reload while something's playing seamless
  // instead of showing stale/default controls that then jump to correct
  // values a moment later.
  if (!ready) {
    return (
      <Center h="100vh">
        <Stack align="center" gap="xs">
          {error ? (
            <Text c="red">{error}</Text>
          ) : (
            <>
              <Loader />
              <Text c="dimmed" size="sm">
                Connecting…
              </Text>
            </>
          )}
        </Stack>
      </Center>
    )
  }

  return (
    <AppShell header={{ height: 60 }} footer={{ height: 88 }} padding="md">
      <AppShell.Header>
        <Container h="100%" display="flex" style={{ alignItems: 'center' }}>
          <Title order={3}>pi-streamer</Title>
        </Container>
      </AppShell.Header>

      <AppShell.Main>
        <Container size="sm">
          <Tabs defaultValue="now-playing">
            <Tabs.List>
              <Tabs.Tab value="search" leftSection={<IconSearch size={16} />}>
                Search
              </Tabs.Tab>
              <Tabs.Tab value="now-playing">Now Playing</Tabs.Tab>
              <Tabs.Tab value="queue">Queue</Tabs.Tab>
              <Tabs.Tab value="favorites">Favorites</Tabs.Tab>
              <Tabs.Tab value="playlists">Playlists</Tabs.Tab>
              <Tabs.Tab value="history">History</Tabs.Tab>
            </Tabs.List>

            <Tabs.Panel value="search" pt="md">
              <Search />
            </Tabs.Panel>
            <Tabs.Panel value="now-playing" pt="md">
              <NowPlaying status={status} />
            </Tabs.Panel>
            <Tabs.Panel value="queue" pt="md">
              <Queue status={status} />
            </Tabs.Panel>
            <Tabs.Panel value="favorites" pt="md">
              <Favorites />
            </Tabs.Panel>
            <Tabs.Panel value="playlists" pt="md">
              <Playlists />
            </Tabs.Panel>
            <Tabs.Panel value="history" pt="md">
              <History />
            </Tabs.Panel>
          </Tabs>
        </Container>
      </AppShell.Main>

      <AppShell.Footer>
        <PlayerBar status={status} />
      </AppShell.Footer>
    </AppShell>
  )
}

export default App
