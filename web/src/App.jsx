import { useEffect, useState } from 'react'
import {
  ActionIcon,
  AppShell,
  Badge,
  Center,
  Container,
  Group,
  Loader,
  Modal,
  Stack,
  Text,
  Tabs,
} from '@mantine/core'
import { IconBooks, IconDownload, IconLoader2, IconPlaylist, IconSettings } from '@tabler/icons-react'
import { getConfig } from './api.js'
import { useDaemonSocket } from './hooks/useDaemonSocket.js'
import { useHashTab } from './hooks/useHashTab.js'
import Queue from './components/Queue.jsx'
import Library from './components/Library.jsx'
import Settings from './components/Settings.jsx'
import PlayerBar from './components/PlayerBar.jsx'
import NowPlayingScreen from './components/NowPlayingScreen.jsx'

// Library is the one-stop hub for finding, selecting, and managing all
// media (tracks/albums/artists/playlists/favorites/recent/search/add — see
// Library.jsx); Queue stays separate since live playback ordering is a
// genuinely distinct, frequently-used job. Both live as header tabs, top
// and center, rather than the old bottom icon bar — the header has more
// room to spare than the footer does once Settings (below) is no longer
// competing with them for a slot there. Settings itself isn't a tab
// anymore: it's opened via a cog icon at the top right into a full-screen
// Modal, since it's a config/admin screen you visit occasionally, not a
// primary destination that deserves equal billing with Library/Queue in
// the main nav — freeing that slot is what made top-middle tabs fit at
// all.
const TABS = ['library', 'queue']

function App() {
  const { status, downloads, jobs, ready, error } = useDaemonSocket()
  const runningJobs = jobs.filter((j) => j.status === 'running')
  const [tab, setTab] = useHashTab(TABS, 'library')
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [nowPlayingOpen, setNowPlayingOpen] = useState(false)

  // The daemon's own config (currently just ui.hideVolumeControl is read
  // here) — fetched once up front, and again whenever Settings closes,
  // since that's the only place it can change. Not pushed over /ws like
  // status/downloads/jobs: it changes rarely (a user toggling a setting),
  // so a fetch-on-close is simpler than adding a fourth WS envelope type
  // for something this infrequent.
  const [uiConfig, setUiConfig] = useState({})

  function refreshUiConfig() {
    getConfig()
      .then((cfg) => setUiConfig(cfg?.ui ?? {}))
      .catch(() => {})
  }

  useEffect(() => {
    refreshUiConfig()
  }, [])

  function closeSettings() {
    setSettingsOpen(false)
    refreshUiConfig()
  }

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
    // Tabs wraps AppShell.Main (the Tabs.Panels) and the header's Tabs.List
    // — Mantine's Tabs is just a controlled context provider, so List/Panel
    // don't need to be adjacent in the tree, only descendants of the same
    // Tabs.
    <Tabs value={tab} onChange={setTab} keepMounted={false}>
      <AppShell header={{ height: 60 }} footer={{ height: 80 }} padding="md">
        <AppShell.Header>
          {/* Three flex sections of equal flex:1 (left spacer, right
              badges+cog) center the Tabs.List between them regardless of
              how wide either side's actual content is — that's what makes
              this "top middle" rather than just left-aligned next to
              whatever happens to be on the left. */}
          <Group h="100%" px="md" wrap="nowrap" gap="xs">
            <Group style={{ flex: 1 }} wrap="nowrap">
              <Text fw={700} size="sm" style={{ whiteSpace: 'nowrap' }}>
                Pi Streamer
              </Text>
            </Group>
            <Tabs.List>
              <Tabs.Tab value="library" leftSection={<IconBooks size={16} />}>
                Library
              </Tabs.Tab>
              <Tabs.Tab value="queue" leftSection={<IconPlaylist size={16} />}>
                Queue
              </Tabs.Tab>
            </Tabs.List>
            <Group style={{ flex: 1 }} justify="flex-end" gap="xs" wrap="nowrap">
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
              {runningJobs.length > 0 && (
                <Badge
                  variant="light"
                  color="grape"
                  leftSection={<IconLoader2 size={12} />}
                  title={runningJobs.map((j) => j.name).join('\n')}
                >
                  {runningJobs.length === 1 ? runningJobs[0].name : `${runningJobs.length} jobs running`}
                </Badge>
              )}
              <ActionIcon
                variant="subtle"
                size="lg"
                onClick={() => setSettingsOpen(true)}
                aria-label="Settings"
              >
                <IconSettings size={20} />
              </ActionIcon>
            </Group>
          </Group>
        </AppShell.Header>

        <AppShell.Main>
          <Container size="sm">
            {/* keepMounted=false: Mantine otherwise renders every tab's
                panel (and keeps it mounted, effects/polling and all) at
                once, so switching tabs never unmounts anything — on a
                low-power mobile browser that's every tab's worth of
                intervals/WebSockets and re-renders running concurrently
                forever. Only the active tab's panel exists now; the
                others tear down. */}
            <Tabs.Panel value="library" pt="md">
              <Library status={status} />
            </Tabs.Panel>
            <Tabs.Panel value="queue" pt="md">
              <Queue status={status} />
            </Tabs.Panel>
          </Container>
        </AppShell.Main>

        <AppShell.Footer>
          <PlayerBar
            status={status}
            onExpand={() => setNowPlayingOpen(true)}
            hideVolumeControl={uiConfig.hideVolumeControl}
          />
        </AppShell.Footer>
      </AppShell>

      <Modal
        opened={settingsOpen}
        onClose={closeSettings}
        title="Settings"
        fullScreen
        transitionProps={{ transition: 'slide-left' }}
      >
        <Settings downloads={downloads} jobs={jobs} />
      </Modal>

      {/* No title/close-button chrome — NowPlayingScreen provides its own
          back affordance (a chevron at the top), for an immersive
          full-screen look rather than a generic modal dialog. */}
      <Modal
        opened={nowPlayingOpen}
        onClose={() => setNowPlayingOpen(false)}
        fullScreen
        withCloseButton={false}
        transitionProps={{ transition: 'slide-up' }}
      >
        <NowPlayingScreen
          status={status}
          onClose={() => setNowPlayingOpen(false)}
          hideVolumeControl={uiConfig.hideVolumeControl}
        />
      </Modal>
    </Tabs>
  )
}

export default App
