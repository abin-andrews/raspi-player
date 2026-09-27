// Package ytdlp detects YouTube video URLs and extracts their audio-only
// stream to a local file via the yt-dlp CLI tool
// (https://github.com/yt-dlp/yt-dlp, expected to already be installed and
// on PATH — this package only ever shells out to it, never vendors or
// reimplements any of its extraction logic). mpd has no way to understand
// a youtube.com URL on its own, so this is what makes "paste a YouTube
// link, hear the audio" work at all — the extracted file is handed to
// internal/bucket.Store.DownloadVia the same way any other "content the
// daemon fetches on mpd's behalf" already is (see
// cmd/pi-streamer/bucket.go's modeResolver).
package ytdlp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// youtubeHostPattern matches the hostnames an ordinary YouTube video link
// uses — youtu.be short links, youtube.com itself (with or without a www/
// m/music subdomain). Deliberately narrow and host-based (not "does the
// URL contain youtube.com anywhere"): this package only exists to route
// *recognized* YouTube video links through yt-dlp, not to guess at every
// site yt-dlp's hundreds of other extractors happen to support.
var youtubeHostPattern = regexp.MustCompile(`(?i)^(?:www\.|m\.|music\.)?youtube\.com$|^youtu\.be$`)

// IsYouTubeURL reports whether rawURL's host looks like a YouTube video
// URL. An unparseable URL is simply not one (false, not an error) — the
// caller's own normalization/reachability checks handle a genuinely
// malformed URL already.
func IsYouTubeURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return youtubeHostPattern.MatchString(u.Hostname())
}

// videoIDPattern matches a YouTube video ID: always exactly 11 characters
// from its URL-safe alphabet (letters, digits, "-", "_").
var videoIDPattern = regexp.MustCompile(`^[\w-]{11}$`)

// VideoID extracts the 11-character video ID from any commonly used
// YouTube URL shape: an ordinary watch link (?v=<id>, on youtube.com,
// m.youtube.com, or music.youtube.com), a share/short link
// (youtu.be/<id>), an embed link (/embed/<id>), a Shorts link
// (/shorts/<id>), or a livestream link (/live/<id>) — with or without
// extra query parameters (a share link's "?si=..." tracking param, a
// timestamp's "&t=30s", a playlist's "&list=...", etc.), which are simply
// ignored rather than tripping up extraction. Returns ("", false) for a
// URL IsYouTubeURL itself would still accept but that doesn't identify a
// single video at all (a bare channel or playlist-only URL).
func VideoID(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || !youtubeHostPattern.MatchString(u.Hostname()) {
		return "", false
	}

	if strings.EqualFold(u.Hostname(), "youtu.be") {
		id := strings.Trim(u.Path, "/")
		if videoIDPattern.MatchString(id) {
			return id, true
		}
		return "", false
	}

	if id := u.Query().Get("v"); videoIDPattern.MatchString(id) {
		return id, true
	}

	for _, prefix := range []string{"/embed/", "/shorts/", "/live/"} {
		rest, ok := strings.CutPrefix(u.Path, prefix)
		if !ok {
			continue
		}
		id, _, _ := strings.Cut(rest, "/")
		if videoIDPattern.MatchString(id) {
			return id, true
		}
	}
	return "", false
}

// ThumbnailURL returns the URL of youtubeURL's highest-resolution video
// thumbnail, or ("", false) if no video ID could be extracted from it.
// maxresdefault.jpg isn't generated for every video (older or
// lower-resolution uploads, in particular) — a caller should fall back to
// HQThumbnailURL if fetching this one 404s.
func ThumbnailURL(youtubeURL string) (string, bool) {
	id, ok := VideoID(youtubeURL)
	if !ok {
		return "", false
	}
	return "https://i.ytimg.com/vi/" + id + "/maxresdefault.jpg", true
}

// HQThumbnailURL returns the URL of youtubeURL's "high quality" (480x360)
// thumbnail — the fallback for ThumbnailURL's maxresdefault.jpg, which
// YouTube generates for every video without exception, unlike the
// higher-resolution one.
func HQThumbnailURL(youtubeURL string) (string, bool) {
	id, ok := VideoID(youtubeURL)
	if !ok {
		return "", false
	}
	return "https://i.ytimg.com/vi/" + id + "/hqdefault.jpg", true
}

// ThumbnailFetcher fetches a YouTube video's thumbnail image directly
// from YouTube's own static image host — a plain HTTP GET, no yt-dlp
// subprocess or API call needed at all, since thumbnail URLs are fully
// predictable from the video ID alone (see ThumbnailURL/HQThumbnailURL).
type ThumbnailFetcher struct {
	httpClient *http.Client
	// baseURL overrides https://i.ytimg.com — for tests only; leave
	// zero-valued in production.
	baseURL string
}

func (f *ThumbnailFetcher) client() *http.Client {
	if f.httpClient != nil {
		return f.httpClient
	}
	return http.DefaultClient
}

func (f *ThumbnailFetcher) base() string {
	if f.baseURL != "" {
		return f.baseURL
	}
	return "https://i.ytimg.com"
}

// Fetch returns youtubeURL's thumbnail image bytes, trying the
// highest-resolution one first (maxresdefault.jpg) and falling back to
// the guaranteed-to-exist "hq" size (hqdefault.jpg, 480x360) if that one
// isn't available at all — not every video has a maxres thumbnail
// generated (older or lower-resolution uploads, in particular), and any
// failure fetching it (a 404, or a genuine network error) is treated the
// same way: fall through to the hq attempt rather than giving up.
// Returns (nil, nil), not an error, if youtubeURL doesn't identify a
// single video at all (VideoID found nothing to build a thumbnail URL
// from) — the same "confirmed no art" outcome Resolve/Refresh already
// model for mpd's own AlbumArt lookup, not a failure worth surfacing.
func (f *ThumbnailFetcher) Fetch(ctx context.Context, youtubeURL string) ([]byte, error) {
	id, ok := VideoID(youtubeURL)
	if !ok {
		return nil, nil
	}
	if data, err := f.fetchImage(ctx, id, "maxresdefault"); err == nil && len(data) > 0 {
		return data, nil
	}
	return f.fetchImage(ctx, id, "hqdefault")
}

func (f *ThumbnailFetcher) fetchImage(ctx context.Context, videoID, size string) ([]byte, error) {
	imgURL := f.base() + "/vi/" + videoID + "/" + size + ".jpg"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
	if err != nil {
		return nil, fmt.Errorf("ytdlp: build thumbnail request: %w", err)
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("ytdlp: thumbnail request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // this size isn't generated for this video — not an error
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("ytdlp: thumbnail fetch: status %d: %s", resp.StatusCode, string(body))
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ytdlp: read thumbnail response: %w", err)
	}
	return data, nil
}

// TitleFetcher fetches a YouTube video's title via YouTube's own oEmbed
// endpoint — a single lightweight, unauthenticated HTTP GET returning a
// small JSON document, not a yt-dlp subprocess call: a title alone
// doesn't need yt-dlp's much heavier extraction machinery (resolving
// formats, downloading anything).
type TitleFetcher struct {
	httpClient *http.Client
	// baseURL overrides the real oEmbed endpoint — for tests only; leave
	// zero-valued in production.
	baseURL string
}

func (f *TitleFetcher) client() *http.Client {
	if f.httpClient != nil {
		return f.httpClient
	}
	return http.DefaultClient
}

func (f *TitleFetcher) endpoint() string {
	if f.baseURL != "" {
		return f.baseURL
	}
	return "https://www.youtube.com/oembed"
}

// FetchTitle returns youtubeURL's video title, or ("", false) if the
// lookup fails for any reason (a private/deleted/age-restricted video, a
// network error, an unexpected response) — best-effort, the same
// "missing metadata isn't an error" stance internal/metadata.Fetcher
// already takes for ordinary tracks.
func (f *TitleFetcher) FetchTitle(ctx context.Context, youtubeURL string) (string, bool) {
	values := url.Values{}
	values.Set("url", youtubeURL)
	values.Set("format", "json")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.endpoint()+"?"+values.Encode(), nil)
	if err != nil {
		return "", false
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}

	var result struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&result); err != nil {
		return "", false
	}
	if result.Title == "" {
		return "", false
	}
	return result.Title, true
}

// Extractor runs the yt-dlp CLI as a subprocess to pull down one video's
// audio-only stream.
type Extractor struct {
	// BinaryPath is the yt-dlp executable to run — defaults to "yt-dlp"
	// resolved via PATH if empty.
	BinaryPath string
	// Format is yt-dlp's own -f format selector — defaults to "bestaudio"
	// if empty. mpd/ffmpeg can decode whatever container yt-dlp picks for
	// that (opus/webm, m4a, etc.), so there's no need to pin down anything
	// more specific than "audio only, best available".
	Format string
}

func (e *Extractor) binary() string {
	if e.BinaryPath != "" {
		return e.BinaryPath
	}
	return "yt-dlp"
}

func (e *Extractor) format() string {
	if e.Format != "" {
		return e.Format
	}
	return "bestaudio"
}

// ExtractAudio downloads youtubeURL's best available audio-only stream
// into a new file inside dir and returns that file's path. dir is the
// exact directory to create the file in — callers pass internal/
// bucket.Store.DownloadVia's own fetchDir, since the rename it does
// afterward needs the file to already be on the same filesystem.
//
// --embed-metadata has yt-dlp tag the file with the video's own title
// (via ffmpeg, as part of downloading it) so mpd's ordinary tag-reading
// picks up a real title with no extra plumbing needed on this daemon's
// side — the same title-then-URL-fallback path every other track already
// goes through (internal/player/title.go's deriveTitleFromURL). If
// embedding fails for some reason (e.g. ffmpeg isn't installed), yt-dlp
// itself only warns rather than failing the download — the audio still
// downloads and plays fine, just without a title until the URL-derived
// fallback kicks in, same as any other untitled track.
func (e *Extractor) ExtractAudio(ctx context.Context, youtubeURL, dir string) (string, error) {
	base := filepath.Join(dir, fmt.Sprintf("ytdlp-%d", time.Now().UnixNano()))
	outputTemplate := base + ".%(ext)s"

	cmd := exec.CommandContext(ctx, e.binary(),
		"-f", e.format(),
		"--no-playlist",
		"--no-progress",
		"--embed-metadata",
		"-o", outputTemplate,
		youtubeURL,
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ytdlp: extract %q: %w: %s", youtubeURL, err, strings.TrimSpace(stderr.String()))
	}

	matches, err := filepath.Glob(base + ".*")
	if err != nil {
		return "", fmt.Errorf("ytdlp: glob output for %q: %w", youtubeURL, err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("ytdlp: no output file produced for %q", youtubeURL)
	}
	return matches[0], nil
}
