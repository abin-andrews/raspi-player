package search

import (
	"database/sql"
	"testing"
)

func TestIndexURL(t *testing.T) {
	t.Run("indexed url is found by a word in the title", func(t *testing.T) {
		idx, err := Open(":memory:")
		if err != nil {
			t.Fatalf("Open() error = %v, want nil", err)
		}
		defer idx.Close()

		if err := idx.IndexURL("https://example.com/a", "Raspberry Pi Setup Guide", "", "", "pi,setup"); err != nil {
			t.Fatalf("IndexURL() error = %v, want nil", err)
		}

		got, err := idx.Search("Raspberry", 0)
		if err != nil {
			t.Fatalf("Search() error = %v, want nil", err)
		}
		if len(got) != 1 {
			t.Fatalf("Search() len = %d, want 1 (%v)", len(got), got)
		}
		if got[0].URL != "https://example.com/a" {
			t.Errorf("Search()[0].URL = %q, want %q", got[0].URL, "https://example.com/a")
		}
		if got[0].Title != "Raspberry Pi Setup Guide" {
			t.Errorf("Search()[0].Title = %q, want %q", got[0].Title, "Raspberry Pi Setup Guide")
		}
		if got[0].Tags != "pi,setup" {
			t.Errorf("Search()[0].Tags = %q, want %q", got[0].Tags, "pi,setup")
		}
	})

	t.Run("non-matching term returns no results", func(t *testing.T) {
		idx, err := Open(":memory:")
		if err != nil {
			t.Fatalf("Open() error = %v, want nil", err)
		}
		defer idx.Close()

		if err := idx.IndexURL("https://example.com/a", "Raspberry Pi Setup Guide", "", "", "pi,setup"); err != nil {
			t.Fatalf("IndexURL() error = %v, want nil", err)
		}

		got, err := idx.Search("nonexistentterm", 0)
		if err != nil {
			t.Fatalf("Search() error = %v, want nil", err)
		}
		if len(got) != 0 {
			t.Fatalf("Search() len = %d, want 0 (%v)", len(got), got)
		}
	})

	t.Run("re-indexing the same url does not duplicate results", func(t *testing.T) {
		idx, err := Open(":memory:")
		if err != nil {
			t.Fatalf("Open() error = %v, want nil", err)
		}
		defer idx.Close()

		if err := idx.IndexURL("https://example.com/a", "Old Title", "", "", "old"); err != nil {
			t.Fatalf("IndexURL() error = %v, want nil", err)
		}
		if err := idx.IndexURL("https://example.com/a", "New Title", "", "", "new"); err != nil {
			t.Fatalf("IndexURL() error = %v, want nil", err)
		}

		got, err := idx.Search("Title", 0)
		if err != nil {
			t.Fatalf("Search() error = %v, want nil", err)
		}
		if len(got) != 1 {
			t.Fatalf("Search() len = %d, want 1 (%v)", len(got), got)
		}
		if got[0].Title != "New Title" {
			t.Errorf("Search()[0].Title = %q, want %q", got[0].Title, "New Title")
		}
		if got[0].Tags != "new" {
			t.Errorf("Search()[0].Tags = %q, want %q", got[0].Tags, "new")
		}
	})

	t.Run("search across multiple indexed urls returns only relevant matches", func(t *testing.T) {
		idx, err := Open(":memory:")
		if err != nil {
			t.Fatalf("Open() error = %v, want nil", err)
		}
		defer idx.Close()

		urls := []struct {
			url, title, tags string
		}{
			{"https://example.com/pi", "Raspberry Pi Setup Guide", "pi,setup"},
			{"https://example.com/music", "Best Jazz Albums of 1959", "music,jazz"},
			{"https://example.com/go", "Learning the Go Programming Language", "go,programming"},
		}
		for _, u := range urls {
			if err := idx.IndexURL(u.url, u.title, "", "", u.tags); err != nil {
				t.Fatalf("IndexURL(%q) error = %v, want nil", u.url, err)
			}
		}

		got, err := idx.Search("Jazz", 0)
		if err != nil {
			t.Fatalf("Search() error = %v, want nil", err)
		}
		if len(got) != 1 {
			t.Fatalf("Search() len = %d, want 1 (%v)", len(got), got)
		}
		if got[0].URL != "https://example.com/music" {
			t.Errorf("Search()[0].URL = %q, want %q", got[0].URL, "https://example.com/music")
		}

		got, err = idx.Search("programming", 0)
		if err != nil {
			t.Fatalf("Search() error = %v, want nil", err)
		}
		if len(got) != 1 {
			t.Fatalf("Search() len = %d, want 1 (%v)", len(got), got)
		}
		if got[0].URL != "https://example.com/go" {
			t.Errorf("Search()[0].URL = %q, want %q", got[0].URL, "https://example.com/go")
		}
	})

	t.Run("search respects limit", func(t *testing.T) {
		idx, err := Open(":memory:")
		if err != nil {
			t.Fatalf("Open() error = %v, want nil", err)
		}
		defer idx.Close()

		for i := 0; i < 5; i++ {
			url := "https://example.com/" + string(rune('a'+i))
			if err := idx.IndexURL(url, "Common Title Word", "", "", "tag"); err != nil {
				t.Fatalf("IndexURL(%q) error = %v, want nil", url, err)
			}
		}

		got, err := idx.Search("Common", 2)
		if err != nil {
			t.Fatalf("Search() error = %v, want nil", err)
		}
		if len(got) != 2 {
			t.Fatalf("Search() len = %d, want 2 (%v)", len(got), got)
		}
	})
}

func TestIndexURLStoresArtistAndAlbumAsRealFields(t *testing.T) {
	idx, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	defer idx.Close()

	if err := idx.IndexURL("https://example.com/a", "Dreams", "Fleetwood Mac", "Rumours", ""); err != nil {
		t.Fatalf("IndexURL() error = %v, want nil", err)
	}

	got, err := idx.List(0, 0)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}
	if len(got) != 1 {
		t.Fatalf("List() len = %d, want 1", len(got))
	}
	if got[0].Artist != "Fleetwood Mac" {
		t.Errorf("Artist = %q, want %q", got[0].Artist, "Fleetwood Mac")
	}
	if got[0].Album != "Rumours" {
		t.Errorf("Album = %q, want %q", got[0].Album, "Rumours")
	}
}

func TestReIndexEnrichesAThinEntryWithArtistAndAlbum(t *testing.T) {
	idx, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	defer idx.Close()

	// Mirrors what actually happens: a thin entry indexed at add-time (no
	// artist/album known yet), later replaced once mpd has decoded the
	// track's real tags.
	if err := idx.IndexURL("https://example.com/a", "", "", "", ""); err != nil {
		t.Fatalf("initial IndexURL() error = %v, want nil", err)
	}
	if err := idx.IndexURL("https://example.com/a", "Dreams", "Fleetwood Mac", "Rumours", ""); err != nil {
		t.Fatalf("enriching IndexURL() error = %v, want nil", err)
	}

	got, err := idx.List(0, 0)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}
	if len(got) != 1 {
		t.Fatalf("List() len = %d, want 1 (re-index should replace, not duplicate)", len(got))
	}
	if got[0].Title != "Dreams" || got[0].Artist != "Fleetwood Mac" || got[0].Album != "Rumours" {
		t.Errorf("List()[0] = %+v, want the enriched entry", got[0])
	}
}

func TestOpenMigratesAPreArtistAlbumDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/search.db"

	// Simulate a database file created before artist/album existed, by
	// opening it with the old 3-column schema directly.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := db.Exec(`CREATE VIRTUAL TABLE urls USING fts5(url, title, tags);`); err != nil {
		t.Fatalf("create old-schema table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO urls (url, title, tags) VALUES (?, ?, ?)`,
		"https://example.com/a", "Dreams", "classic"); err != nil {
		t.Fatalf("insert into old-schema table: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	// Open() should migrate this in place rather than erroring.
	idx, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() on a pre-existing old-schema database: %v, want nil", err)
	}
	defer idx.Close()

	if err := idx.IndexURL("https://example.com/b", "New", "Someone", "Some Album", ""); err != nil {
		t.Fatalf("IndexURL() after migration: %v, want nil", err)
	}

	got, err := idx.List(0, 0)
	if err != nil {
		t.Fatalf("List() after migration: %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("List() len = %d, want 2 (old row preserved + new row)", len(got))
	}
}

func TestGetFindsExactURLWithoutFTSMatching(t *testing.T) {
	idx, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	defer idx.Close()

	if err := idx.IndexURL("https://example.com/a", "Dreams", "Fleetwood Mac", "Rumours", ""); err != nil {
		t.Fatalf("IndexURL() error = %v, want nil", err)
	}

	got, ok, err := idx.Get("https://example.com/a")
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if got.Title != "Dreams" || got.Artist != "Fleetwood Mac" || got.Album != "Rumours" {
		t.Errorf("Get() = %+v, want the indexed entry", got)
	}
}

func TestGetReportsNotFoundForAnUnindexedURL(t *testing.T) {
	idx, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	defer idx.Close()

	_, ok, err := idx.Get("https://example.com/never-indexed")
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}
	if ok {
		t.Error("Get() ok = true, want false for an unindexed URL")
	}
}

func TestOpenCreatesTable(t *testing.T) {
	idx, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	defer idx.Close()

	// Opening again against a fresh in-memory DB and indexing/searching should
	// just work, confirming the FTS5 virtual table was created.
	if err := idx.IndexURL("https://example.com", "Hello World", "", "", ""); err != nil {
		t.Fatalf("IndexURL() error = %v, want nil", err)
	}
	got, err := idx.Search("Hello", 0)
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}
	if len(got) != 1 {
		t.Fatalf("Search() len = %d, want 1", len(got))
	}
}

func TestList(t *testing.T) {
	idx, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	defer idx.Close()

	if err := idx.IndexURL("https://example.com/a", "A", "", "", ""); err != nil {
		t.Fatalf("IndexURL(a) error = %v, want nil", err)
	}
	if err := idx.IndexURL("https://example.com/b", "B", "", "", ""); err != nil {
		t.Fatalf("IndexURL(b) error = %v, want nil", err)
	}
	if err := idx.IndexURL("https://example.com/c", "C", "", "", ""); err != nil {
		t.Fatalf("IndexURL(c) error = %v, want nil", err)
	}

	t.Run("lists everything most-recently-indexed first, no query needed", func(t *testing.T) {
		got, err := idx.List(0, 0)
		if err != nil {
			t.Fatalf("List() error = %v, want nil", err)
		}
		if len(got) != 3 {
			t.Fatalf("List() len = %d, want 3 (%v)", len(got), got)
		}
		if got[0].URL != "https://example.com/c" || got[2].URL != "https://example.com/a" {
			t.Errorf("List() = %v, want c, b, a in that order", got)
		}
	})

	t.Run("limit and offset paginate", func(t *testing.T) {
		got, err := idx.List(1, 1)
		if err != nil {
			t.Fatalf("List() error = %v, want nil", err)
		}
		if len(got) != 1 || got[0].URL != "https://example.com/b" {
			t.Errorf("List(1, 1) = %v, want just b", got)
		}
	})

	t.Run("re-indexing does not duplicate or reorder unexpectedly", func(t *testing.T) {
		if err := idx.IndexURL("https://example.com/a", "A updated", "", "", ""); err != nil {
			t.Fatalf("re-IndexURL(a) error = %v, want nil", err)
		}
		got, err := idx.List(0, 0)
		if err != nil {
			t.Fatalf("List() error = %v, want nil", err)
		}
		if len(got) != 3 {
			t.Fatalf("List() len = %d, want 3 (still 3 unique URLs) (%v)", len(got), got)
		}
		if got[0].URL != "https://example.com/a" || got[0].Title != "A updated" {
			t.Errorf("List()[0] = %+v, want the re-indexed a to be most recent", got[0])
		}
	})
}
