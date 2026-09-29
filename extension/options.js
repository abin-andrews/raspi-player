const baseUrlInput = document.getElementById('baseUrl')
const statusEl = document.getElementById('status')

function setStatus(text, ok) {
  statusEl.textContent = text
  statusEl.className = ok ? 'ok' : 'err'
}

function normalize(value) {
  return value.trim().replace(/\/+$/, '')
}

chrome.storage.sync.get('baseUrl').then(({ baseUrl }) => {
  if (baseUrl) baseUrlInput.value = baseUrl
})

document.getElementById('save').addEventListener('click', async () => {
  const baseUrl = normalize(baseUrlInput.value)
  if (!baseUrl) {
    setStatus('Enter an address first.', false)
    return
  }
  await chrome.storage.sync.set({ baseUrl })
  setStatus('Saved.', true)
})

document.getElementById('discover').addEventListener('click', async () => {
  setStatus('Searching your network…', true)
  const found = await discoverStreamer()
  if (found) {
    baseUrlInput.value = found
    await chrome.storage.sync.set({ baseUrl: found })
    setStatus(`Found and saved: ${found}`, true)
  } else {
    setStatus('Nothing found on this network. Enter the address manually.', false)
  }
})

document.getElementById('test').addEventListener('click', async () => {
  const baseUrl = normalize(baseUrlInput.value)
  if (!baseUrl) {
    setStatus('Enter an address first.', false)
    return
  }
  setStatus('Checking…', true)
  try {
    // GET /api/discover positively identifies pi-streamer specifically
    // (not just "something answered"), and doesn't depend on mpd being
    // connected the way most other endpoints do — see
    // internal/api/handlers.go's discoveryResponse doc comment.
    const resp = await fetch(baseUrl + '/api/discover')
    if (!resp.ok) throw new Error(`daemon returned ${resp.status}`)
    const data = await resp.json()
    if (data?.service !== 'pi-streamer') throw new Error('responded, but not as pi-streamer')
    setStatus('Connected — Pi Streamer responded.', true)
  } catch (err) {
    setStatus(`Could not reach Pi Streamer: ${err.message || err}`, false)
  }
})
