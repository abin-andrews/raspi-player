package player

import "testing"

func TestDeriveTitleFromURLCleansUpArchiveOrgStyleURL(t *testing.T) {
	got := deriveTitleFromURL("https://archive.org/download/90s-evergreen-malayalam-film-songs/02-Chandamaama%20%28D%29.mp3")
	want := "02 Chandamaama (D)"
	if got != want {
		t.Errorf("deriveTitleFromURL = %q, want %q", got, want)
	}
}

func TestDeriveTitleFromURLIsIdempotentForPlainTitles(t *testing.T) {
	// Already a real title (e.g. mpd's own Song, Title-preferred) — must
	// pass through unchanged, not get mangled by URL-shaped assumptions.
	if got := deriveTitleFromURL("Dreams"); got != "Dreams" {
		t.Errorf("deriveTitleFromURL(%q) = %q, want unchanged", "Dreams", got)
	}
}

func TestDeriveTitleFromURLHandlesEmptyString(t *testing.T) {
	if got := deriveTitleFromURL(""); got != "" {
		t.Errorf("deriveTitleFromURL(\"\") = %q, want empty", got)
	}
}

func TestDeriveTitleFromURLReplacesSeparators(t *testing.T) {
	got := deriveTitleFromURL("http://example.com/My_Cool-Track.mp3")
	want := "My Cool Track"
	if got != want {
		t.Errorf("deriveTitleFromURL = %q, want %q", got, want)
	}
}

func TestDeriveTitleFromURLFallsBackToRawURLWhenUnparseable(t *testing.T) {
	// A value with characters invalid in a URL — url.Parse fails, and the
	// whole raw value should come back rather than an empty/panicking result.
	raw := "://not a url"
	if got := deriveTitleFromURL(raw); got != raw {
		t.Errorf("deriveTitleFromURL(%q) = %q, want the raw value unchanged", raw, got)
	}
}
