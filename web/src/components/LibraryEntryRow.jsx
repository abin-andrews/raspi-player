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
  IconTrash,
  IconUser,
} from '@tabler/icons-react'
import { isYouTubeUrl } from '../isYouTubeUrl.js'

// One track's list-view row — used by every track-based sub-view
// (Tracks/Albums/Artists/Favorites/search results). Play/Queue/Favorite
// stay as one-tap icons (the most common actions); everything else —
// including the "go straight to this artist/album" shortcuts, which skip
// opening the track detail page entirely — lives behind one overflow Menu
// so the row doesn't need eight icons to fit a phone width.
function LibraryEntryRow({
  entry,
  isPlaying,
  isFavorite,
  cached,
  onPlay,
  onQueue,
  onToggleFavorite,
  onEdit,
  onDelete,
  onOpenDetail,
  onArtistClick,
  onAlbumClick,
}) {
  return (
    <Card
      withBorder
      padding="sm"
      style={isPlaying ? { borderLeft: '3px solid var(--mantine-color-blue-6)' } : undefined}
    >
      <Group justify="space-between" wrap="nowrap">
        <Stack gap={0} style={{ minWidth: 0 }}>
          <Group gap="xs" wrap="nowrap">
            <Anchor
              component="button"
              type="button"
              fw={500}
              c="inherit"
              underline="hover"
              truncate="end"
              onClick={() => onOpenDetail(entry)}
              title="View track details"
            >
              {entry.title || entry.url}
            </Anchor>
            {isYouTubeUrl(entry.url) && (
              <span title="From YouTube" style={{ display: 'inline-flex', flexShrink: 0 }}>
                <IconBrandYoutube size={16} color="var(--mantine-color-red-6)" />
              </span>
            )}
            {isPlaying && (
              <Badge size="xs" color="blue" variant="light">
                Now Playing
              </Badge>
            )}
            {cached && (
              <Badge size="xs" color="teal" variant="light">
                Cached
              </Badge>
            )}
          </Group>
          {(entry.artist || entry.album) && (
            <Group gap={4} wrap="nowrap" style={{ minWidth: 0 }}>
              {entry.artist && (
                <Anchor
                  component="button"
                  type="button"
                  size="xs"
                  c="dimmed"
                  underline="hover"
                  truncate="end"
                  onClick={() => onArtistClick(entry.artist)}
                  title={`See tracks by ${entry.artist}`}
                >
                  {entry.artist}
                </Anchor>
              )}
              {entry.artist && entry.album && (
                <Text size="xs" c="dimmed">
                  —
                </Text>
              )}
              {entry.album && (
                <Anchor
                  component="button"
                  type="button"
                  size="xs"
                  c="dimmed"
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
          {!entry.artist && !entry.album && entry.title && (
            <Text size="xs" c="dimmed" truncate="end">
              {entry.url}
            </Text>
          )}
        </Stack>
        <Group gap={4} wrap="nowrap">
          <ActionIcon variant="light" onClick={() => onPlay(entry.url)} aria-label="Play">
            <IconPlayerPlay size={16} />
          </ActionIcon>
          <ActionIcon
            variant="subtle"
            onClick={() => onQueue(entry.url, entry.title)}
            aria-label="Add to queue"
          >
            <IconPlaylistAdd size={16} />
          </ActionIcon>
          <ActionIcon
            variant={isFavorite ? 'filled' : 'subtle'}
            color="red"
            onClick={() => onToggleFavorite(entry.url, entry.title)}
            aria-label={isFavorite ? 'Remove from favorites' : 'Add to favorites'}
          >
            {isFavorite ? <IconHeartFilled size={16} /> : <IconHeart size={16} />}
          </ActionIcon>
          <Menu position="bottom-end" withinPortal>
            <Menu.Target>
              <ActionIcon variant="subtle" aria-label="More actions">
                <IconDotsVertical size={16} />
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
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
              <Menu.Item color="red" leftSection={<IconTrash size={14} />} onClick={() => onDelete(entry)}>
                Delete
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>
        </Group>
      </Group>
    </Card>
  )
}

export default LibraryEntryRow
