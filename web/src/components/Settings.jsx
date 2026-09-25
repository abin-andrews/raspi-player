import { useEffect, useState } from 'react'
import { Alert, Badge, Button, Group, Select, Stack, Text, Title } from '@mantine/core'
import { IconRefresh } from '@tabler/icons-react'
import { getConfig, getOledBauds, getOledPorts, getOledStatus, reloadConfig, setConfig } from '../api.js'

const DEFAULT_BAUD = 115200

// Settings currently only covers the OLED display, but talks to the
// general-purpose /api/config endpoints (internal/config) so more settings
// can be added here later without a new mechanism.
//
// Both dropdowns (port, baud) are populated from the backend rather than
// free text — the backend rejects anything else anyway (internal/api's
// validateOLED), so offering only valid choices avoids a round-trip just to
// find out a typed value was rejected.
function Settings() {
  const [port, setPort] = useState('')
  const [baud, setBaud] = useState(String(DEFAULT_BAUD))
  const [ports, setPorts] = useState([])
  const [bauds, setBauds] = useState([])
  const [status, setStatus] = useState(null)
  const [loadingPorts, setLoadingPorts] = useState(false)
  const [saving, setSaving] = useState(false)
  const [reloading, setReloading] = useState(false)
  const [error, setError] = useState(null)

  async function refreshStatus() {
    try {
      setStatus(await getOledStatus())
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

  function applyConfig(cfg) {
    setPort(cfg?.oled?.port ?? '')
    setBaud(String(cfg?.oled?.baud || DEFAULT_BAUD))
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
    refreshStatus()
    getOledBauds()
      .then((b) => setBauds(b ?? []))
      .catch((err) => setError(err.message))
  }, [])

  async function handleSave() {
    setError(null)
    setSaving(true)
    try {
      await setConfig({ oled: { port, baud: Number(baud) } })
      await refreshStatus()
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
      await refreshStatus()
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
        Drives the Arduino display over USB serial (see arduino/control.ino). Saved to the
        daemon's config.json and connects immediately — no restart needed.
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

      <Group>
        <Button onClick={handleSave} loading={saving}>
          Save &amp; Connect
        </Button>
        <Button variant="light" onClick={handleReload} loading={reloading}>
          Reload from file
        </Button>
      </Group>

      {status && (
        <Group gap="xs">
          <Badge color={status.connected ? 'green' : 'gray'}>
            {status.connected ? 'Connected' : 'Disconnected'}
          </Badge>
          {status.port && (
            <Text size="sm" c="dimmed">
              {status.port} @ {status.baud} baud
            </Text>
          )}
          {status.error && (
            <Text size="sm" c="red">
              {status.error}
            </Text>
          )}
        </Group>
      )}
    </Stack>
  )
}

export default Settings
