// Mirrors internal/ytdlp.IsYouTubeURL — a host-based check (not "does the
// URL contain youtube anywhere"), so the UI can show a YouTube badge on
// exactly the tracks the daemon itself would route through yt-dlp
// extraction. Kept in sync with the Go version's pattern intentionally;
// if that one ever grows a new recognized host, this one should too.
const YOUTUBE_HOST_PATTERN = /^(?:www\.|m\.|music\.)?youtube\.com$|^youtu\.be$/i

export function isYouTubeUrl(url) {
  if (!url) return false
  try {
    return YOUTUBE_HOST_PATTERN.test(new URL(url).hostname)
  } catch {
    return false
  }
}
