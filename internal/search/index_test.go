package search

import "testing"

func TestIndexURL(t *testing.T) {
	t.Run("indexed url is found by a word in the title", func(t *testing.T) {
		idx, err := Open(":memory:")
		if err != nil {
			t.Fatalf("Open() error = %v, want nil", err)
		}
		defer idx.Close()

		if err := idx.IndexURL("https://example.com/a", "Raspberry Pi Setup Guide", "pi,setup"); err != nil {
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

		if err := idx.IndexURL("https://example.com/a", "Raspberry Pi Setup Guide", "pi,setup"); err != nil {
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

		if err := idx.IndexURL("https://example.com/a", "Old Title", "old"); err != nil {
			t.Fatalf("IndexURL() error = %v, want nil", err)
		}
		if err := idx.IndexURL("https://example.com/a", "New Title", "new"); err != nil {
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
			if err := idx.IndexURL(u.url, u.title, u.tags); err != nil {
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
			if err := idx.IndexURL(url, "Common Title Word", "tag"); err != nil {
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

func TestOpenCreatesTable(t *testing.T) {
	idx, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	defer idx.Close()

	// Opening again against a fresh in-memory DB and indexing/searching should
	// just work, confirming the FTS5 virtual table was created.
	if err := idx.IndexURL("https://example.com", "Hello World", ""); err != nil {
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
