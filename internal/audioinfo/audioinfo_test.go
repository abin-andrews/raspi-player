package audioinfo

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeFfprobeScript writes a shell script standing in for the real
// ffprobe binary — echoes stdout (a canned JSON response) and exits with
// exitCode, so Probe's own plumbing (running the command, parsing its
// output) is exercised without a real ffprobe install or a real media
// file at all.
func fakeFfprobeScript(t *testing.T, stdout string, exitCode int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake ffprobe script is a shell script; not run on windows")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "ffprobe")
	content := "#!/bin/sh\ncat <<'EOF'\n" + stdout + "\nEOF\nexit " + itoa(exitCode) + "\n"
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatalf("write fake ffprobe script: %v", err)
	}
	return script
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	if n == 1 {
		return "1"
	}
	panic("itoa: only 0/1 needed for these tests")
}

const mp3JSON = `{
  "streams": [
    {
      "codec_type": "audio",
      "codec_name": "mp3",
      "sample_rate": "44100",
      "channels": 2,
      "bit_rate": "320000"
    }
  ],
  "format": {
    "bit_rate": "323000",
    "duration": "215.481000"
  }
}`

const flacJSON = `{
  "streams": [
    {
      "codec_type": "audio",
      "codec_name": "flac",
      "sample_rate": "96000",
      "channels": 2,
      "bits_per_raw_sample": "24",
      "bit_rate": "0"
    }
  ],
  "format": {
    "bit_rate": "2304000",
    "duration": "180.0"
  }
}`

func TestProbeParsesMP3Stream(t *testing.T) {
	script := fakeFfprobeScript(t, mp3JSON, 0)
	p := &Prober{BinaryPath: script}

	info, err := p.Probe(context.Background(), "/does/not/matter.mp3")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	want := Info{Codec: "mp3", SampleRateHz: 44100, Channels: 2, BitrateKbps: 320, DurationSeconds: 215.481}
	if info != want {
		t.Errorf("Probe() = %+v, want %+v", info, want)
	}
}

func TestProbeFallsBackToBitsPerRawSampleAndFormatBitrate(t *testing.T) {
	script := fakeFfprobeScript(t, flacJSON, 0)
	p := &Prober{BinaryPath: script}

	info, err := p.Probe(context.Background(), "/does/not/matter.flac")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if info.BitsPerSample != 24 {
		t.Errorf("BitsPerSample = %d, want 24 (from bits_per_raw_sample)", info.BitsPerSample)
	}
	if info.BitrateKbps != 2304 {
		t.Errorf("BitrateKbps = %d, want 2304 (falling back to format.bit_rate)", info.BitrateKbps)
	}
}

func TestProbePropagatesFfprobeFailure(t *testing.T) {
	script := fakeFfprobeScript(t, "", 1)
	p := &Prober{BinaryPath: script}

	if _, err := p.Probe(context.Background(), "/does/not/matter.mp3"); err == nil {
		t.Error("Probe: want error when ffprobe exits non-zero, got nil")
	}
}

func TestProbeErrorsWithNoAudioStreams(t *testing.T) {
	script := fakeFfprobeScript(t, `{"streams": [], "format": {}}`, 0)
	p := &Prober{BinaryPath: script}

	if _, err := p.Probe(context.Background(), "/does/not/matter.mp3"); err == nil {
		t.Error("Probe: want error when ffprobe reports no audio streams, got nil")
	}
}
