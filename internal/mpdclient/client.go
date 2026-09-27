// Package mpdclient is the only package in pi-streamer allowed to talk to
// mpd directly. It defines the Client interface used by the rest of the
// daemon (e.g. internal/player) to control playback, plus a real
// implementation backed by gompd and an in-memory fake for tests.
package mpdclient

import (
	"errors"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/fhs/gompd/v2/mpd"
)

// Status is a minimal snapshot of mpd's playback status.
type Status struct {
	// State is the player state, e.g. "play", "pause", or "stop".
	State string `json:"state"`
	// Song is the URI of the current song — always the URI, never a
	// display title (Title, below, is the separate field for that). Empty
	// if nothing is loaded. Callers that key off a track's identity (album
	// art lookup by URL, matching a queue/library entry to "now playing")
	// depend on this being the actual URI unconditionally.
	Song string `json:"song"`
	// Artist is the current song's artist, if known.
	Artist string `json:"artist"`
	// Album is the current song's album, if known.
	Album string `json:"album"`
	// Title is the current song's title, if known.
	Title string `json:"title"`
	// Elapsed is the number of seconds played into the current song.
	Elapsed float64 `json:"elapsed"`
	// Duration is the total length of the current song in seconds.
	Duration float64 `json:"duration"`
	// Volume is the current output volume, 0-100.
	Volume int `json:"volume"`
	// SongID is the queue ID of the current song, matching QueueTrack.ID.
	// 0 if nothing is loaded.
	SongID int `json:"songId"`
}

// QueueTrack is a single entry in mpd's live playback queue, distinct from
// the app's own saved named playlists in internal/store.
type QueueTrack struct {
	ID       int     `json:"id"`
	Position int     `json:"position"`
	URL      string  `json:"url"`
	Artist   string  `json:"artist"`
	Title    string  `json:"title"`
	Duration float64 `json:"duration"`
}

// Client is the minimal set of operations a player needs to control mpd.
// internal/player depends on this interface, not on mpd directly, so its
// tests can run against FakeClient without a real mpd server.
type Client interface {
	// Add adds a URL to the current playlist.
	Add(uri string) error
	// AddGetID adds a URL to the end of the current playlist and returns
	// the queue id assigned to it, so the caller can immediately move/play
	// that specific entry (see PlayURL) rather than whatever mpd's "current
	// song" pointer happens to already be on.
	AddGetID(uri string) (int, error)
	// Play starts or resumes playback.
	Play() error
	// Pause pauses playback.
	Pause() error
	// Stop stops playback.
	Stop() error
	// Status returns the current player status.
	Status() (Status, error)
	// Seek seeks to absolute position d within the current song.
	Seek(d time.Duration) error
	// SeekRelative seeks by relative offset d (positive or negative)
	// within the current song.
	SeekRelative(d time.Duration) error
	// Next skips to the next song in the playlist.
	Next() error
	// Previous skips to the previous song in the playlist.
	Previous() error
	// SetVolume sets the output volume, 0-100.
	SetVolume(volume int) error
	// AlbumArt returns album art bytes for the song at uri, or nil if
	// none is available.
	AlbumArt(uri string) ([]byte, error)
	// Queue returns the current playback queue, in order.
	Queue() ([]QueueTrack, error)
	// RemoveFromQueue removes the queue entry with the given id.
	RemoveFromQueue(id int) error
	// MoveInQueue moves the queue entry with the given id to position.
	MoveInQueue(id, position int) error
	// PlayQueueItem starts playback at the queue entry with the given id.
	PlayQueueItem(id int) error
	// ClearQueue removes all entries from the queue.
	ClearQueue() error
}

// GompdClient is a Client implementation backed by a real mpd connection
// via gompd. All methods that touch conn are synchronized via mu, since a
// background broadcast loop may call Status() concurrently with
// HTTP-handler-triggered command calls on the same underlying connection,
// and mpd's text protocol is not safe for concurrent use.
//
// AlbumArt gets its own dedicated connection (artConn/artMu) rather than
// sharing conn/mu: gompd's ReadPicture/AlbumArt fetch mpd's "readpicture"/
// "albumart" binary response in a loop, one mpd command per chunk (mpd
// caps each response around a few KB), so a single embedded-art fetch can
// mean dozens of round trips to mpd — all held under one lock/unlock pair
// for the whole call. Sharing conn/mu would mean an in-flight art fetch
// (e.g. a Library grid rendering many thumbnails at once) blocks every
// Status() call the 1s ticker makes for its whole duration, stalling the
// UI/OLED. If a second connection can't be opened for some reason,
// artConn falls back to conn (see Dial) and AlbumArt falls back to
// locking mu instead — degraded (back to the contention this avoids) but
// not broken, since album art is best-effort already.
type GompdClient struct {
	mu   sync.Mutex
	conn *mpd.Client

	artMu   sync.Mutex
	artConn *mpd.Client
}

// Dial connects to an mpd server listening on address addr (e.g.
// "127.0.0.1:6600") over network network (e.g. "tcp"), returning a
// GompdClient wrapping the connection. It also opens a second, dedicated
// connection for AlbumArt (see GompdClient's doc comment) — best-effort;
// if that second dial fails, AlbumArt falls back to sharing the primary
// connection rather than failing the whole daemon over it.
func Dial(network, addr string) (*GompdClient, error) {
	conn, err := mpd.Dial(network, addr)
	if err != nil {
		return nil, err
	}
	artConn, err := mpd.Dial(network, addr)
	if err != nil {
		log.Printf("mpdclient: dedicated album-art connection unavailable, album art will share the "+
			"primary mpd connection instead: %v", err)
		artConn = conn
	}
	return &GompdClient{conn: conn, artConn: artConn}, nil
}

// Close closes the underlying mpd connection(s).
func (c *GompdClient) Close() error {
	if c.artConn != c.conn {
		c.artMu.Lock()
		c.artConn.Close()
		c.artMu.Unlock()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Close()
}

// Add adds a URL to the current playlist.
func (c *GompdClient) Add(uri string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Add(uri)
}

// AddGetID adds a URL to the end of the current playlist and returns the
// queue id assigned to it.
func (c *GompdClient) AddGetID(uri string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.AddID(uri, -1)
}

// Play starts/resumes playback at the current position in the playlist.
func (c *GompdClient) Play() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Play(-1)
}

// Pause pauses playback.
func (c *GompdClient) Pause() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Pause(true)
}

// Stop stops playback.
func (c *GompdClient) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Stop()
}

// Status returns the current player status, translated from mpd's
// Attrs (map[string]string) into a Status struct.
func (c *GompdClient) Status() (Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	attrs, err := c.conn.Status()
	if err != nil {
		return Status{}, err
	}

	status := Status{State: attrs["state"]}
	status.Elapsed, _ = strconv.ParseFloat(attrs["elapsed"], 64)
	status.Duration, _ = strconv.ParseFloat(attrs["duration"], 64)
	status.Volume, _ = strconv.Atoi(attrs["volume"])
	status.SongID, _ = strconv.Atoi(attrs["songid"])

	if song, err := c.conn.CurrentSong(); err == nil {
		// Song is always the URI — never falls back to Title. An earlier
		// version preferred Title here (when present) with file as the
		// fallback, which broke every URL-keyed use of Song (album art
		// lookup by URL, matching a queue/library row to "now playing") for
		// any track with an embedded Title tag, i.e. most of them; Title
		// already exists as its own field for display, so that fallback
		// was pure redundancy with a real cost, not a deliberate choice.
		status.Song = song["file"]
		status.Artist = song["Artist"]
		status.Album = song["Album"]
		status.Title = song["Title"]
	}

	return status, nil
}

// Seek seeks to absolute position d within the current song.
func (c *GompdClient) Seek(d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.SeekCur(d, false)
}

// SeekRelative seeks by relative offset d within the current song.
func (c *GompdClient) SeekRelative(d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.SeekCur(d, true)
}

// Next skips to the next song in the playlist.
func (c *GompdClient) Next() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Next()
}

// Previous skips to the previous song in the playlist.
func (c *GompdClient) Previous() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Previous()
}

// SetVolume sets the output volume, 0-100.
func (c *GompdClient) SetVolume(volume int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.SetVolume(volume)
}

// AlbumArt returns album art bytes for the song at uri. It first tries
// embedded tag art via mpd's readpicture command, falling back to a cover
// file via mpd's albumart command if that fails or returns no data.
func (c *GompdClient) AlbumArt(uri string) ([]byte, error) {
	if c.artConn == c.conn {
		// No dedicated connection was available at Dial time — fall back
		// to the shared one, guarded by the same mutex as every other
		// method (see GompdClient's doc comment).
		c.mu.Lock()
		defer c.mu.Unlock()
		return fetchAlbumArt(c.conn, uri)
	}
	c.artMu.Lock()
	defer c.artMu.Unlock()
	return fetchAlbumArt(c.artConn, uri)
}

func fetchAlbumArt(conn *mpd.Client, uri string) ([]byte, error) {
	if data, err := conn.ReadPicture(uri); err == nil && len(data) > 0 {
		return data, nil
	}
	data, err := conn.AlbumArt(uri)
	if err != nil {
		var mpdErr mpd.Error
		if errors.As(err, &mpdErr) {
			// mpd itself definitively answered — no cover file, no such
			// song, etc. (a real ACK response, meaning mpd is alive and
			// processed the command) — that's a confirmed "no art"
			// result, not a failure, so the caller (internal/api's
			// album-art cache) can cache it as such rather than treating
			// it the same as a genuine connection/protocol error.
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

// Queue returns the current playback queue, translated from mpd's
// PlaylistInfo Attrs into QueueTrack structs.
func (c *GompdClient) Queue() ([]QueueTrack, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	attrsList, err := c.conn.PlaylistInfo(-1, -1)
	if err != nil {
		return nil, err
	}

	tracks := make([]QueueTrack, len(attrsList))
	for i, attrs := range attrsList {
		id, _ := strconv.Atoi(attrs["Id"])
		pos, _ := strconv.Atoi(attrs["Pos"])
		duration, _ := strconv.ParseFloat(attrs["duration"], 64)
		tracks[i] = QueueTrack{
			ID:       id,
			Position: pos,
			URL:      attrs["file"],
			Artist:   attrs["Artist"],
			Title:    attrs["Title"],
			Duration: duration,
		}
	}
	return tracks, nil
}

// RemoveFromQueue removes the queue entry with the given id.
func (c *GompdClient) RemoveFromQueue(id int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.DeleteID(id)
}

// MoveInQueue moves the queue entry with the given id to position.
func (c *GompdClient) MoveInQueue(id, position int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.MoveID(id, position)
}

// PlayQueueItem starts playback at the queue entry with the given id.
func (c *GompdClient) PlayQueueItem(id int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.PlayID(id)
}

// ClearQueue removes all entries from the queue.
func (c *GompdClient) ClearQueue() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Clear()
}

// Ensure GompdClient satisfies Client at compile time.
var _ Client = (*GompdClient)(nil)
