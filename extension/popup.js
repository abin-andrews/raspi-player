const statusEl = document.getElementById('status')
const manualUrlInput = document.getElementById('manualUrl')

function setStatus(text, ok) {
  statusEl.textContent = text
  statusEl.className = ok ? 'ok' : 'err'
}

async function send(url, mode) {
  if (!url) return
  setStatus(mode === 'play' ? 'Playing…' : 'Queuing…', true)
  const result = await chrome.runtime.sendMessage({ type: 'send-to-streamer', url, mode })
  if (result?.ok) {
    setStatus(mode === 'play' ? 'Now playing.' : 'Added to queue.', true)
  } else {
    setStatus(result?.error || 'Failed to reach Pi Streamer.', false)
  }
}

document.getElementById('playManual').addEventListener('click', () => send(manualUrlInput.value.trim(), 'play'))
document.getElementById('queueManual').addEventListener('click', () => send(manualUrlInput.value.trim(), 'queue'))
document.getElementById('openOptions').addEventListener('click', () => chrome.runtime.openOptionsPage())
