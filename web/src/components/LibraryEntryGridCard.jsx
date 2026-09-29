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

const ART_SIZE = 120

// One track's grid-view tile — album art plus a single overflow menu for
// actions, rather than five icons cluttering a small card.
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
    <Card withBorder padding="xs" style={isPlaying ? { borderColor: 'var(--mantine-color-blue-6)' } : undefined}>
      <Stack gap={4} align="center">
        <div style={{ position: 'relative' }}>
          <TrackArt
            url={entry.url}
            label={entry.album || entry.title || entry.url}
            artist={entry.artist}
            album={entry.album}
            knownHasArt={artStatus?.hasArt}
            resolvedPath={artStatus?.path}
            size={ART_SIZE}
          />
          {isYouTubeUrl(entry.url) && (
            <span
              title="From YouTube"
              style={{
                position: 'absolute',
                top: 4,
                left: 4,
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
                style={{ position: 'absolute', top: 4, right: 4 }}
                aria-label="Actions"
              >
                <IconDotsVertical size={14} />
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Item leftSection={<IconPlayerPlay size={14} />} onClick={() => onPlay(entry.url)}>
                Play
              </Menu.Item>
              <Menu.Item
                leftSection={<IconPlaylistAdd size={14} />}
                onClick={() => onQueue(entry.url, entry.title)}
              >
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
              <Menu.Item
                color="red"
                leftSection={<IconTrash size={14} />}
                onClick={() => onDelete(entry)}
              >
                Delete
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>
        </div>
        <Anchor
          component="button"
          type="button"
          size="sm"
          fw={500}
          c="inherit"
          underline="hover"
          truncate="end"
          w="100%"
          ta="center"
          onClick={() => onOpenDetail(entry)}
          title="View track details"
        >
          {entry.title || entry.url}
        </Anchor>
        {(entry.artist || entry.album) && (
          <Group gap={4} wrap="nowrap" justify="center" w="100%">
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
        {isPlaying && (
          <Badge size="xs" color="blue" variant="light">
            Now Playing
          </Badge>
        )}
      </Stack>
    </Card>
  )
}

export default LibraryEntryGridCard
