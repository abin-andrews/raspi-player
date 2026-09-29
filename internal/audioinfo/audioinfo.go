// Package audioinfo reads technical audio properties (codec, sample rate,
// channels, bit depth, bitrate, duration) directly from a local file by
// shelling out to ffprobe — the same tool this project already requires
// installed alongside ffmpeg (see internal/ytdlp's YouTube extraction and
// this project's install-deps targets), never vendored or reimplemented.
// This only works against a local file path, not a remote URL — it's
// meant for the daemon's own cached copies (internal/bucket), which are
// real files on disk, not the mpd-side tag reading internal/metadata does
// against a ranged HTTP GET.
package audioinfo

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
)

// Info is what one file's audio stream reports. Fields are zero-valued
// (and, at the API layer, omitted from JSON) when ffprobe couldn't
// determine them — a lossy codec has no meaningful BitsPerSample, for
// instance, and ffprobe simply doesn't report one for those.
type Info struct {
	Codec           string
	SampleRateHz    int
	Channels        int
	BitsPerSample   int
	BitrateKbps     int
	DurationSeconds float64
}

// ffprobeOutput mirrors just the fields this package reads from
// `ffprobe -print_format json -show_format -show_streams`.
type ffprobeOutput struct {
	Streams []struct {
		CodecType     string `json:"codec_type"`
		CodecName     string `json:"codec_name"`
		SampleRate    string `json:"sample_rate"`
		Channels      int    `json:"channels"`
		BitsPerSample int    `json:"bits_per_sample"`
		// BitsPerRawSample is what FLAC/WAV report instead of
		// bits_per_sample, which ffprobe often leaves at 0 for them.
		BitsPerRawSample string `json:"bits_per_raw_sample"`
		BitRate          string `json:"bit_rate"`
	} `json:"streams"`
	Format struct {
		BitRate  string `json:"bit_rate"`
		Duration string `json:"duration"`
	} `json:"format"`
}

// Prober runs ffprobe as a subprocess. BinaryPath overrides the binary
// looked up on PATH (empty means "ffprobe", matching ytdlp.Extractor's
// identical -ytdlp-path pattern) — for tests, or a system where it's
// installed somewhere nonstandard.
type Prober struct {
	BinaryPath string
}

func (p *Prober) binary() string {
	if p.BinaryPath != "" {
		return p.BinaryPath
	}
	return "ffprobe"
}

// Probe reads path's first audio stream's technical properties. Returns
// an error only if ffprobe itself couldn't run or produced unparseable
// output — a file ffprobe genuinely can't make sense of still yields an
// error, since unlike a missing tag (internal/metadata's stance) there's
// no meaningful "confirmed nothing" answer for a corrupt/unrecognized
// audio file's technical properties.
func (p *Prober) Probe(ctx context.Context, path string) (Info, error) {
	cmd := exec.CommandContext(ctx, p.binary(),
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-select_streams", "a:0", // first audio stream only — plenty for a plain audio file
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		return Info{}, fmt.Errorf("audioinfo: run ffprobe: %w", err)
	}

	var probed ffprobeOutput
	if err := json.Unmarshal(out, &probed); err != nil {
		return Info{}, fmt.Errorf("audioinfo: parse ffprobe output: %w", err)
	}
	if len(probed.Streams) == 0 {
		return Info{}, fmt.Errorf("audioinfo: no audio stream found in %s", path)
	}
	stream := probed.Streams[0]

	info := Info{
		Codec:        stream.CodecName,
		SampleRateHz: atoiOr(stream.SampleRate, 0),
		Channels:     stream.Channels,
	}
	if stream.BitsPerSample > 0 {
		info.BitsPerSample = stream.BitsPerSample
	} else {
		info.BitsPerSample = atoiOr(stream.BitsPerRawSample, 0)
	}

	// Prefer the stream's own bitrate (more specific — the format-level
	// figure can include container overhead/other streams); fall back to
	// the format-level one, e.g. for containers that only report it there.
	bitRate := atoiOr(stream.BitRate, 0)
	if bitRate == 0 {
		bitRate = atoiOr(probed.Format.BitRate, 0)
	}
	info.BitrateKbps = bitRate / 1000

	info.DurationSeconds = atofOr(probed.Format.Duration, 0)

	return info, nil
}

func atoiOr(s string, fallback int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

func atofOr(s string, fallback float64) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fallback
	}
	return f
}
