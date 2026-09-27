import { Button, Group, Stack, TextInput } from '@mantine/core'
import { IconDeviceFloppy } from '@tabler/icons-react'

// The edit-metadata form for an existing library entry, pre-filled from
// whatever's already in hand from the loaded list (no extra fetch needed).
// The URL is fixed — editing is keyed on it, so changing it would just
// create a second, unrelated entry rather than renaming this one.
function LibraryEntryForm({ values, onChange, onSubmit, onCancel, submitting }) {
  function set(field) {
    return (e) => onChange({ ...values, [field]: e.currentTarget.value })
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
