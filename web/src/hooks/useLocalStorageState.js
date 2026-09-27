import { useState } from 'react'

// Persists a piece of per-browser UI state (a search query, a grid/list
// toggle, ...) to localStorage under `key`, so it survives a page reload —
// read once lazily on mount (useState's lazy initializer, not a useEffect,
// so there's no flash of the default value before the stored one loads),
// written on every change. Same [value, setValue] shape as useState
// (including functional updates) so it drops in as a replacement.
// localStorage access is wrapped in try/catch since it can throw (private
// browsing, disabled storage) — falls back to defaultValue and simply
// doesn't persist rather than crashing the tab.
export function useLocalStorageState(key, defaultValue) {
  const [value, setValue] = useState(() => {
    try {
      const stored = localStorage.getItem(key)
      return stored === null ? defaultValue : JSON.parse(stored)
    } catch {
      return defaultValue
    }
  })

  function setPersisted(next) {
    setValue((prev) => {
      const resolved = typeof next === 'function' ? next(prev) : next
      try {
        localStorage.setItem(key, JSON.stringify(resolved))
      } catch {
        // best-effort persistence only — ignore a storage failure
      }
      return resolved
    })
  }

  return [value, setPersisted]
}
