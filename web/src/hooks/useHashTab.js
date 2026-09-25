import { useEffect, useState } from 'react'

// Syncs the active tab with the URL's hash fragment (e.g. "#/queue"), so
// the current tab survives a page reload and browser back/forward work —
// without a router dependency or any server-side change: the hash is never
// sent to the server, so cmd/pi-streamer's static file serving doesn't need
// a "SPA fallback" route for this to work on a hard refresh.
export function useHashTab(validTabs, defaultTab) {
  function readHash() {
    const tab = location.hash.replace(/^#\/?/, '')
    return validTabs.includes(tab) ? tab : defaultTab
  }

  const [tab, setTabState] = useState(readHash)

  useEffect(() => {
    function onHashChange() {
      setTabState(readHash())
    }
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [])

  function setTab(next) {
    if (next === tab) return
    location.hash = `/${next}`
    setTabState(next)
  }

  return [tab, setTab]
}
