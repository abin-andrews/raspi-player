// Package mpdclient is the only package in pi-streamer allowed to talk to
// mpd directly. It defines the Client interface used by the rest of the
// daemon (e.g. internal/player) to control playback, plus a real
// implementation backed by gompd and an in-memory fake for tests.
package mpdclient

import (
	"strconv"
	"sync"
	"time"

	"github.com/fhs/gompd/v2/mpd"
)

// Status is a minimal snapshot of mpd's playback status.
type Status struct {
	// State is the player state, e.g. "play", "pause", or "stop".
	State string `json:"state"`
	// Song is the URI (or title, if available) of the current song.
	// Empty if nothing is loaded.
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
type GompdClient struct {
	mu   sync.Mutex
	conn *mpd.Client
}

// Dial connects to an mpd server listening on address addr (e.g.
// "127.0.0.1:6600") over network network (e.g. "tcp"), returning a
// GompdClient wrapping the connection.
func Dial(network, addr string) (*GompdClient, error) {
	conn, err := mpd.Dial(network, addr)
	if err != nil {
		return nil, err
	}
	return &GompdClient{conn: conn}, nil
}

// Close closes the underlying mpd connection.
func (c *GompdClient) Close() error {
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
		if title, ok := song["Title"]; ok && title != "" {
			status.Song = title
		} else {
			status.Song = song["file"]
		}
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
	c.mu.Lock()
	defer c.mu.Unlock()

	if data, err := c.conn.ReadPicture(uri); err == nil && len(data) > 0 {
		return data, nil
	}
	return c.conn.AlbumArt(uri)
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
