package mpdclient

import (
	"testing"
	"time"
)

func TestFakeClient_AddAppendsToPlaylist(t *testing.T) {
	c := NewFakeClient()

	if err := c.Add("http://example.com/song1.mp3"); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if err := c.Add("http://example.com/song2.mp3"); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}

	want := []string{"http://example.com/song1.mp3", "http://example.com/song2.mp3"}
	if len(c.Playlist) != len(want) {
		t.Fatalf("Playlist = %v, want %v", c.Playlist, want)
	}
	for i, uri := range want {
		if c.Playlist[i] != uri {
			t.Errorf("Playlist[%d] = %q, want %q", i, c.Playlist[i], uri)
		}
	}
}

func TestFakeClient_PlaySetsState(t *testing.T) {
	c := NewFakeClient()
	_ = c.Add("http://example.com/song1.mp3")

	if err := c.Play(); err != nil {
		t.Fatalf("Play returned error: %v", err)
	}

	if c.State != "play" {
		t.Errorf("State = %q, want %q", c.State, "play")
	}
}

func TestFakeClient_PauseSetsState(t *testing.T) {
	c := NewFakeClient()
	_ = c.Add("http://example.com/song1.mp3")
	_ = c.Play()

	if err := c.Pause(); err != nil {
		t.Fatalf("Pause returned error: %v", err)
	}

	if c.State != "pause" {
		t.Errorf("State = %q, want %q", c.State, "pause")
	}
}

func TestFakeClient_StopSetsState(t *testing.T) {
	c := NewFakeClient()
	_ = c.Add("http://example.com/song1.mp3")
	_ = c.Play()

	if err := c.Stop(); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}

	if c.State != "stop" {
		t.Errorf("State = %q, want %q", c.State, "stop")
	}
}

func TestFakeClient_StatusReflectsStateAndSong(t *testing.T) {
	c := NewFakeClient()
	_ = c.Add("http://example.com/song1.mp3")
	_ = c.Play()

	got, err := c.Status()
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}

	want := Status{State: "play", Song: "http://example.com/song1.mp3"}
	if got != want {
		t.Errorf("Status() = %+v, want %+v", got, want)
	}
}

func TestFakeClient_SeekRecordsCall(t *testing.T) {
	c := NewFakeClient()

	if err := c.Seek(30 * time.Second); err != nil {
		t.Fatalf("Seek returned error: %v", err)
	}
	if err := c.Seek(90 * time.Second); err != nil {
		t.Fatalf("Seek returned error: %v", err)
	}

	want := []time.Duration{30 * time.Second, 90 * time.Second}
	if len(c.SeekCalls) != len(want) {
		t.Fatalf("SeekCalls = %v, want %v", c.SeekCalls, want)
	}
	for i, d := range want {
		if c.SeekCalls[i] != d {
			t.Errorf("SeekCalls[%d] = %v, want %v", i, c.SeekCalls[i], d)
		}
	}
}

func TestFakeClient_AlbumArtDefaultsToNil(t *testing.T) {
	c := NewFakeClient()

	got, err := c.AlbumArt("http://example.com/song1.mp3")
	if err != nil {
		t.Fatalf("AlbumArt returned error: %v", err)
	}
	if got != nil {
		t.Errorf("AlbumArt() = %v, want nil", got)
	}
}

func TestFakeClient_AlbumArtReturnsConfiguredArt(t *testing.T) {
	c := NewFakeClient()
	c.Art = []byte{0xFF, 0xD8, 0xFF}

	got, err := c.AlbumArt("http://example.com/song1.mp3")
	if err != nil {
		t.Fatalf("AlbumArt returned error: %v", err)
	}
	if string(got) != string(c.Art) {
		t.Errorf("AlbumArt() = %v, want %v", got, c.Art)
	}
}

func TestFakeClient_StatusReflectsMetadataAndProgress(t *testing.T) {
	c := NewFakeClient()
	c.State = "play"
	c.Song = "http://example.com/song1.mp3"
	c.Artist = "Some Artist"
	c.Album = "Some Album"
	c.Title = "Some Title"
	c.Elapsed = 12.5
	c.Duration = 245.0

	got, err := c.Status()
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}

	want := Status{
		State:    "play",
		Song:     "http://example.com/song1.mp3",
		Artist:   "Some Artist",
		Album:    "Some Album",
		Title:    "Some Title",
		Elapsed:  12.5,
		Duration: 245.0,
	}
	if got != want {
		t.Errorf("Status() = %+v, want %+v", got, want)
	}
}

func TestFakeClient_NextIncrementsCalls(t *testing.T) {
	c := NewFakeClient()

	if err := c.Next(); err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if err := c.Next(); err != nil {
		t.Fatalf("Next returned error: %v", err)
	}

	if c.NextCalls != 2 {
		t.Errorf("NextCalls = %d, want 2", c.NextCalls)
	}
}

func TestFakeClient_PreviousIncrementsCalls(t *testing.T) {
	c := NewFakeClient()

	if err := c.Previous(); err != nil {
		t.Fatalf("Previous returned error: %v", err)
	}

	if c.PreviousCalls != 1 {
		t.Errorf("PreviousCalls = %d, want 1", c.PreviousCalls)
	}
}

func TestFakeClient_SetVolumeRecordsCall(t *testing.T) {
	c := NewFakeClient()

	if err := c.SetVolume(42); err != nil {
		t.Fatalf("SetVolume returned error: %v", err)
	}
	if err := c.SetVolume(80); err != nil {
		t.Fatalf("SetVolume returned error: %v", err)
	}

	want := []int{42, 80}
	if len(c.VolumeCalls) != len(want) {
		t.Fatalf("VolumeCalls = %v, want %v", c.VolumeCalls, want)
	}
	for i, v := range want {
		if c.VolumeCalls[i] != v {
			t.Errorf("VolumeCalls[%d] = %d, want %d", i, c.VolumeCalls[i], v)
		}
	}
}

func TestFakeClient_SeekRelativeRecordsCall(t *testing.T) {
	c := NewFakeClient()

	if err := c.SeekRelative(10 * time.Second); err != nil {
		t.Fatalf("SeekRelative returned error: %v", err)
	}
	if err := c.SeekRelative(-10 * time.Second); err != nil {
		t.Fatalf("SeekRelative returned error: %v", err)
	}

	want := []time.Duration{10 * time.Second, -10 * time.Second}
	if len(c.SeekRelativeCalls) != len(want) {
		t.Fatalf("SeekRelativeCalls = %v, want %v", c.SeekRelativeCalls, want)
	}
	for i, d := range want {
		if c.SeekRelativeCalls[i] != d {
			t.Errorf("SeekRelativeCalls[%d] = %v, want %v", i, c.SeekRelativeCalls[i], d)
		}
	}
}

func TestFakeClient_StatusReflectsVolume(t *testing.T) {
	c := NewFakeClient()
	c.Volume = 55

	got, err := c.Status()
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if got.Volume != 55 {
		t.Errorf("Status().Volume = %d, want 55", got.Volume)
	}
}

func TestFakeClient_QueueReflectsAddedTracks(t *testing.T) {
	c := NewFakeClient()
	_ = c.Add("http://example.com/song1.mp3")
	_ = c.Add("http://example.com/song2.mp3")

	queue, err := c.Queue()
	if err != nil {
		t.Fatalf("Queue returned error: %v", err)
	}
	if len(queue) != 2 {
		t.Fatalf("Queue = %v, want 2 entries", queue)
	}
	if queue[0].ID != 1 || queue[0].Position != 0 || queue[0].URL != "http://example.com/song1.mp3" {
		t.Errorf("queue[0] = %+v, want ID=1 Position=0 URL=song1", queue[0])
	}
	if queue[1].ID != 2 || queue[1].Position != 1 || queue[1].URL != "http://example.com/song2.mp3" {
		t.Errorf("queue[1] = %+v, want ID=2 Position=1 URL=song2", queue[1])
	}
}

func TestFakeClient_QueueReturnsDefensiveCopy(t *testing.T) {
	c := NewFakeClient()
	_ = c.Add("http://example.com/song1.mp3")

	queue, err := c.Queue()
	if err != nil {
		t.Fatalf("Queue returned error: %v", err)
	}
	queue[0].URL = "mutated"

	queue2, err := c.Queue()
	if err != nil {
		t.Fatalf("Queue returned error: %v", err)
	}
	if queue2[0].URL != "http://example.com/song1.mp3" {
		t.Errorf("Queue()[0].URL = %q after mutating a prior copy, want unaffected", queue2[0].URL)
	}
}

func TestFakeClient_RemoveFromQueueRenumbersPositions(t *testing.T) {
	c := NewFakeClient()
	_ = c.Add("http://example.com/song1.mp3")
	_ = c.Add("http://example.com/song2.mp3")
	_ = c.Add("http://example.com/song3.mp3")

	if err := c.RemoveFromQueue(2); err != nil {
		t.Fatalf("RemoveFromQueue returned error: %v", err)
	}

	queue, err := c.Queue()
	if err != nil {
		t.Fatalf("Queue returned error: %v", err)
	}
	if len(queue) != 2 {
		t.Fatalf("Queue = %v, want 2 entries", queue)
	}
	if queue[0].ID != 1 || queue[0].Position != 0 {
		t.Errorf("queue[0] = %+v, want ID=1 Position=0", queue[0])
	}
	if queue[1].ID != 3 || queue[1].Position != 1 {
		t.Errorf("queue[1] = %+v, want ID=3 Position=1", queue[1])
	}
}

func TestFakeClient_RemoveFromQueueUnknownIDIsNoop(t *testing.T) {
	c := NewFakeClient()
	_ = c.Add("http://example.com/song1.mp3")

	if err := c.RemoveFromQueue(999); err != nil {
		t.Fatalf("RemoveFromQueue returned error: %v", err)
	}

	queue, err := c.Queue()
	if err != nil {
		t.Fatalf("Queue returned error: %v", err)
	}
	if len(queue) != 1 {
		t.Errorf("Queue = %v, want unchanged 1 entry", queue)
	}
}

func TestFakeClient_MoveInQueueToFront(t *testing.T) {
	c := NewFakeClient()
	_ = c.Add("http://example.com/song1.mp3")
	_ = c.Add("http://example.com/song2.mp3")
	_ = c.Add("http://example.com/song3.mp3")

	if err := c.MoveInQueue(3, 0); err != nil {
		t.Fatalf("MoveInQueue returned error: %v", err)
	}

	queue, err := c.Queue()
	if err != nil {
		t.Fatalf("Queue returned error: %v", err)
	}
	wantIDs := []int{3, 1, 2}
	for i, id := range wantIDs {
		if queue[i].ID != id || queue[i].Position != i {
			t.Errorf("queue[%d] = %+v, want ID=%d Position=%d", i, queue[i], id, i)
		}
	}
}

func TestFakeClient_MoveInQueueToBack(t *testing.T) {
	c := NewFakeClient()
	_ = c.Add("http://example.com/song1.mp3")
	_ = c.Add("http://example.com/song2.mp3")
	_ = c.Add("http://example.com/song3.mp3")

	if err := c.MoveInQueue(1, 2); err != nil {
		t.Fatalf("MoveInQueue returned error: %v", err)
	}

	queue, err := c.Queue()
	if err != nil {
		t.Fatalf("Queue returned error: %v", err)
	}
	wantIDs := []int{2, 3, 1}
	for i, id := range wantIDs {
		if queue[i].ID != id || queue[i].Position != i {
			t.Errorf("queue[%d] = %+v, want ID=%d Position=%d", i, queue[i], id, i)
		}
	}
}

func TestFakeClient_MoveInQueueToMiddle(t *testing.T) {
	c := NewFakeClient()
	_ = c.Add("http://example.com/song1.mp3")
	_ = c.Add("http://example.com/song2.mp3")
	_ = c.Add("http://example.com/song3.mp3")
	_ = c.Add("http://example.com/song4.mp3")

	if err := c.MoveInQueue(4, 1); err != nil {
		t.Fatalf("MoveInQueue returned error: %v", err)
	}

	queue, err := c.Queue()
	if err != nil {
		t.Fatalf("Queue returned error: %v", err)
	}
	wantIDs := []int{1, 4, 2, 3}
	for i, id := range wantIDs {
		if queue[i].ID != id || queue[i].Position != i {
			t.Errorf("queue[%d] = %+v, want ID=%d Position=%d", i, queue[i], id, i)
		}
	}
}

func TestFakeClient_PlayQueueItemSetsStateAndSong(t *testing.T) {
	c := NewFakeClient()
	_ = c.Add("http://example.com/song1.mp3")
	_ = c.Add("http://example.com/song2.mp3")

	if err := c.PlayQueueItem(2); err != nil {
		t.Fatalf("PlayQueueItem returned error: %v", err)
	}
	if c.State != "play" {
		t.Errorf("State = %q, want %q", c.State, "play")
	}
	if c.Song != "http://example.com/song2.mp3" {
		t.Errorf("Song = %q, want %q", c.Song, "http://example.com/song2.mp3")
	}
}

func TestFakeClient_ClearQueueEmptiesQueueAndPlaylist(t *testing.T) {
	c := NewFakeClient()
	_ = c.Add("http://example.com/song1.mp3")
	_ = c.Add("http://example.com/song2.mp3")

	if err := c.ClearQueue(); err != nil {
		t.Fatalf("ClearQueue returned error: %v", err)
	}

	queue, err := c.Queue()
	if err != nil {
		t.Fatalf("Queue returned error: %v", err)
	}
	if len(queue) != 0 {
		t.Errorf("Queue = %v, want empty", queue)
	}
	if len(c.Playlist) != 0 {
		t.Errorf("Playlist = %v, want empty", c.Playlist)
	}
}

func TestFakeClient_StatusReflectsSongID(t *testing.T) {
	c := NewFakeClient()
	c.SongID = 7

	got, err := c.Status()
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if got.SongID != 7 {
		t.Errorf("Status().SongID = %d, want 7", got.SongID)
	}
}

// Ensure FakeClient satisfies the Client interface at compile time.
var _ Client = (*FakeClient)(nil)
