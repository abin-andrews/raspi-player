package artstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "art")
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("Open() did not create %q as a directory", dir)
	}
	if s.Dir() != dir {
		t.Errorf("Dir() = %q, want %q", s.Dir(), dir)
	}
}

func TestOpenResolvesRelativeDirToAbsolute(t *testing.T) {
	tmp := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldWD)
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}

	s, err := Open("relative-art-dir")
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	if !filepath.IsAbs(s.Dir()) {
		t.Errorf("Dir() = %q, want an absolute path", s.Dir())
	}
}

func TestLookupUnknownURL(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if _, _, known := s.Lookup("https://example.com/never-seen.mp3"); known {
		t.Error("Lookup() known = true, want false for a url never Put")
	}
}

func TestPutThenLookupPositive(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	jpegLike := []byte("\xff\xd8\xff\xe0fakejpegbytes")
	name, hasArt, err := s.Put("https://example.com/a.mp3", jpegLike)
	if err != nil {
		t.Fatalf("Put() error = %v, want nil", err)
	}
	if !hasArt || name == "" {
		t.Fatalf("Put() = %q, %v, want a non-empty filename and hasArt=true", name, hasArt)
	}

	gotName, gotHasArt, known := s.Lookup("https://example.com/a.mp3")
	if !known || !gotHasArt || gotName != name {
		t.Errorf("Lookup() = %q, %v, %v, want %q, true, true", gotName, gotHasArt, known, name)
	}

	// The file should actually exist on disk under Dir().
	if _, err := os.Stat(filepath.Join(s.Dir(), name)); err != nil {
		t.Errorf("Put() didn't write a file at %q: %v", filepath.Join(s.Dir(), name), err)
	}
}

func TestPutWithEmptyDataRecordsConfirmedNoArt(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	name, hasArt, err := s.Put("https://example.com/no-art.mp3", nil)
	if err != nil {
		t.Fatalf("Put() error = %v, want nil", err)
	}
	if hasArt || name != "" {
		t.Errorf("Put(nil) = %q, %v, want empty filename and hasArt=false", name, hasArt)
	}

	gotName, gotHasArt, known := s.Lookup("https://example.com/no-art.mp3")
	if !known || gotHasArt || gotName != "" {
		t.Errorf("Lookup() = %q, %v, %v, want \"\", false, true", gotName, gotHasArt, known)
	}
}

func TestExtensionMatchesDetectedContentType(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	pngLike := []byte("\x89PNG\r\n\x1a\nrestofpngbytes")
	name, _, err := s.Put("https://example.com/cover.mp3", pngLike)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(name) != ".png" {
		t.Errorf("filename = %q, want a .png extension for PNG-sniffed data", name)
	}
}

func TestIndexPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s1.Put("https://example.com/a.mp3", []byte("\xff\xd8\xff\xe0jpeg")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s1.Put("https://example.com/no-art.mp3", nil); err != nil {
		t.Fatal(err)
	}

	// Simulate a daemon restart: open a fresh Store over the same
	// directory and confirm both the positive and negative results
	// survived without needing to re-fetch anything.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	name, hasArt, known := s2.Lookup("https://example.com/a.mp3")
	if !known || !hasArt || name == "" {
		t.Errorf("Lookup(a.mp3) after reopen = %q, %v, %v, want a filename, true, true", name, hasArt, known)
	}
	if _, err := os.Stat(filepath.Join(s2.Dir(), name)); err != nil {
		t.Errorf("art file missing after reopen: %v", err)
	}

	_, hasArt, known = s2.Lookup("https://example.com/no-art.mp3")
	if !known || hasArt {
		t.Errorf("Lookup(no-art.mp3) after reopen = %v, %v, want false, true", hasArt, known)
	}
}

func TestPutOverwritesExistingEntry(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.Put("https://example.com/a.mp3", nil); err != nil {
		t.Fatal(err)
	}
	// Re-resolving later finds real art after all (e.g. the file was
	// re-tagged) — Put should just overwrite the earlier negative result.
	name, hasArt, err := s.Put("https://example.com/a.mp3", []byte("\xff\xd8\xff\xe0jpeg"))
	if err != nil {
		t.Fatal(err)
	}
	if !hasArt || name == "" {
		t.Fatalf("second Put() = %q, %v, want a filename and hasArt=true", name, hasArt)
	}

	gotName, gotHasArt, known := s.Lookup("https://example.com/a.mp3")
	if !known || !gotHasArt || gotName != name {
		t.Errorf("Lookup() after overwrite = %q, %v, %v, want %q, true, true", gotName, gotHasArt, known, name)
	}
}
