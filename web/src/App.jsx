import { AppShell, Badge, Center, Container, Loader, Stack, Text, Tabs, Title } from '@mantine/core'
import { IconBooks, IconDatabase, IconDownload, IconSearch, IconSettings } from '@tabler/icons-react'
import { useDaemonSocket } from './hooks/useDaemonSocket.js'
import { useHashTab } from './hooks/useHashTab.js'
import NowPlaying from './components/NowPlaying.jsx'
import Favorites from './components/Favorites.jsx'
import Playlists from './components/Playlists.jsx'
import History from './components/History.jsx'
import Search from './components/Search.jsx'
import Queue from './components/Queue.jsx'
import Bucket from './components/Bucket.jsx'
import Library from './components/Library.jsx'
import Settings from './components/Settings.jsx'
import PlayerBar from './components/PlayerBar.jsx'

const TABS = [
  'search',
  'now-playing',
  'queue',
  'favorites',
  'playlists',
  'history',
  'bucket',
  'library',
  'settings',
]

function App() {
  const { status, downloads, ready, error } = useDaemonSocket()
  const [tab, setTab] = useHashTab(TABS, 'now-playing')

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
        <Container h="100%" display="flex" style={{ alignItems: 'center', gap: 12 }}>
          <Title order={3}>pi-streamer</Title>
          {downloads.length > 0 && (
            <Badge
              variant="light"
              color="teal"
              leftSection={<IconDownload size={12} />}
              title={downloads.map((d) => d.url).join('\n')}
            >
              Caching {downloads.length}
            </Badge>
          )}
        </Container>
      </AppShell.Header>

      <AppShell.Main>
        <Container size="sm">
          {/* keepMounted=false: Mantine otherwise renders every tab's panel
              (and keeps it mounted, effects/polling and all) at once, so
              switching tabs never unmounts anything — on a low-power
              mobile browser that's 9 components' worth of intervals/
              WebSockets and re-renders running concurrently forever. Only
              the active tab's panel exists now; the others tear down. */}
          <Tabs value={tab} onChange={setTab} keepMounted={false}>
            <Tabs.List>
              <Tabs.Tab value="search" leftSection={<IconSearch size={16} />}>
                Search
              </Tabs.Tab>
              <Tabs.Tab value="now-playing">Now Playing</Tabs.Tab>
              <Tabs.Tab value="queue">Queue</Tabs.Tab>
              <Tabs.Tab value="favorites">Favorites</Tabs.Tab>
              <Tabs.Tab value="playlists">Playlists</Tabs.Tab>
              <Tabs.Tab value="history">History</Tabs.Tab>
              <Tabs.Tab value="bucket" leftSection={<IconDatabase size={16} />}>
                Bucket
              </Tabs.Tab>
              <Tabs.Tab value="library" leftSection={<IconBooks size={16} />}>
                Library
              </Tabs.Tab>
              <Tabs.Tab value="settings" leftSection={<IconSettings size={16} />}>
                Settings
              </Tabs.Tab>
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
            <Tabs.Panel value="bucket" pt="md">
              <Bucket downloads={downloads} />
            </Tabs.Panel>
            <Tabs.Panel value="library" pt="md">
              <Library />
            </Tabs.Panel>
            <Tabs.Panel value="settings" pt="md">
              <Settings />
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
