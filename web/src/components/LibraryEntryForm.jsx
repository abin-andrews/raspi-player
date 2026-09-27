import { useState } from 'react'
import { Alert, Button, Card, Group, Stack, Text, TextInput } from '@mantine/core'
import { IconDeviceFloppy, IconSearch } from '@tabler/icons-react'
import { suggestMetadata } from '../api.js'

// Best-effort fallback search query for "Look up" when the form has
// nothing typed yet: the URL's last path segment, percent-decoded and
// with separators turned into spaces — same idea as
// internal/player/title.go's deriveTitleFromURL on the backend, just good
// enough for a search query rather than a display title.
function deriveQueryFromURL(url) {
  try {
    const path = new URL(url).pathname
    const last = path.split('/').filter(Boolean).pop() || ''
    const withoutExt = decodeURIComponent(last).replace(/\.[a-zA-Z0-9]+$/, '')
    return withoutExt.replace(/[-_]+/g, ' ').trim()
  } catch {
    return ''
  }
}

// The edit-metadata form for an existing library entry, pre-filled from
// whatever's already in hand from the loaded list (no extra fetch needed).
// The URL is fixed — editing is keyed on it, so changing it would just
// create a second, unrelated entry rather than renaming this one.
//
// "Look up" queries the same MusicBrainz-backed lookup the album art
// fallback already uses (internal/coverart.Fetcher.SearchMetadata, via
// POST /api/albumart/suggest) for candidate Title/Artist/Album matches,
// shown as clickable suggestions here — never applied automatically, since
// a free-text match is a best guess, not a confirmed correction.
function LibraryEntryForm({ values, onChange, onSubmit, onCancel, submitting }) {
  function set(field) {
    return (e) => onChange({ ...values, [field]: e.currentTarget.value })
  }

  const [suggestions, setSuggestions] = useState(null)
  const [suggesting, setSuggesting] = useState(false)
  const [suggestError, setSuggestError] = useState(null)

  async function handleSuggest() {
    const query =
      [values.title, values.artist, values.album].filter(Boolean).join(' ').trim() ||
      deriveQueryFromURL(values.url)
    if (!query) return
    setSuggesting(true)
    setSuggestError(null)
    try {
      const result = await suggestMetadata(query)
      setSuggestions(result?.suggestions ?? [])
    } catch (err) {
      setSuggestError(err.message)
    } finally {
      setSuggesting(false)
    }
  }

  function applySuggestion(s) {
    onChange({
      ...values,
      title: s.title || values.title,
      artist: s.artist || values.artist,
      album: s.album || values.album,
    })
    setSuggestions(null)
  }

  return (
    <Stack gap="xs">
      <TextInput
        label="URL"
        value={values.url}
        onChange={set('url')}
        disabled
        description="Editing is keyed on the URL — remove and re-add to change it"
      />
      <Group grow>
        <TextInput label="Title" value={values.title} onChange={set('title')} />
        <TextInput label="Artist" value={values.artist} onChange={set('artist')} />
      </Group>
      <Group grow>
        <TextInput label="Album" value={values.album} onChange={set('album')} />
        <TextInput label="Tags" placeholder="comma, separated" value={values.tags} onChange={set('tags')} />
      </Group>

      <Button
        variant="light"
        size="xs"
        onClick={handleSuggest}
        loading={suggesting}
        leftSection={<IconSearch size={14} />}
        style={{ alignSelf: 'flex-start' }}
      >
        Look up title/artist/album
      </Button>

      {suggestError && (
        <Alert color="red" title="Lookup failed" withCloseButton onClose={() => setSuggestError(null)}>
          {suggestError}
        </Alert>
      )}

      {suggestions !== null &&
        (suggestions.length === 0 ? (
          <Text size="xs" c="dimmed">
            No matches found.
          </Text>
        ) : (
          <Stack gap={4}>
            <Text size="xs" c="dimmed">
              Pick a match to fill in the fields above:
            </Text>
            {suggestions.map((s, i) => (
              <Card
                key={i}
                withBorder
                padding="xs"
                onClick={() => applySuggestion(s)}
                style={{ cursor: 'pointer' }}
              >
                <Text size="sm" fw={500} truncate="end">
                  {s.title || '(no title)'}
                </Text>
                <Text size="xs" c="dimmed" truncate="end">
                  {[s.artist, s.album].filter(Boolean).join(' — ') || 'No artist/album'}
                </Text>
              </Card>
            ))}
          </Stack>
        ))}

      <Group>
        <Button
          onClick={onSubmit}
          loading={submitting}
          disabled={!values.url.trim()}
          leftSection={<IconDeviceFloppy size={16} />}
        >
          Save changes
        </Button>
        <Button variant="subtle" onClick={onCancel} disabled={submitting}>
          Cancel
        </Button>
      </Group>
    </Stack>
  )
}

export default LibraryEntryForm
