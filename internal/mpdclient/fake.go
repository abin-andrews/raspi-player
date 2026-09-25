package mpdclient

import "time"

// FakeClient is an in-memory Client implementation for use in unit tests
// of code that depends on mpdclient.Client (e.g. internal/player), so
// those tests don't need a real mpd server.
type FakeClient struct {
	// Playlist holds the URIs added via Add, in order.
	Playlist []string
	// State is the current player state: "stop", "play", or "pause".
	State string
	// Song is the URI of the current song, if any.
	Song string
	// Artist is the current song's artist, if set by the test.
	Artist string
	// Album is the current song's album, if set by the test.
	Album string
	// Title is the current song's title, if set by the test.
	Title string
	// Elapsed is the number of seconds played into the current song.
	Elapsed float64
	// Duration is the total length of the current song in seconds.
	Duration float64
	// SeekCalls records every duration passed to Seek, in order.
	SeekCalls []time.Duration
	// SeekRelativeCalls records every duration passed to SeekRelative, in
	// order.
	SeekRelativeCalls []time.Duration
	// NextCalls counts calls to Next.
	NextCalls int
	// PreviousCalls counts calls to Previous.
	PreviousCalls int
	// Volume is the current output volume returned by Status.
	Volume int
	// VolumeCalls records every volume passed to SetVolume, in order.
	VolumeCalls []int
	// Art is the album art bytes returned by AlbumArt. Tests can set
	// this beforehand to simulate art being found; the zero value (nil)
	// simulates no art found, mpd's common case.
	Art []byte
	// QueueTracks holds the current playback queue, in order. Named
	// distinctly from the Queue() method (Go forbids a field and method
	// sharing a name), mirroring how Playlist differs from Add().
	QueueTracks []QueueTrack
	// NextQueueID is the id assigned to the next track appended to
	// QueueTracks via Add. Starts at 1 so ids are never 0/ambiguous.
	NextQueueID int
	// SongID is the queue id of the current song, returned via Status.
	SongID int
}

// NewFakeClient returns a FakeClient with an empty playlist and stopped
// state, mirroring a freshly started mpd.
func NewFakeClient() *FakeClient {
	return &FakeClient{State: "stop", NextQueueID: 1}
}

// Add appends uri to the playlist. If nothing is currently selected as the
// current song, it becomes the current song (mimicking mpd auto-selecting
// the first track added to an empty playlist). It also appends a
// corresponding entry to QueueTracks with a fresh, sequential id.
func (c *FakeClient) Add(uri string) error {
	c.Playlist = append(c.Playlist, uri)
	if c.Song == "" {
		c.Song = uri
	}
	if c.NextQueueID == 0 {
		c.NextQueueID = 1
	}
	c.QueueTracks = append(c.QueueTracks, QueueTrack{ID: c.NextQueueID, Position: len(c.QueueTracks), URL: uri})
	c.NextQueueID++
	return nil
}

// Play sets the state to "play". If no song is current yet and the
// playlist is non-empty, it selects the first song.
func (c *FakeClient) Play() error {
	if c.Song == "" && len(c.Playlist) > 0 {
		c.Song = c.Playlist[0]
	}
	c.State = "play"
	return nil
}

// Pause sets the state to "pause".
func (c *FakeClient) Pause() error {
	c.State = "pause"
	return nil
}

// Stop sets the state to "stop".
func (c *FakeClient) Stop() error {
	c.State = "stop"
	return nil
}

// Status returns the current state, song, and metadata/progress fields.
func (c *FakeClient) Status() (Status, error) {
	return Status{
		State:    c.State,
		Song:     c.Song,
		Artist:   c.Artist,
		Album:    c.Album,
		Title:    c.Title,
		Elapsed:  c.Elapsed,
		Duration: c.Duration,
		Volume:   c.Volume,
		SongID:   c.SongID,
	}, nil
}

// Seek records d in SeekCalls.
func (c *FakeClient) Seek(d time.Duration) error {
	c.SeekCalls = append(c.SeekCalls, d)
	return nil
}

// SeekRelative records d in SeekRelativeCalls.
func (c *FakeClient) SeekRelative(d time.Duration) error {
	c.SeekRelativeCalls = append(c.SeekRelativeCalls, d)
	return nil
}

// Next increments NextCalls.
func (c *FakeClient) Next() error {
	c.NextCalls++
	return nil
}

// Previous increments PreviousCalls.
func (c *FakeClient) Previous() error {
	c.PreviousCalls++
	return nil
}

// SetVolume records volume in VolumeCalls.
func (c *FakeClient) SetVolume(volume int) error {
	c.VolumeCalls = append(c.VolumeCalls, volume)
	return nil
}

// AlbumArt returns the configured Art bytes, or nil if none were set.
func (c *FakeClient) AlbumArt(uri string) ([]byte, error) {
	return c.Art, nil
}

// renumberQueue reassigns Position on every entry in c.QueueTracks to match
// its current index, after an insert/remove/move changes the slice order.
func (c *FakeClient) renumberQueue() {
	for i := range c.QueueTracks {
		c.QueueTracks[i].Position = i
	}
}

// Queue returns a defensive copy of the current playback queue.
func (c *FakeClient) Queue() ([]QueueTrack, error) {
	return append([]QueueTrack(nil), c.QueueTracks...), nil
}

// RemoveFromQueue removes the entry with the given id from QueueTracks, if
// present, and renumbers the remaining entries' Position fields. Removing
// an id that isn't found is not an error.
func (c *FakeClient) RemoveFromQueue(id int) error {
	for i, t := range c.QueueTracks {
		if t.ID == id {
			c.QueueTracks = append(c.QueueTracks[:i], c.QueueTracks[i+1:]...)
			c.renumberQueue()
			return nil
		}
	}
	return nil
}

// MoveInQueue moves the entry with the given id to position (clamped into
// [0, len(QueueTracks)]) and renumbers all entries' Position fields.
func (c *FakeClient) MoveInQueue(id, position int) error {
	idx := -1
	for i, t := range c.QueueTracks {
		if t.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil
	}

	track := c.QueueTracks[idx]
	c.QueueTracks = append(c.QueueTracks[:idx], c.QueueTracks[idx+1:]...)

	if position < 0 {
		position = 0
	} else if position > len(c.QueueTracks) {
		position = len(c.QueueTracks)
	}

	c.QueueTracks = append(c.QueueTracks, QueueTrack{})
	copy(c.QueueTracks[position+1:], c.QueueTracks[position:])
	c.QueueTracks[position] = track

	c.renumberQueue()
	return nil
}

// PlayQueueItem simulates jumping playback to the queue entry with the
// given id: sets Song to its URL and State to "play". If the id isn't
// found, it still sets State to "play".
func (c *FakeClient) PlayQueueItem(id int) error {
	for _, t := range c.QueueTracks {
		if t.ID == id {
			c.Song = t.URL
			c.State = "play"
			return nil
		}
	}
	c.State = "play"
	return nil
}

// ClearQueue empties QueueTracks and Playlist.
func (c *FakeClient) ClearQueue() error {
	c.QueueTracks = nil
	c.Playlist = nil
	return nil
}
