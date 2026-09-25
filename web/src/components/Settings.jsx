import { useEffect, useState } from 'react'
import {
  Alert,
  Badge,
  Button,
  Group,
  NumberInput,
  Progress,
  Select,
  Stack,
  Text,
  Title,
} from '@mantine/core'
import { IconRefresh } from '@tabler/icons-react'
import {
  getBucketStatus,
  getConfig,
  getOledBauds,
  getOledPorts,
  getOledStatus,
  reloadConfig,
  setConfig,
} from '../api.js'
import { formatBytes } from '../format.js'

const DEFAULT_BAUD = 115200
const MODE_DATA = [
  { value: 'stream', label: 'Stream directly' },
  { value: 'bucket', label: 'Download to bucket, then stream' },
]

// Settings talks to the general-purpose /api/config endpoints
// (internal/config) — currently the OLED display and the local audio
// bucket, but more can be added here later without a new mechanism.
// PUT /api/config replaces the whole object, not a per-section patch, so
// the single Save button below always sends every field currently in the
// form (both sections), not just whichever one the user was looking at.
//
// Port/baud/mode dropdowns are populated from the backend rather than free
// text — the backend rejects anything else anyway (internal/api's
// validateOLED/validateBucket), so offering only valid choices avoids a
// round-trip just to find out a typed value was rejected.
function Settings() {
  const [port, setPort] = useState('')
  const [baud, setBaud] = useState(String(DEFAULT_BAUD))
  const [ports, setPorts] = useState([])
  const [bauds, setBauds] = useState([])
  const [oledStatus, setOledStatus] = useState(null)
  const [loadingPorts, setLoadingPorts] = useState(false)

  const [mode, setMode] = useState('stream')
  const [maxSizeMb, setMaxSizeMb] = useState('')
  const [favoritesMaxSizeMb, setFavoritesMaxSizeMb] = useState('')
  const [minFreeMb, setMinFreeMb] = useState('')
  const [bucketStatus, setBucketStatus] = useState(null)

  const [saving, setSaving] = useState(false)
  const [reloading, setReloading] = useState(false)
  const [error, setError] = useState(null)

  async function refreshOledStatus() {
    try {
      setOledStatus(await getOledStatus())
    } catch (err) {
      setError(err.message)
    }
  }

  async function refreshPorts() {
    setLoadingPorts(true)
    try {
      setPorts((await getOledPorts()) ?? [])
    } catch (err) {
      setError(err.message)
    } finally {
      setLoadingPorts(false)
    }
  }

  async function refreshBucketStatus() {
    try {
      setBucketStatus(await getBucketStatus())
    } catch (err) {
      setError(err.message)
    }
  }

  function applyConfig(cfg) {
    setPort(cfg?.oled?.port ?? '')
    setBaud(String(cfg?.oled?.baud || DEFAULT_BAUD))
    setMode(cfg?.bucket?.mode || 'stream')
    setMaxSizeMb(cfg?.bucket?.maxSizeMb ? String(cfg.bucket.maxSizeMb) : '')
    setFavoritesMaxSizeMb(cfg?.bucket?.favoritesMaxSizeMb ? String(cfg.bucket.favoritesMaxSizeMb) : '')
    setMinFreeMb(cfg?.bucket?.minFreeMb ? String(cfg.bucket.minFreeMb) : '')
  }

  useEffect(() => {
    async function load() {
      try {
        applyConfig(await getConfig())
      } catch (err) {
        setError(err.message)
      }
    }
    load()
    refreshPorts()
    refreshOledStatus()
    refreshBucketStatus()
    getOledBauds()
      .then((b) => setBauds(b ?? []))
      .catch((err) => setError(err.message))
  }, [])

  async function handleSave() {
    setError(null)
    setSaving(true)
    try {
      await setConfig({
        oled: { port, baud: Number(baud) },
        bucket: {
          mode,
          maxSizeMb: Number(maxSizeMb) || 0,
          favoritesMaxSizeMb: Number(favoritesMaxSizeMb) || 0,
          minFreeMb: Number(minFreeMb) || 0,
        },
      })
      await Promise.all([refreshOledStatus(), refreshBucketStatus()])
    } catch (err) {
      setError(err.message)
    } finally {
      setSaving(false)
    }
  }

  async function handleReload() {
    setError(null)
    setReloading(true)
    try {
      applyConfig(await reloadConfig())
      await Promise.all([refreshOledStatus(), refreshBucketStatus()])
    } catch (err) {
      setError(err.message)
    } finally {
      setReloading(false)
    }
  }

  const portData = [
    { value: '', label: 'Disabled (no display)' },
    ...ports.map((p) => ({ value: p, label: p })),
  ]
  const baudData = bauds.map((b) => ({ value: String(b), label: String(b) }))

  return (
    <Stack>
      {error && (
        <Alert color="red" title="Error" withCloseButton onClose={() => setError(null)}>
          {error}
        </Alert>
      )}

      <Title order={5}>OLED Display</Title>
      <Text size="sm" c="dimmed">
        Drives the Arduino display over USB serial (see arduino/control.ino). Connects
        immediately on Save — no restart needed.
      </Text>

      <Group align="flex-end">
        <Select
          label="Serial port"
          placeholder={loadingPorts ? 'Scanning…' : 'Select a port'}
          data={portData}
          value={port}
          onChange={(value) => setPort(value ?? '')}
          style={{ flex: 1 }}
        />
        <Button
          variant="light"
          leftSection={<IconRefresh size={14} />}
          onClick={refreshPorts}
          loading={loadingPorts}
        >
          Refresh ports
        </Button>
      </Group>

      <Select
        label="Baud rate"
        data={baudData}
        value={baud}
        onChange={(value) => setBaud(value ?? String(DEFAULT_BAUD))}
        style={{ maxWidth: 200 }}
      />

      {oledStatus && (
        <Group gap="xs">
          <Badge color={oledStatus.connected ? 'green' : 'gray'}>
            {oledStatus.connected ? 'Connected' : 'Disconnected'}
          </Badge>
          {oledStatus.port && (
            <Text size="sm" c="dimmed">
              {oledStatus.port} @ {oledStatus.baud} baud
            </Text>
          )}
          {oledStatus.error && (
            <Text size="sm" c="red">
              {oledStatus.error}
            </Text>
          )}
        </Group>
      )}

      <Title order={5} mt="md">
        Audio Bucket
      </Title>
      <Text size="sm" c="dimmed">
        "Stream directly" plays a URL as-is (after a quick reachability check). "Download to
        bucket" downloads it locally first, then hands the local file to mpd — more robust
        against a flaky remote server mid-playback. The bucket cache evicts your
        least-recently-played tracks once it hits its size limit; favorited tracks are saved
        separately and permanently instead (never auto-deleted), up to their own limit. A
        safety margin always keeps some space free on the SD card regardless of either limit.
      </Text>

      <Select
        label="Playback mode"
        data={MODE_DATA}
        value={mode}
        onChange={(value) => setMode(value ?? 'stream')}
        style={{ maxWidth: 320 }}
      />

      <Group grow>
        <NumberInput
          label="Bucket cache limit (MB)"
          description="Evictable — oldest-played tracks removed to make room"
          placeholder="512"
          min={0}
          value={maxSizeMb}
          onChange={(v) => setMaxSizeMb(v === '' ? '' : String(v))}
        />
        <NumberInput
          label="Favorites storage limit (MB)"
          description="Permanent — full means new favorites can't be saved, not that old ones get deleted"
          placeholder="1024"
          min={0}
          value={favoritesMaxSizeMb}
          onChange={(v) => setFavoritesMaxSizeMb(v === '' ? '' : String(v))}
        />
        <NumberInput
          label="Safety margin (MB)"
          description="Minimum free disk space either store will always leave"
          placeholder="512"
          min={0}
          value={minFreeMb}
          onChange={(v) => setMinFreeMb(v === '' ? '' : String(v))}
        />
      </Group>

      {bucketStatus && (
        <Stack gap={4}>
          <Group justify="space-between">
            <Text size="sm">Bucket cache</Text>
            <Text size="sm" c="dimmed">
              {formatBytes(bucketStatus.usedBytes)} / {formatBytes(bucketStatus.maxBytes)}
            </Text>
          </Group>
          <Progress
            value={bucketStatus.maxBytes ? (100 * bucketStatus.usedBytes) / bucketStatus.maxBytes : 0}
            size="sm"
          />
          <Group justify="space-between" mt="xs">
            <Text size="sm">Favorites archive</Text>
            <Text size="sm" c="dimmed">
              {formatBytes(bucketStatus.favoritesUsedBytes)} / {formatBytes(bucketStatus.favoritesMaxBytes)}
            </Text>
          </Group>
          <Progress
            value={
              bucketStatus.favoritesMaxBytes
                ? (100 * bucketStatus.favoritesUsedBytes) / bucketStatus.favoritesMaxBytes
                : 0
            }
            size="sm"
            color="grape"
          />
          <Text size="xs" c="dimmed" mt="xs">
            Disk free: {formatBytes(bucketStatus.diskFreeBytes)} (margin:{' '}
            {formatBytes(bucketStatus.minFreeBytes)})
          </Text>
        </Stack>
      )}

      <Group mt="md">
        <Button onClick={handleSave} loading={saving}>
          Save Settings
        </Button>
        <Button variant="light" onClick={handleReload} loading={reloading}>
          Reload from file
        </Button>
      </Group>
    </Stack>
  )
}

export default Settings
