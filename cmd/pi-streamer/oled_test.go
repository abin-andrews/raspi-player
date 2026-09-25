package main

import (
	"strings"
	"testing"
)

func TestOledTruncateLeavesShortStringsAlone(t *testing.T) {
	if got := oledTruncate("Dreams", oledTitleMax); got != "Dreams" {
		t.Errorf("oledTruncate = %q, want unchanged", got)
	}
}

func TestOledTruncateCapsLongStrings(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := oledTruncate(long, oledTitleMax)
	if len([]rune(got)) != oledTitleMax {
		t.Errorf("len(oledTruncate(...)) = %d, want %d", len([]rune(got)), oledTitleMax)
	}
}

func TestOledTruncateIsRuneSafe(t *testing.T) {
	// Multi-byte runes: truncating must not split one in half and produce
	// invalid UTF-8 (even though the display itself only targets ASCII).
	s := strings.Repeat("é", 200) // each 'é' is 2 bytes in UTF-8
	got := oledTruncate(s, 10)
	if len([]rune(got)) != 10 {
		t.Errorf("rune count = %d, want 10", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "é") {
		t.Errorf("oledTruncate produced %q, want it to end on a whole rune", got)
	}
}

func TestTruncateOLEDLineLeavesShortLinesAlone(t *testing.T) {
	parts := []string{"TITLE", "Dreams"}
	got := truncateOLEDLine(parts)
	if len(got) != 2 || got[0] != "TITLE" || got[1] != "Dreams" {
		t.Errorf("truncateOLEDLine(%v) = %v, want unchanged", parts, got)
	}
}

func TestTruncateOLEDLineFitsTheWireLimit(t *testing.T) {
	// A fallback title built from a full URL, as happened in practice —
	// long enough to blow past maxOLEDLineBytes even after updateOLEDTrack's
	// own oledTitleMax cap, simulating a value this package didn't
	// explicitly truncate (the scenario this backstop exists for).
	longURL := "https://archive.org/download/90s-evergreen-malayalam-film-songs/" + strings.Repeat("x", 100)
	parts := []string{"TITLE", longURL}

	got := truncateOLEDLine(parts)
	line := strings.Join(got, "\t")
	if len(line) > maxOLEDLineBytes {
		t.Errorf("truncated line is %d bytes, want <= %d: %q", len(line), maxOLEDLineBytes, line)
	}
	if got[0] != "TITLE" {
		t.Errorf("command name = %q, want it untouched", got[0])
	}
}

func TestTruncateOLEDLineNeverProducesNegativeLength(t *testing.T) {
	// A pathological case: even the command name alone overflows the
	// budget. The value should end up empty, not panic on a negative slice
	// bound.
	parts := []string{strings.Repeat("CMD", 40), "value"}
	got := truncateOLEDLine(parts)
	if len(got[1]) != 0 {
		t.Errorf("value = %q, want empty when even the command name overflows the budget", got[1])
	}
}
