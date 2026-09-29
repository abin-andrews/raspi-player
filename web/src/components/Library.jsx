import { memo, useEffect, useState } from 'react'
import {
  ActionIcon,
  Alert,
  Anchor,
  Badge,
  Button,
  Card,
  Chip,
  Group,
  Modal,
  ScrollArea,
  SegmentedControl,
  SimpleGrid,
  Stack,
  Text,
  TextInput,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconArrowLeft,
  IconChevronLeft,
  IconChevronRight,
  IconLayoutGrid,
  IconList,
  IconPencil,
  IconPlayerPlay,
  IconPlaylistAdd,
  IconRefresh,
  IconSearch,
  IconTrash,
  IconX,
} from '@tabler/icons-react'
import {
  addFavorite,
  addToLibrary,
  addToQueue,
  getLibrary,
  listFavorites,
  playURL,
  refreshAlbumArt,
  removeFavorite,
  removeFromLibrary,
  search,
} from '../api.js'
import { useCachedUrls } from '../hooks/useCachedUrls.js'
import { useAlbumArtStatus } from '../hooks/useAlbumArtStatus.js'
import { useLocalStorageState } from '../hooks/useLocalStorageState.js'
import CustomArtControl from './CustomArtControl.jsx'
import LibraryEntryForm from './LibraryEntryForm.jsx'
import LibraryEntryRow from './LibraryEntryRow.jsx'
import LibraryEntryGridCard from './LibraryEntryGridCard.jsx'
import TrackArt from './TrackArt.jsx'
import Playlists from './Playlists.jsx'
import History from './History.jsx'

const PAGE_SIZE = 25
const SEARCH_LIMIT = 50
const ENTITY_ART_SIZE = 100
const EMPTY_FORM = { url: '', title: '', artist: '', album: '', tags: '' }

const SUB_VIEWS = [
  { value: 'tracks', label: 'Tracks' },
  { value: 'albums', label: 'Albums' },
  { value: 'artists', label: 'Artists' },
  { value: 'playlists', label: 'Playlists' },
  { value: 'favorites', label: 'Favorites' },
  { value: 'recent', label: 'Recent' },
]

// Client-side grouping — used both for the top-level Albums/Artists index
// (grouping allEntries, the whole library) and, within an artist's detail
// page, for grouping that one artist's tracks by album. Not a new
// paginated-by-album/by-artist backend endpoint — a deliberate scope call
// for a personal-scale library (see the allEntries note above).
function groupBy(list, field) {
  const groups = new Map()
  for (const e of list) {
    const key = e[field] || '(Unknown)'
    if (!groups.has(key)) groups.set(key, [])
    groups.get(key).push(e)
  }
  return [...groups.entries()].sort(([a], [b]) => {
    if (a === '(Unknown)') return 1
    if (b === '(Unknown)') return -1
    return a.localeCompare(b)
  })
}

// The unified Library tab: add/browse/search/manage every URL ever
// played/favorited/playlisted, plus saved Playlists and Recently Played —
// this is the one place to find and select media on a phone, replacing
// what used to be five separate tabs (Search, Library, Favorites,
// Playlists, History) and absorbing Now Playing's one unique job (paste a
// URL, play it immediately — the persistent PlayerBar footer already
// covers "what's playing" everywhere, so a dedicated tab for that was
// redundant).
function Library({ status }) {
  // Edit form — opened in a Modal (not an inline panel) since Edit can be
  // triggered from any row, however far down a long/paginated list; a
  // Modal is always front-and-center regardless of scroll position.
  const [formValues, setFormValues] = useState(EMPTY_FORM)
  const [editingUrl, setEditingUrl] = useState(null)
  const [submitting, setSubmitting] = useState(false)

  // Search — a separate render path from the paginated browse list; v1
  // keeps search results flat/unpaginated (a generous limit, no "Load
  // more") to avoid changing internal/search.Search's signature. The query
  // text itself persists to localStorage (useLocalStorageState) so it
  // survives a reload; the mount effect below re-runs the search against
  // it then, so the results come back too, not just the box's text.
  const [query, setQuery] = useLocalStorageState('library.query', '')
  const [searchResults, setSearchResults] = useState(null)
  const [searching, setSearching] = useState(false)

  // Browse (Tracks): real Previous/Next pagination — each page *replaces*
  // entries rather than accumulating ("Load more" made the list grow
  // without bound and never gave any sense of where you were in it).
  // page is 0-indexed; hasNext is a heuristic (a full page came back, so
  // there's *probably* another) since internal/search.List doesn't return
  // a total count — matches the same heuristic "Load more" used before.
  const [entries, setEntries] = useState([])
  const [page, setPage] = useState(0)
  const [hasNext, setHasNext] = useState(false)
  const [loading, setLoading] = useState(false)

  // Albums/Artists/Favorites need the *whole* library to group/cross-reference
  // correctly, not just whatever page Tracks happens to be showing — grouping
  // over a single 25-item page meant most albums/artists never showed up at
  // all (recently-added tracks without metadata yet dominate page 1, pushing
  // everything else off it). This is a separate fetch from Tracks' own
  // Previous/Next pagination, kept in sync with it (reloaded on the same
  // add/edit/delete events) but not tied to which Tracks page is displayed.
  // A single generous-limit request, not a full paginating loop: matches the
  // existing "personal-scale library" scope call this codebase already makes
  // for search (SEARCH_LIMIT) and Albums/Artists grouping.
  const ALL_ENTRIES_LIMIT = 2000
  const [allEntries, setAllEntries] = useState([])

  // Which pill (Tracks/Albums/Artists/Playlists/Favorites/Recent) persists
  // across reloads too, same as query/viewMode below.
  const [subView, setSubView] = useLocalStorageState('library.subView', 'tracks')
  // Grid/list persists across reloads too — a personal preference like the
  // search query above, not per-session throwaway state.
  const [viewMode, setViewMode] = useLocalStorageState('library.viewMode', 'list')

  // Clicking a track's artist/album (from any sub-view, including Tracks/
  // Favorites/search results) jumps to the Artists/Albums sub-view scoped to
  // just that one — activeFilter narrows the groups rendered there down to a
  // single match instead of the full grouped list. Switching sub-views via
  // the Chip row itself clears it (handleSubViewChange below), so it never
  // sticks around and silently filters a tab the user picked directly.
  const [activeFilter, setActiveFilter] = useState(null)

  function handleSubViewChange(value) {
    setActiveFilter(null)
    setSubView(value)
  }

  function handleArtistClick(artist) {
    if (!artist) return
    setActiveFilter({ type: 'artist', value: artist })
    setSubView('artists')
  }

  function handleAlbumClick(album) {
    if (!album) return
    setActiveFilter({ type: 'album', value: album })
    setSubView('albums')
  }

  // Track detail page — opened from any track row/card's title or "View
  // track details" menu item, from any sub-view (Tracks/Albums/Artists
  // detail/Favorites/search results alike). Stored as just the url so the
  // rendered page always reflects the latest data (see openTrackEntry
  // below), not a snapshot taken at the moment it was opened — matters
  // after editing the very track whose detail page is open.
  const [openTrack, setOpenTrack] = useState(null)

  function handleOpenTrack(entry) {
    setOpenTrack(entry)
  }

  const [favorites, setFavorites] = useState([])
  const [error, setError] = useState(null)
  // Tracks which single URL's Play request is currently in flight, so the
  // exact row/card/detail-page button that was clicked can show a spinner
  // instead of its Play icon — a fresh PlayURL can take a real, visible
  // while to respond for a YouTube link (the daemon extracts its audio via
  // yt-dlp before mpd ever starts playing — see CLAUDE.md's YouTube-URL
  // architecture note), so without this the button just looks unresponsive
  // for however long that takes.
  const [pendingPlayUrl, setPendingPlayUrl] = useState(null)

  const listAndGridEnabled = subView === 'tracks' || subView === 'albums' || subView === 'artists'
  const effectiveViewMode = listAndGridEnabled ? viewMode : 'list'

  async function loadPage(nextPage) {
    setLoading(true)
    setError(null)
    try {
      const results = (await getLibrary(PAGE_SIZE, nextPage * PAGE_SIZE)) ?? []
      setEntries(results)
      setPage(nextPage)
      setHasNext(results.length === PAGE_SIZE)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  async function loadAllEntries() {
    try {
      setAllEntries((await getLibrary(ALL_ENTRIES_LIMIT, 0)) ?? [])
    } catch (err) {
      setError(err.message)
    }
  }

  async function refreshFavorites() {
    try {
      setFavorites((await listFavorites()) ?? [])
    } catch (err) {
      setError(err.message)
    }
  }

  // A persisted query (see useLocalStorageState above) restores the search
  // box's text instantly, but not the results — the mount effect below
  // re-runs it once so a reload lands back on the same results, not just
  // the same box contents. Deliberately mount-only: including query/
  // handleSearch in the dependency array would re-run the search on every
  // keystroke instead of once at startup.
  useEffect(() => {
    loadPage(0)
    loadAllEntries()
    refreshFavorites()
    if (query.trim()) handleSearch()
  }, [])

  const favoriteUrls = new Set(favorites.map((f) => f.url))
  const cached = useCachedUrls(entries.map((e) => e.url))
  // Whichever list is actually on screen (search results override the
  // paginated browse list entirely — see the render branch below) is what
  // needs its art status known, so the Grid view can skip straight to a
  // placeholder for confirmed-no-art tracks instead of always trying <img>.
  const [artOverrides, setArtOverrides] = useState({})
  const queriedArtStatus = useAlbumArtStatus(
    (searchResults !== null ? searchResults : entries).map((e) => e.url),
  )
  // A "Re-fetch art" action refreshes one specific track — the batched
  // query hook above only re-checks when the *set* of URLs on screen
  // changes, not when a single one's already-known answer is deliberately
  // overwritten, so a manual override takes precedence for anything just
  // refreshed until the next full re-query.
  const artStatus = { ...queriedArtStatus, ...artOverrides }

  // Re-look-up by url against the whole library on every render, rather than
  // rendering the snapshot passed to handleOpenTrack — so the detail page
  // reflects an edit made while it's open instead of going stale.
  const openTrackEntry = openTrack ? allEntries.find((e) => e.url === openTrack.url) ?? openTrack : null

  async function handleSearch() {
    const q = query.trim()
    if (!q) return
    setSearching(true)
    setError(null)
    try {
      const results = await search(q, SEARCH_LIMIT)
      setSearchResults(results ?? [])
    } catch (err) {
      setError(err.message)
    } finally {
      setSearching(false)
    }
  }

  function handleClearSearch() {
    setQuery('')
    setSearchResults(null)
  }

  function handleEdit(entry) {
    setFormValues({
      url: entry.url,
      title: entry.title || '',
      artist: entry.artist || '',
      album: entry.album || '',
      tags: entry.tags || '',
    })
    setEditingUrl(entry.url) // non-null opens the edit Modal — see its `opened` prop below
  }

  function closeForm() {
    setEditingUrl(null)
    setFormValues(EMPTY_FORM)
  }

  async function handleFormSubmit() {
    const { url, title, artist, album, tags } = formValues
    if (!url.trim()) return
    setSubmitting(true)
    setError(null)
    try {
      await addToLibrary(url.trim(), title.trim(), artist.trim(), album.trim(), tags.trim())
      notifications.show({
        color: 'green',
        title: 'Library entry updated',
        message: title.trim() || url.trim(),
      })
      closeForm()
      await Promise.all([loadPage(page), loadAllEntries()])
    } catch (err) {
      setError(err.message)
      notifications.show({ color: 'red', title: 'Could not save', message: err.message })
    } finally {
      setSubmitting(false)
    }
  }

  async function handleDelete(entry) {
    if (!window.confirm(`Remove "${entry.title || entry.url}" from your library?`)) return
    setError(null)
    try {
      await removeFromLibrary(entry.url)
      notifications.show({ color: 'green', title: 'Removed from library', message: entry.title || entry.url })
      setEntries((prev) => prev.filter((e) => e.url !== entry.url))
      setAllEntries((prev) => prev.filter((e) => e.url !== entry.url))
      if (searchResults) setSearchResults((prev) => prev.filter((e) => e.url !== entry.url))
      setOpenTrack((cur) => (cur?.url === entry.url ? null : cur))
    } catch (err) {
      setError(err.message)
      notifications.show({ color: 'red', title: 'Could not remove', message: err.message })
    }
  }

  async function handleRefreshArt(entry) {
    setError(null)
    try {
      const status = await refreshAlbumArt(entry.url, entry.artist, entry.album)
      setArtOverrides((prev) => ({ ...prev, [entry.url]: status }))
      notifications.show({
        color: 'green',
        title: status.hasArt ? 'Art updated' : 'Still no art found',
        message: entry.title || entry.url,
      })
    } catch (err) {
      setError(err.message)
      notifications.show({ color: 'red', title: 'Could not re-fetch art', message: err.message })
    }
  }

  async function handlePlay(url) {
    setError(null)
    setPendingPlayUrl(url)
    try {
      await playURL(url)
    } catch (err) {
      setError(err.message)
    } finally {
      setPendingPlayUrl(null)
    }
  }

  async function handleQueue(url, title) {
    setError(null)
    try {
      await addToQueue(url)
      notifications.show({ color: 'green', title: 'Added to queue', message: title || url })
    } catch (err) {
      setError(err.message)
      notifications.show({ color: 'red', title: 'Could not add to queue', message: err.message })
    }
  }

  async function handleToggleFavorite(url, title) {
    setError(null)
    try {
      if (favoriteUrls.has(url)) {
        await removeFavorite(url)
      } else {
        await addFavorite(url, title || '')
      }
      await refreshFavorites()
    } catch (err) {
      setError(err.message)
    }
  }

  function renderEntry(entry) {
    const isPlaying = Boolean(status?.song) && entry.url === status.song
    const isFavorite = favoriteUrls.has(entry.url)
    const isPending = pendingPlayUrl === entry.url
    return effectiveViewMode === 'grid' ? (
      <LibraryEntryGridCard
        key={entry.url}
        entry={entry}
        isPlaying={isPlaying}
        isPending={isPending}
        isFavorite={isFavorite}
        artStatus={artStatus[entry.url]}
        onPlay={handlePlay}
        onQueue={handleQueue}
        onToggleFavorite={handleToggleFavorite}
        onEdit={handleEdit}
        onDelete={handleDelete}
        onRefreshArt={handleRefreshArt}
        onOpenDetail={handleOpenTrack}
        onArtistClick={handleArtistClick}
        onAlbumClick={handleAlbumClick}
      />
    ) : (
      <LibraryEntryRow
        key={entry.url}
        entry={entry}
        isPlaying={isPlaying}
        isPending={isPending}
        isFavorite={isFavorite}
        cached={cached[entry.url]}
        onPlay={handlePlay}
        onQueue={handleQueue}
        onToggleFavorite={handleToggleFavorite}
        onEdit={handleEdit}
        onDelete={handleDelete}
        onOpenDetail={handleOpenTrack}
        onArtistClick={handleArtistClick}
        onAlbumClick={handleAlbumClick}
      />
    )
  }

  function renderEntryList(list) {
    if (list.length === 0) {
      return (
        <Text c="dimmed" size="sm">
          Nothing here yet.
        </Text>
      )
    }
    return effectiveViewMode === 'grid' ? (
      <SimpleGrid cols={{ base: 2, sm: 3, md: 4 }} spacing="xs">
        {list.map(renderEntry)}
      </SimpleGrid>
    ) : (
      <Stack gap="xs">{list.map(renderEntry)}</Stack>
    )
  }

  // Track detail page: everything about one track in one place, including
  // clickable links to its artist/album pages — reachable by clicking a
  // track's title anywhere (Tracks/Favorites/search/an album or artist
  // page's own track list) or via that row/card's "View track details"
  // menu item. The "Go to artist"/"Go to album" menu items on every row/
  // card jump straight to those pages without opening this one at all —
  // this page is for viewing everything about the track itself (tags, raw
  // URL, larger art), not the only way to reach its artist/album.
  function renderTrackDetail(entry) {
    const isPlaying = Boolean(status?.song) && entry.url === status.song
    const isFavorite = favoriteUrls.has(entry.url)
    return (
      <Stack gap="md">
        <Group justify="flex-end">
          <Button
            variant="subtle"
            size="xs"
            leftSection={<IconArrowLeft size={14} />}
            onClick={() => setOpenTrack(null)}
          >
            Back
          </Button>
        </Group>
        <Group align="flex-start" wrap="nowrap">
          <TrackArt
            url={entry.url}
            label={entry.album || entry.title || entry.url}
            artist={entry.artist}
            album={entry.album}
            size={160}
          />
          <Stack gap={6} style={{ minWidth: 0, flex: 1 }}>
            <Text fw={700} size="lg" truncate="end">
              {entry.title || entry.url}
            </Text>
            {isPlaying && (
              <Badge size="xs" color="blue" variant="light" style={{ alignSelf: 'flex-start' }}>
                Now Playing
              </Badge>
            )}
            {entry.artist && (
              <Group gap={4}>
                <Text size="xs" c="dimmed">
                  Artist
                </Text>
                <Anchor component="button" type="button" size="sm" onClick={() => handleArtistClick(entry.artist)}>
                  {entry.artist}
                </Anchor>
              </Group>
            )}
            {entry.album && (
              <Group gap={4}>
                <Text size="xs" c="dimmed">
                  Album
                </Text>
                <Anchor component="button" type="button" size="sm" onClick={() => handleAlbumClick(entry.album)}>
                  {entry.album}
                </Anchor>
              </Group>
            )}
            {entry.tags && (
              <Text size="xs" c="dimmed">
                Tags: {entry.tags}
              </Text>
            )}
            <Text size="xs" c="dimmed" truncate="end">
              {entry.url}
            </Text>
          </Stack>
        </Group>
        <Group>
          <Button
            leftSection={<IconPlayerPlay size={16} />}
            loading={pendingPlayUrl === entry.url}
            onClick={() => handlePlay(entry.url)}
          >
            Play
          </Button>
          <Button
            variant="light"
            leftSection={<IconPlaylistAdd size={16} />}
            onClick={() => handleQueue(entry.url, entry.title)}
          >
            Add to queue
          </Button>
          <Button
            variant={isFavorite ? 'filled' : 'light'}
            color="red"
            onClick={() => handleToggleFavorite(entry.url, entry.title)}
          >
            {isFavorite ? 'Remove favorite' : 'Add favorite'}
          </Button>
          <Button variant="subtle" leftSection={<IconPencil size={16} />} onClick={() => handleEdit(entry)}>
            Edit
          </Button>
          <Button variant="subtle" leftSection={<IconRefresh size={16} />} onClick={() => handleRefreshArt(entry)}>
            Re-fetch art
          </Button>
          <Button
            variant="subtle"
            color="red"
            leftSection={<IconTrash size={16} />}
            onClick={() => handleDelete(entry)}
          >
            Delete
          </Button>
        </Group>
        <CustomArtControl
          scope="track"
          artKey={entry.url}
          label={entry.title || entry.url}
          onChanged={() => handleRefreshArt(entry)}
        />
      </Stack>
    )
  }

  // Albums/Artists index: a clickable directory of every album/artist in
  // the library (name + track count), not a dump of every track inline —
  // this is the "page" the user drills into via renderEntityDetail below.
  // Tracks with no album/artist tag ((Unknown)) have nothing to link to, so
  // they're excluded from the directory itself but still counted, in a
  // small note, so their tracks aren't silently unaccounted for.
  function renderEntityIndex(type) {
    const allGroups = groupBy(allEntries, type)
    const groups = allGroups.filter(([key]) => key !== '(Unknown)')
    const unknown = allGroups.find(([key]) => key === '(Unknown)')

    function openEntity(name) {
      if (type === 'album') handleAlbumClick(name)
      else handleArtistClick(name)
    }

    return (
      <Stack gap="md">
        {groups.length === 0 && (
          <Text c="dimmed" size="sm">
            Nothing here yet.
          </Text>
        )}
        {effectiveViewMode === 'grid' ? (
          <SimpleGrid cols={{ base: 2, sm: 3, md: 4 }} spacing="xs">
            {groups.map(([name, list]) => (
              <Card
                key={name}
                withBorder
                padding="xs"
                onClick={() => openEntity(name)}
                style={{ cursor: 'pointer' }}
              >
                <Stack gap={4} align="center">
                  <TrackArt
                    url={type === 'album' ? list[0]?.url : undefined}
                    label={name}
                    artist={type === 'album' ? list[0]?.artist : name}
                    album={type === 'album' ? name : undefined}
                    size={ENTITY_ART_SIZE}
                  />
                  <Text size="sm" fw={500} truncate="end" w="100%" ta="center">
                    {name}
                  </Text>
                  <Text size="xs" c="dimmed">
                    {list.length} track{list.length === 1 ? '' : 's'}
                  </Text>
                </Stack>
              </Card>
            ))}
          </SimpleGrid>
        ) : (
          <Stack gap="xs">
            {groups.map(([name, list]) => (
              <Card
                key={name}
                withBorder
                padding="sm"
                onClick={() => openEntity(name)}
                style={{ cursor: 'pointer' }}
              >
                <Group justify="space-between" wrap="nowrap">
                  <Text fw={500} truncate="end">
                    {name}
                  </Text>
                  <Group gap={4} wrap="nowrap">
                    <Text size="xs" c="dimmed">
                      {list.length} track{list.length === 1 ? '' : 's'}
                    </Text>
                    <IconChevronRight size={16} />
                  </Group>
                </Group>
              </Card>
            ))}
          </Stack>
        )}
        {unknown && (
          <Text size="xs" c="dimmed">
            {unknown[1].length} track{unknown[1].length === 1 ? '' : 's'} with no {type} tag set — edit a
            track to give it one so it shows up here.
          </Text>
        )}
      </Stack>
    )
  }

  // Albums/Artists detail page — where a click on an album/artist name
  // (from the index above, or from any track row's clickable artist/album
  // text) actually lands. An album page cross-links back to every artist
  // that appears on it; an artist page groups its own tracks by album (so
  // artist -> album -> tracks is one page, not a separate hop) and
  // cross-links each album onward to *its* detail page.
  function renderEntityDetail(type, value) {
    const tracks = allEntries.filter((e) => (e[type] || '(Unknown)') === value)

    const header = (
      <Group justify="space-between" align="flex-start" wrap="nowrap">
        <Stack gap={2} style={{ minWidth: 0 }}>
          <Text size="xs" c="dimmed">
            {type === 'album' ? 'Album' : 'Artist'}
          </Text>
          <Text fw={700} size="lg" truncate="end">
            {value}
          </Text>
        </Stack>
        <Button
          variant="subtle"
          size="xs"
          leftSection={<IconArrowLeft size={14} />}
          onClick={() => setActiveFilter(null)}
        >
          Back
        </Button>
      </Group>
    )

    if (type === 'album') {
      const artists = [...new Set(tracks.map((e) => e.artist).filter(Boolean))].sort()
      return (
        <Stack gap="md">
          {header}
          {artists.length > 0 && (
            <Group gap={4}>
              <Text size="xs" c="dimmed">
                By
              </Text>
              {artists.map((a) => (
                <Anchor key={a} component="button" type="button" size="xs" onClick={() => handleArtistClick(a)}>
                  {a}
                </Anchor>
              ))}
            </Group>
          )}
          <Text size="xs" c="dimmed">
            {tracks.length} track{tracks.length === 1 ? '' : 's'}
          </Text>
          <CustomArtControl scope="album" artKey={value} label={value} />
          {renderEntryList(tracks)}
        </Stack>
      )
    }

    // type === 'artist'
    const albumGroups = groupBy(tracks, 'album')
    return (
      <Stack gap="md">
        {header}
        <Text size="xs" c="dimmed">
          {tracks.length} track{tracks.length === 1 ? '' : 's'} across {albumGroups.length} album
          {albumGroups.length === 1 ? '' : 's'}
        </Text>
        <CustomArtControl scope="artist" artKey={value} label={value} />
        {albumGroups.map(([album, albumTracks]) => (
          <Stack key={album} gap="xs">
            {album === '(Unknown)' ? (
              <Text fw={600} size="sm" c="dimmed">
                Singles / no album
              </Text>
            ) : (
              <Anchor
                component="button"
                type="button"
                fw={600}
                size="sm"
                onClick={() => handleAlbumClick(album)}
              >
                {album}
              </Anchor>
            )}
            {renderEntryList(albumTracks)}
          </Stack>
        ))}
      </Stack>
    )
  }

  function renderBrowseContent() {
    if (subView === 'playlists') return <Playlists />
    if (subView === 'recent') return <History />

    if (subView === 'favorites') {
      const byUrl = new Map(allEntries.map((e) => [e.url, e]))
      const favoriteEntries = favorites.map(
        (f) => byUrl.get(f.url) ?? { url: f.url, title: f.title, artist: '', album: '', tags: '' },
      )
      return renderEntryList(favoriteEntries)
    }

    if (subView === 'albums' || subView === 'artists') {
      const type = subView === 'albums' ? 'album' : 'artist'
      return activeFilter?.type === type ? renderEntityDetail(type, activeFilter.value) : renderEntityIndex(type)
    }

    // Tracks
    return (
      <Stack gap="xs">
        {renderEntryList(entries)}
        <Group justify="center" gap="xs">
          <ActionIcon
            variant="light"
            size="lg"
            disabled={page === 0 || loading}
            onClick={() => loadPage(page - 1)}
            aria-label="Previous page"
          >
            <IconChevronLeft size={18} />
          </ActionIcon>
          <Text size="sm" c="dimmed">
            Page {page + 1}
          </Text>
          <ActionIcon
            variant="light"
            size="lg"
            disabled={!hasNext || loading}
            onClick={() => loadPage(page + 1)}
            aria-label="Next page"
          >
            <IconChevronRight size={18} />
          </ActionIcon>
        </Group>
      </Stack>
    )
  }

  return (
    <Stack>
      {error && (
        <Alert color="red" title="Error" withCloseButton onClose={() => setError(null)}>
          {error}
        </Alert>
      )}

      <Modal opened={editingUrl !== null} onClose={closeForm} title="Edit library entry">
        <LibraryEntryForm
          values={formValues}
          onChange={setFormValues}
          onSubmit={handleFormSubmit}
          onCancel={closeForm}
          submitting={submitting}
        />
      </Modal>

      {/* Search — a separate render path from browsing; while active, it
          replaces whatever sub-view is selected below. */}
      <Group align="flex-end">
        <TextInput
          label="Search your library"
          placeholder="Title, artist, album…"
          leftSection={<IconSearch size={16} />}
          value={query}
          onChange={(e) => setQuery(e.currentTarget.value)}
          onKeyDown={(e) => e.key === 'Enter' && handleSearch()}
          style={{ flex: 1 }}
        />
        <Button onClick={handleSearch} loading={searching} disabled={!query.trim()}>
          Search
        </Button>
        {searchResults !== null && (
          <ActionIcon variant="subtle" size="lg" onClick={handleClearSearch} aria-label="Clear search">
            <IconX size={18} />
          </ActionIcon>
        )}
      </Group>

      {openTrackEntry ? (
        renderTrackDetail(openTrackEntry)
      ) : searchResults !== null ? (
        <Stack gap="xs">
          <Text size="xs" c="dimmed">
            {searchResults.length} result{searchResults.length === 1 ? '' : 's'} for "{query}"
          </Text>
          {renderEntryList(searchResults)}
        </Stack>
      ) : (
        <>
          <ScrollArea type="auto" scrollbarSize={4}>
            <Group wrap="nowrap" gap="xs">
              <Chip.Group value={subView} onChange={handleSubViewChange}>
                <Group gap="xs" wrap="nowrap">
                  {SUB_VIEWS.map((v) => (
                    <Chip key={v.value} value={v.value} size="sm" variant="filled">
                      {v.label}
                    </Chip>
                  ))}
                </Group>
              </Chip.Group>
            </Group>
          </ScrollArea>

          {listAndGridEnabled && (
            <SegmentedControl
              size="xs"
              value={viewMode}
              onChange={setViewMode}
              style={{ alignSelf: 'flex-start' }}
              data={[
                { value: 'list', label: <IconList size={14} /> },
                { value: 'grid', label: <IconLayoutGrid size={14} /> },
              ]}
            />
          )}

          {renderBrowseContent()}
        </>
      )}
    </Stack>
  )
}

// Memoized with a custom comparator: this component only cares about
// status.song (to highlight the currently-playing row) — not elapsed/
// duration — so without this it would re-render every second while
// something plays, same reasoning as Queue.jsx's own comparator.
export default memo(Library, (prev, next) => prev.status?.song === next.status?.song)
