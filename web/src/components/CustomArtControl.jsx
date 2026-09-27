import { useState } from 'react'
import { ActionIcon, Group, Text, TextInput, Tooltip } from '@mantine/core'
import { IconPhotoUp, IconPhotoX } from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { clearCustomArt, setCustomArt } from '../api.js'

// A URL input to set (or clear) a custom album art fallback for scope
// ("track"/"album"/"artist") — shared by the track/album/artist detail
// pages in Library.jsx rather than duplicated three times, since the
// interaction (paste an image URL, save; or clear back to
// auto-resolution) is identical for all three. See the album art
// architecture notes for how a custom entry interacts with
// auto-resolution afterward — a track's own custom art always wins, an
// album/artist one is only ever used as a fallback for a track with none
// of its own.
function CustomArtControl({ scope, artKey, label, onChanged }) {
  const [url, setUrl] = useState('')
  const [saving, setSaving] = useState(false)
  const [clearing, setClearing] = useState(false)

  async function handleSet() {
    const trimmed = url.trim()
    if (!trimmed) return
    setSaving(true)
    try {
      await setCustomArt(scope, artKey, trimmed)
      notifications.show({ color: 'green', title: 'Custom art set', message: label })
      setUrl('')
      onChanged?.()
    } catch (err) {
      notifications.show({ color: 'red', title: 'Could not set custom art', message: err.message })
    } finally {
      setSaving(false)
    }
  }

  async function handleClear() {
    setClearing(true)
    try {
      await clearCustomArt(scope, artKey)
      notifications.show({ color: 'green', title: 'Custom art cleared', message: label })
      onChanged?.()
    } catch (err) {
      notifications.show({ color: 'red', title: 'Could not clear custom art', message: err.message })
    } finally {
      setClearing(false)
    }
  }

  return (
    <Group gap={4} wrap="nowrap" align="flex-end">
      <TextInput
        size="xs"
        label={
          <Text size="xs" c="dimmed">
            Custom art URL for {label}
          </Text>
        }
        placeholder="https://…/cover.jpg"
        value={url}
        onChange={(e) => setUrl(e.currentTarget.value)}
        onKeyDown={(e) => e.key === 'Enter' && handleSet()}
        style={{ flex: 1 }}
      />
      <Tooltip label="Set custom art">
        <ActionIcon
          variant="light"
          size="md"
          loading={saving}
          disabled={!url.trim()}
          onClick={handleSet}
          aria-label={`Set custom art for ${label}`}
        >
          <IconPhotoUp size={16} />
        </ActionIcon>
      </Tooltip>
      <Tooltip label="Clear custom art">
        <ActionIcon
          variant="subtle"
          color="red"
          size="md"
          loading={clearing}
          onClick={handleClear}
          aria-label={`Clear custom art for ${label}`}
        >
          <IconPhotoX size={16} />
        </ActionIcon>
      </Tooltip>
    </Group>
  )
}

export default CustomArtControl
