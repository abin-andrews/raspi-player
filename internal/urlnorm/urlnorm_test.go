package urlnorm

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{
			name: "lowercases scheme and host",
			in:   "HTTP://Example.COM/song.mp3",
			want: "http://example.com/song.mp3",
		},
		{
			name: "strips default http port",
			in:   "http://example.com:80/song.mp3",
			want: "http://example.com/song.mp3",
		},
		{
			name: "strips default https port",
			in:   "https://example.com:443/song.mp3",
			want: "https://example.com/song.mp3",
		},
		{
			name: "keeps non-default port",
			in:   "http://example.com:8080/song.mp3",
			want: "http://example.com:8080/song.mp3",
		},
		{
			name: "fills in empty path as root",
			in:   "http://example.com",
			want: "http://example.com/",
		},
		{
			name: "leaves path case alone",
			in:   "http://example.com/SoMe/PATH.mp3",
			want: "http://example.com/SoMe/PATH.mp3",
		},
		{
			name: "leaves query string alone",
			in:   "http://example.com/song.mp3?Token=AbC123",
			want: "http://example.com/song.mp3?Token=AbC123",
		},
		{
			name: "trims surrounding whitespace",
			in:   "  http://example.com/song.mp3  ",
			want: "http://example.com/song.mp3",
		},
		{
			name:    "empty string errors",
			in:      "   ",
			wantErr: true,
		},
		{
			name:    "missing host errors",
			in:      "not-a-url",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Normalize(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Normalize(%q) = %q, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Normalize(%q) error = %v, want nil", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeCollapsesEquivalentVariantsToTheSameKey(t *testing.T) {
	a, err := Normalize("HTTP://Example.com:80/track.mp3")
	if err != nil {
		t.Fatalf("Normalize a: %v", err)
	}
	b, err := Normalize("http://example.com/track.mp3")
	if err != nil {
		t.Fatalf("Normalize b: %v", err)
	}
	if a != b {
		t.Errorf("Normalize(%q) = %q, Normalize(%q) = %q, want equal", "HTTP://Example.com:80/track.mp3", a, "http://example.com/track.mp3", b)
	}
}
