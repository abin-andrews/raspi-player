import { ActionIcon, Anchor, Badge, Card, Group, Menu, Stack, Text } from '@mantine/core'
import {
  IconBrandYoutube,
  IconDisc,
  IconDotsVertical,
  IconHeart,
  IconHeartFilled,
  IconInfoCircle,
  IconPencil,
  IconPlayerPlay,
  IconPlaylistAdd,
  IconRefresh,
  IconTrash,
  IconUser,
} from '@tabler/icons-react'
import { isYouTubeUrl } from '../isYouTubeUrl.js'
import TrackArt from './TrackArt.jsx'

// One track's grid-view tile — a full-bleed "poster" card (album art fills
// the entire tile, title/artist/album overlaid on top of it behind a dark
// gradient) rather than a small thumbnail with text below it, matching the
// media-app look (Spotify/Apple Music-style tiles) this is going for. The
// gradient (transparent at the top, solid near the bottom) is what keeps
// the overlaid text readable regardless of how light or busy the
// underlying art is — needed since a real photo/thumbnail's colors are
// unpredictable, unlike a plain background color card would be. TrackArt
// itself fills the tile (size="100%") so the art also scales up with the
// grid cell's real width, not a small fixed pixel box regardless of how
// much room the tile actually has.
function LibraryEntryGridCard({
  entry,
  isPlaying,
  isPending,
  isFavorite,
  artStatus,
  onPlay,
  onQueue,
  onToggleFavorite,
  onEdit,
  onDelete,
  onRefreshArt,
  onOpenDetail,
  onArtistClick,
  onAlbumClick,
}) {
  return (
    <Card
      withBorder
      padding={0}
      radius="md"
      style={{
        overflow: 'hidden',
        position: 'relative',
        ...(isPlaying ? { borderColor: 'var(--mantine-color-blue-6)', borderWidth: 2 } : undefined),
      }}
    >
      <TrackArt
        url={entry.url}
        label={entry.album || entry.title || entry.url}
        artist={entry.artist}
        album={entry.album}
        knownHasArt={artStatus?.hasArt}
        resolvedPath={artStatus?.path}
        size="100%"
        radius={0}
      />

      {/* Transparent-to-solid gradient, not a flat scrim: keeps the top of
          the art (and the badge/menu sitting on it) fully visible while
          still giving the bottom-anchored text enough contrast to read
          against any underlying image. */}
      <div
        style={{
          position: 'absolute',
          inset: 0,
          background: 'linear-gradient(to bottom, transparent 45%, rgba(0, 0, 0, 0.85) 100%)',
          pointerEvents: 'none',
        }}
      />

      {isYouTubeUrl(entry.url) && (
        <span
          title="From YouTube"
          style={{
            position: 'absolute',
            top: 6,
            left: 6,
            display: 'inline-flex',
            background: 'rgba(0, 0, 0, 0.55)',
            borderRadius: 'var(--mantine-radius-sm)',
            padding: 2,
          }}
        >
          <IconBrandYoutube size={14} color="var(--mantine-color-red-5)" />
        </span>
      )}
      <Menu position="bottom-end" withinPortal>
        <Menu.Target>
          <ActionIcon
            variant="filled"
            color="dark"
            size="sm"
            loading={isPending}
            style={{ position: 'absolute', top: 6, right: 6 }}
            aria-label="Actions"
          >
            <IconDotsVertical size={14} />
          </ActionIcon>
        </Menu.Target>
        <Menu.Dropdown>
          <Menu.Item leftSection={<IconPlayerPlay size={14} />} onClick={() => onPlay(entry.url)}>
            Play
          </Menu.Item>
          <Menu.Item leftSection={<IconPlaylistAdd size={14} />} onClick={() => onQueue(entry.url, entry.title)}>
            Add to queue
          </Menu.Item>
          <Menu.Item
            leftSection={isFavorite ? <IconHeartFilled size={14} /> : <IconHeart size={14} />}
            onClick={() => onToggleFavorite(entry.url, entry.title)}
          >
            {isFavorite ? 'Remove from favorites' : 'Add to favorites'}
          </Menu.Item>
          <Menu.Item leftSection={<IconInfoCircle size={14} />} onClick={() => onOpenDetail(entry)}>
            View track details
          </Menu.Item>
          {entry.artist && (
            <Menu.Item leftSection={<IconUser size={14} />} onClick={() => onArtistClick(entry.artist)}>
              Go to artist
            </Menu.Item>
          )}
          {entry.album && (
            <Menu.Item leftSection={<IconDisc size={14} />} onClick={() => onAlbumClick(entry.album)}>
              Go to album
            </Menu.Item>
          )}
          <Menu.Item leftSection={<IconPencil size={14} />} onClick={() => onEdit(entry)}>
            Edit
          </Menu.Item>
          <Menu.Item leftSection={<IconRefresh size={14} />} onClick={() => onRefreshArt(entry)}>
            Re-fetch art
          </Menu.Item>
          <Menu.Item color="red" leftSection={<IconTrash size={14} />} onClick={() => onDelete(entry)}>
            Delete
          </Menu.Item>
        </Menu.Dropdown>
      </Menu>

      <Stack gap={2} style={{ position: 'absolute', left: 8, right: 8, bottom: 8 }}>
        {isPlaying && (
          <Badge size="xs" color="blue" variant="filled" style={{ alignSelf: 'flex-start' }}>
            Now Playing
          </Badge>
        )}
        <Anchor
          component="button"
          type="button"
          size="sm"
          fw={600}
          c="white"
          underline="hover"
          truncate="end"
          w="100%"
          ta="left"
          onClick={() => onOpenDetail(entry)}
          title="View track details"
        >
          {entry.title || entry.url}
        </Anchor>
        {(entry.artist || entry.album) && (
          <Group gap={4} wrap="nowrap" w="100%">
            {entry.artist && (
              <Anchor
                component="button"
                type="button"
                size="xs"
                c="gray.3"
                underline="hover"
                truncate="end"
                onClick={() => onArtistClick(entry.artist)}
                title={`See tracks by ${entry.artist}`}
              >
                {entry.artist}
              </Anchor>
            )}
            {entry.artist && entry.album && (
              <Text size="xs" c="gray.5">
                —
              </Text>
            )}
            {entry.album && (
              <Anchor
                component="button"
                type="button"
                size="xs"
                c="gray.3"
                underline="hover"
                truncate="end"
                onClick={() => onAlbumClick(entry.album)}
                title={`See tracks from ${entry.album}`}
              >
                {entry.album}
              </Anchor>
            )}
          </Group>
        )}
      </Stack>
    </Card>
  )
}

export default LibraryEntryGridCard
