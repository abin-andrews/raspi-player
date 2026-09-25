package store

import (
	"errors"
	"testing"
	"time"
)

func TestHistory(t *testing.T) {
	t.Run("empty store returns empty history", func(t *testing.T) {
		s := NewMemoryStore()

		got, err := s.History(0)
		if err != nil {
			t.Fatalf("History() error = %v, want nil", err)
		}
		if len(got) != 0 {
			t.Fatalf("History() = %v, want empty", got)
		}
	})

	t.Run("most recent plays first", func(t *testing.T) {
		s := NewMemoryStore()
		base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
		tracks := []Track{
			{URL: "a", Title: "A", PlayedAt: base},
			{URL: "b", Title: "B", PlayedAt: base.Add(time.Minute)},
			{URL: "c", Title: "C", PlayedAt: base.Add(2 * time.Minute)},
		}
		for _, tr := range tracks {
			if err := s.AddHistory(tr); err != nil {
				t.Fatalf("AddHistory(%v) error = %v", tr, err)
			}
		}

		got, err := s.History(0)
		if err != nil {
			t.Fatalf("History() error = %v, want nil", err)
		}
		wantOrder := []string{"c", "b", "a"}
		if len(got) != len(wantOrder) {
			t.Fatalf("History() len = %d, want %d", len(got), len(wantOrder))
		}
		for i, url := range wantOrder {
			if got[i].URL != url {
				t.Errorf("History()[%d].URL = %q, want %q", i, got[i].URL, url)
			}
		}
	})

	t.Run("limit caps results", func(t *testing.T) {
		s := NewMemoryStore()
		for i := 0; i < 5; i++ {
			if err := s.AddHistory(Track{URL: string(rune('a' + i))}); err != nil {
				t.Fatalf("AddHistory() error = %v", err)
			}
		}

		got, err := s.History(2)
		if err != nil {
			t.Fatalf("History(2) error = %v, want nil", err)
		}
		if len(got) != 2 {
			t.Fatalf("History(2) len = %d, want 2", len(got))
		}
		// Most recent two: "e", "d"
		if got[0].URL != "e" || got[1].URL != "d" {
			t.Errorf("History(2) = %v, want [e d]", got)
		}
	})

	t.Run("negative limit means no cap", func(t *testing.T) {
		s := NewMemoryStore()
		for i := 0; i < 3; i++ {
			if err := s.AddHistory(Track{URL: string(rune('a' + i))}); err != nil {
				t.Fatalf("AddHistory() error = %v", err)
			}
		}

		got, err := s.History(-1)
		if err != nil {
			t.Fatalf("History(-1) error = %v, want nil", err)
		}
		if len(got) != 3 {
			t.Fatalf("History(-1) len = %d, want 3", len(got))
		}
	})

	t.Run("limit larger than history returns all", func(t *testing.T) {
		s := NewMemoryStore()
		if err := s.AddHistory(Track{URL: "only"}); err != nil {
			t.Fatalf("AddHistory() error = %v", err)
		}

		got, err := s.History(100)
		if err != nil {
			t.Fatalf("History(100) error = %v, want nil", err)
		}
		if len(got) != 1 {
			t.Fatalf("History(100) len = %d, want 1", len(got))
		}
	})
}

func TestFavorites(t *testing.T) {
	t.Run("add and list favorites", func(t *testing.T) {
		s := NewMemoryStore()
		if err := s.AddFavorite(Track{URL: "a", Title: "A"}); err != nil {
			t.Fatalf("AddFavorite() error = %v", err)
		}
		if err := s.AddFavorite(Track{URL: "b", Title: "B"}); err != nil {
			t.Fatalf("AddFavorite() error = %v", err)
		}

		got, err := s.Favorites()
		if err != nil {
			t.Fatalf("Favorites() error = %v, want nil", err)
		}
		if len(got) != 2 {
			t.Fatalf("Favorites() len = %d, want 2", len(got))
		}
	})

	t.Run("adding same URL twice does not duplicate", func(t *testing.T) {
		s := NewMemoryStore()
		if err := s.AddFavorite(Track{URL: "a", Title: "A"}); err != nil {
			t.Fatalf("AddFavorite() error = %v", err)
		}
		if err := s.AddFavorite(Track{URL: "a", Title: "A (again)"}); err != nil {
			t.Fatalf("AddFavorite() error = %v", err)
		}

		got, err := s.Favorites()
		if err != nil {
			t.Fatalf("Favorites() error = %v, want nil", err)
		}
		if len(got) != 1 {
			t.Fatalf("Favorites() len = %d, want 1 (idempotent add)", len(got))
		}
	})

	t.Run("remove favorite", func(t *testing.T) {
		s := NewMemoryStore()
		if err := s.AddFavorite(Track{URL: "a"}); err != nil {
			t.Fatalf("AddFavorite() error = %v", err)
		}
		if err := s.AddFavorite(Track{URL: "b"}); err != nil {
			t.Fatalf("AddFavorite() error = %v", err)
		}

		if err := s.RemoveFavorite("a"); err != nil {
			t.Fatalf("RemoveFavorite() error = %v", err)
		}

		got, err := s.Favorites()
		if err != nil {
			t.Fatalf("Favorites() error = %v, want nil", err)
		}
		if len(got) != 1 {
			t.Fatalf("Favorites() len = %d, want 1", len(got))
		}
		if got[0].URL != "b" {
			t.Errorf("Favorites()[0].URL = %q, want %q", got[0].URL, "b")
		}
	})

	t.Run("removing a non-favorite is not an error", func(t *testing.T) {
		s := NewMemoryStore()
		if err := s.RemoveFavorite("nonexistent"); err != nil {
			t.Fatalf("RemoveFavorite() error = %v, want nil", err)
		}
	})
}

func TestPlaylists(t *testing.T) {
	t.Run("create and list playlists", func(t *testing.T) {
		s := NewMemoryStore()
		if err := s.CreatePlaylist("road-trip"); err != nil {
			t.Fatalf("CreatePlaylist() error = %v", err)
		}
		if err := s.CreatePlaylist("chill"); err != nil {
			t.Fatalf("CreatePlaylist() error = %v", err)
		}

		got, err := s.Playlists()
		if err != nil {
			t.Fatalf("Playlists() error = %v, want nil", err)
		}
		if len(got) != 2 {
			t.Fatalf("Playlists() len = %d, want 2", len(got))
		}
	})

	t.Run("duplicate playlist name errors", func(t *testing.T) {
		s := NewMemoryStore()
		if err := s.CreatePlaylist("road-trip"); err != nil {
			t.Fatalf("CreatePlaylist() error = %v", err)
		}
		if err := s.CreatePlaylist("road-trip"); err == nil {
			t.Fatal("CreatePlaylist() duplicate name: error = nil, want error")
		}
	})

	t.Run("add to playlist and read back", func(t *testing.T) {
		s := NewMemoryStore()
		if err := s.CreatePlaylist("road-trip"); err != nil {
			t.Fatalf("CreatePlaylist() error = %v", err)
		}
		if err := s.AddToPlaylist("road-trip", Track{URL: "a", Title: "A"}); err != nil {
			t.Fatalf("AddToPlaylist() error = %v", err)
		}
		if err := s.AddToPlaylist("road-trip", Track{URL: "b", Title: "B"}); err != nil {
			t.Fatalf("AddToPlaylist() error = %v", err)
		}

		got, err := s.Playlist("road-trip")
		if err != nil {
			t.Fatalf("Playlist() error = %v, want nil", err)
		}
		if len(got) != 2 {
			t.Fatalf("Playlist() len = %d, want 2", len(got))
		}
		if got[0].URL != "a" || got[1].URL != "b" {
			t.Errorf("Playlist() = %v, want [a b] in insertion order", got)
		}
	})

	t.Run("add to nonexistent playlist errors", func(t *testing.T) {
		s := NewMemoryStore()
		err := s.AddToPlaylist("ghost", Track{URL: "a"})
		if err == nil {
			t.Fatal("AddToPlaylist() on missing playlist: error = nil, want error")
		}
	})

	t.Run("read nonexistent playlist errors", func(t *testing.T) {
		s := NewMemoryStore()
		_, err := s.Playlist("ghost")
		if err == nil {
			t.Fatal("Playlist() on missing playlist: error = nil, want error")
		}
	})

	t.Run("playlists start empty", func(t *testing.T) {
		s := NewMemoryStore()
		got, err := s.Playlists()
		if err != nil {
			t.Fatalf("Playlists() error = %v, want nil", err)
		}
		if len(got) != 0 {
			t.Fatalf("Playlists() = %v, want empty", got)
		}
	})

	t.Run("newly created playlist is empty", func(t *testing.T) {
		s := NewMemoryStore()
		if err := s.CreatePlaylist("empty-one"); err != nil {
			t.Fatalf("CreatePlaylist() error = %v", err)
		}
		got, err := s.Playlist("empty-one")
		if err != nil {
			t.Fatalf("Playlist() error = %v, want nil", err)
		}
		if len(got) != 0 {
			t.Fatalf("Playlist() = %v, want empty", got)
		}
	})
}

func TestErrorsAreSentinelWrapped(t *testing.T) {
	// Not-found style errors should be usable with errors.Is if the
	// implementation defines sentinel errors; at minimum they must be
	// non-nil and distinguishable from success.
	s := NewMemoryStore()
	_, err := s.Playlist("missing")
	if err == nil {
		t.Fatal("expected error for missing playlist")
	}
	if errors.Is(err, nil) {
		t.Fatal("error should not be nil-equivalent")
	}
}
