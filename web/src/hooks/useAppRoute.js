import { useEffect, useState } from 'react'

// A single hash-based route drives the whole app's navigable state — the
// active tab, whether Settings or the full-screen Now Playing view is
// open on top of it, and (for Settings) which sidebar section — so a
// reload or browser back/forward restores exactly what was on screen
// instead of always landing back on the Library tab with everything else
// closed. Deliberately still hash-based, not a router library or real
// paths (same reasoning as the old per-tab useHashTab this replaces): a
// hash fragment is never sent to the server, so cmd/pi-streamer's static
// file serving needs no "SPA fallback" route for a hard refresh on any of
// these to keep working.
//
// Hash shape: "#/<tab>" or "#/<tab>/settings[/<section>]" or
// "#/<tab>/now-playing" — the tab is always the first segment (even while
// an overlay is open on top of it) specifically so closing the overlay
// knows which tab to land back on without needing separate, unsynced
// state for that.
const TABS = ['library', 'queue']
const SECTIONS = ['general', 'oled', 'cache']
const DEFAULT_SECTION = 'general'

function parseHash() {
  const parts = location.hash.replace(/^#\/?/, '').split('/').filter(Boolean)
  const tab = TABS.includes(parts[0]) ? parts[0] : 'library'
  if (parts[1] === 'settings') {
    return { tab, overlay: 'settings', section: SECTIONS.includes(parts[2]) ? parts[2] : DEFAULT_SECTION }
  }
  if (parts[1] === 'now-playing') {
    return { tab, overlay: 'now-playing', section: null }
  }
  return { tab, overlay: null, section: null }
}

function buildHash({ tab, overlay, section }) {
  if (overlay === 'settings') return `/${tab}/settings/${section || DEFAULT_SECTION}`
  if (overlay === 'now-playing') return `/${tab}/now-playing`
  return `/${tab}`
}

export function useAppRoute() {
  const [route, setRoute] = useState(parseHash)

  useEffect(() => {
    function onHashChange() {
      setRoute(parseHash())
    }
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [])

  // Every navigation both assigns location.hash (so it's reflected in the
  // URL for reload/back-forward/sharing) and updates state directly
  // (rather than waiting on the hashchange event) — same belt-and-
  // suspenders approach the old useHashTab used, so the UI updates
  // immediately rather than depending on the browser's event timing.
  function navigate(next) {
    const merged = { ...route, ...next }
    location.hash = buildHash(merged)
    setRoute(merged)
  }

  return {
    tab: route.tab,
    overlay: route.overlay,
    section: route.section,
    setTab: (tab) => navigate({ tab, overlay: null, section: null }),
    openSettings: (section) => navigate({ overlay: 'settings', section: section || route.section || DEFAULT_SECTION }),
    setSettingsSection: (section) => navigate({ overlay: 'settings', section }),
    openNowPlaying: () => navigate({ overlay: 'now-playing', section: null }),
    closeOverlay: () => navigate({ overlay: null, section: null }),
  }
}
