package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenMissingFileYieldsZeroConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := s.Get(); got != (Config{}) {
		t.Errorf("Get() = %+v, want zero value", got)
	}
}

func TestSetPersistsAndGetReflectsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	want := Config{OLED: OLED{Port: "/dev/ttyACM0", Baud: 115200}}
	if err := s.Set(want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := s.Get(); got != want {
		t.Errorf("Get() = %+v, want %+v", got, want)
	}
}

func TestSetIsVisibleToANewStoreOnTheSamePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s1, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	want := Config{OLED: OLED{Port: "/dev/ttyUSB0", Baud: 9600}}
	if err := s1.Set(want); err != nil {
		t.Fatalf("Set: %v", err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if got := s2.Get(); got != want {
		t.Errorf("second Store's Get() = %+v, want %+v", got, want)
	}
}

func TestReloadPicksUpAnExternalEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	original := Config{OLED: OLED{Port: "/dev/ttyACM0", Baud: 115200}}
	if err := s.Set(original); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Simulate a hand-edit made directly on disk (e.g. over SSH) rather
	// than through Set.
	edited := Config{OLED: OLED{Port: "/dev/ttyACM1", Baud: 57600}}
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if err := s2.Set(edited); err != nil {
		t.Fatalf("Set on second store: %v", err)
	}

	if err := s.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := s.Get(); got != edited {
		t.Errorf("after Reload, Get() = %+v, want %+v", got, edited)
	}
}

func TestIsAllowedBaud(t *testing.T) {
	if !IsAllowedBaud(115200) {
		t.Error("IsAllowedBaud(115200) = false, want true")
	}
	if IsAllowedBaud(115201) {
		t.Error("IsAllowedBaud(115201) = true, want false")
	}
	if IsAllowedBaud(0) {
		t.Error("IsAllowedBaud(0) = true, want false")
	}
}

func TestIsAllowedMode(t *testing.T) {
	if !IsAllowedMode(ModeStream) {
		t.Error("IsAllowedMode(ModeStream) = false, want true")
	}
	if !IsAllowedMode(ModeBucket) {
		t.Error("IsAllowedMode(ModeBucket) = false, want true")
	}
	if IsAllowedMode("") {
		t.Error(`IsAllowedMode("") = true, want false`)
	}
	if IsAllowedMode("bogus") {
		t.Error(`IsAllowedMode("bogus") = true, want false`)
	}
}

func TestReloadOnDeletedFileResetsToZeroValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Set(Config{OLED: OLED{Port: "/dev/ttyACM0", Baud: 115200}}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove config file: %v", err)
	}

	// Reload should not error just because the file happens not to exist
	// (e.g. it was removed) — it's the same as never having configured
	// anything.
	if err := s.Reload(); err != nil {
		t.Fatalf("Reload after removing the file: %v", err)
	}
	if got := s.Get(); got != (Config{}) {
		t.Errorf("Get() after Reload = %+v, want zero value", got)
	}
}
