// The daemon's OLED integration: a live-reconnectable connection manager
// for the Arduino display (see arduino/control.ino), plus the
// protocol-specific line building on top of internal/serial's generic
// transport. Kept out of internal/serial deliberately — that package knows
// nothing about any particular device's command vocabulary.
package main

import (
	"log"
	"strconv"
	"strings"
	"sync"

	"pi-streamer/internal/api"
	"pi-streamer/internal/bucket"
	"pi-streamer/internal/config"
	"pi-streamer/internal/mpdclient"
	"pi-streamer/internal/serial"
)

// oledManager owns the (possibly absent) live serial connection to the
// Arduino display, so it can be opened/closed/replaced at runtime — via the
// web UI's /api/config endpoints — instead of only once at startup.
type oledManager struct {
	mu      sync.Mutex
	client  *serial.Client
	port    string
	baud    int
	lastErr error
}

// reconfigure closes any existing connection and opens port/baud instead.
// An empty port just disconnects (display support turned off).
func (m *oledManager) reconfigure(port string, baud int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.client != nil {
		m.client.Close()
		m.client = nil
	}
	m.port, m.baud = port, baud
	m.lastErr = nil
	if port == "" {
		return
	}
	c, err := serial.Open(port, baud)
	if err != nil {
		m.lastErr = err
		log.Printf("oled connect to %s: %v", port, err)
		return
	}
	m.client = c
}

// send writes one protocol line if currently connected; it's a no-op
// otherwise. Every attempt is logged (line out, reply in) so `make dev`'s
// console doubles as a live view of OLED traffic during testing. A send
// failure (e.g. the Arduino was unplugged) drops the connection so Status()
// reflects reality instead of silently retrying a dead port on every update.
func (m *oledManager) send(parts ...string) {
	m.mu.Lock()
	c := m.client
	m.mu.Unlock()
	if c == nil {
		return
	}
	parts = truncateOLEDLine(parts)
	line := strings.Join(parts, "\t")
	log.Printf("oled -> %s", line)
	reply, err := c.Send(line)
	if err != nil {
		log.Printf("oled send %q: %v", parts[0], err)
		m.mu.Lock()
		if m.client == c {
			m.client = nil
			m.lastErr = err
		}
		m.mu.Unlock()
		return
	}
	log.Printf("oled <- %s", reply)
	if reply != "OK" {
		log.Printf("oled rejected %q: %s", parts[0], reply)
	}
}

// Status implements internal/api.Oled.
func (m *oledManager) Status() api.OledStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := api.OledStatus{Connected: m.client != nil, Port: m.port, Baud: m.baud}
	if m.lastErr != nil {
		st.Error = m.lastErr.Error()
	}
	return st
}

// ListPorts implements internal/api.Oled.
func (m *oledManager) ListPorts() ([]string, error) {
	return serial.ListPorts()
}

func (m *oledManager) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client != nil {
		m.client.Close()
		m.client = nil
	}
}

// configAdapter bridges the on-disk config.Store to internal/api.Config,
// applying every live-reconfigurable subsystem's settings (OLED
// port/baud, bucket/favorites size caps and safety margin) whenever the
// config changes (through Set) or is reloaded from disk — so editing it
// through the web UI or hand-editing the file both take effect without a
// daemon restart.
type configAdapter struct {
	store     *config.Store
	oled      *oledManager
	cache     *bucket.Store // evictable playback cache
	favorites *bucket.Store // permanent favorites archive
	// getStatus fetches the current mpd status, so a fresh OLED connection
	// syncs the display immediately instead of sitting on the sketch's
	// boot-time placeholder values until the next unrelated status change.
	getStatus func() (mpdclient.Status, error)
}

func (a *configAdapter) Get() config.Config { return a.store.Get() }

func (a *configAdapter) Set(cfg config.Config) error {
	if err := a.store.Set(cfg); err != nil {
		return err
	}
	a.apply(cfg)
	return nil
}

func (a *configAdapter) Reload() error {
	if err := a.store.Reload(); err != nil {
		return err
	}
	a.apply(a.store.Get())
	return nil
}

func (a *configAdapter) apply(cfg config.Config) {
	a.applyOLED(cfg)
	a.applyBucket(cfg)
}

func (a *configAdapter) applyOLED(cfg config.Config) {
	baud := cfg.OLED.Baud
	if baud == 0 {
		baud = 115200
	}
	a.oled.reconfigure(cfg.OLED.Port, baud)
	if !a.oled.Status().Connected {
		return
	}
	status, err := a.getStatus()
	if err != nil {
		log.Printf("oled initial sync: fetch status: %v", err)
		return
	}
	updateOLEDTrack(a.oled, status)
}

// applyBucket pushes the configured size caps/safety margin into both the
// playback cache and the favorites archive live — SetMaxSize/SetMinFree
// don't evict anything themselves; a lowered cap just takes effect on the
// bucket's next Download.
func (a *configAdapter) applyBucket(cfg config.Config) {
	bucketMaxMB := cfg.Bucket.MaxSizeMB
	if bucketMaxMB == 0 {
		bucketMaxMB = config.DefaultBucketMaxSizeMB
	}
	favMaxMB := cfg.Bucket.FavoritesMaxSizeMB
	if favMaxMB == 0 {
		favMaxMB = config.DefaultFavoritesMaxSizeMB
	}
	minFreeMB := cfg.Bucket.MinFreeMB
	if minFreeMB == 0 {
		minFreeMB = config.DefaultMinFreeMB
	}

	const mb = 1024 * 1024
	a.cache.SetMaxSize(int64(bucketMaxMB) * mb)
	a.cache.SetMinFree(int64(minFreeMB) * mb)
	a.favorites.SetMaxSize(int64(favMaxMB) * mb)
	a.favorites.SetMinFree(int64(minFreeMB) * mb)
}

// oledState maps mpd's player state to arduino/control.ino's STATE enum.
func oledState(mpdState string) string {
	switch mpdState {
	case "play":
		return "PLAYING"
	case "pause":
		return "PAUSED"
	default:
		return "STOPPED"
	}
}

// oledSanitize replaces the OLED protocol's own delimiters (tab, newline) so
// a metadata value can never be mistaken for a field boundary —
// arduino/control.ino documents this as the host's responsibility.
func oledSanitize(s string) string {
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.ReplaceAll(s, "\n", " ")
}

// arduino/control.ino's fixed-size buffers: TITLE_CAP/TEXT_CAP each include
// space for a null terminator, so usable content is one less. oledTruncate
// caps a value to fit *before* sending, rather than relying on the
// firmware's own copyText to cut it off: a value that fits in the buffer
// but not within RX_CAP (the whole line's wire limit — see collectSerial's
// discardingLine handling) doesn't get gracefully shortened, the entire
// line is discarded and the field never updates at all (logged as "ERR").
// This matters in practice: a fallback title (Status.Title empty, so
// Status.Song — often a full URL — is sent instead) can easily run past
// either limit.
const (
	oledTitleMax = 47 // TITLE_CAP - 1
	oledTextMax  = 39 // TEXT_CAP - 1 (artist/album)
)

func oledTruncate(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen])
}

// maxOLEDLineBytes is the wire limit for one full command line (command +
// tab-separated args), leaving room for the newline internal/serial.Client
// appends and RX_CAP's own off-by-one — arduino/control.ino's
// collectSerial discards (not gracefully shortens) anything longer,
// replying ERR and leaving that field unchanged.
const maxOLEDLineBytes = 60

// truncateOLEDLine is a backstop beyond updateOLEDTrack's per-field
// oledTruncate calls: it catches any value — current or future field,
// including ones this package doesn't explicitly cap — that's still too
// long once joined with its command name. mpd-derived metadata is
// effectively unbounded (e.g. a fallback title built from a full URL, as
// happened in practice), so send always checks the assembled line itself
// rather than trusting every call site to have capped its own value.
func truncateOLEDLine(parts []string) []string {
	if len(parts) == 0 {
		return parts
	}
	line := strings.Join(parts, "\t")
	if len(line) <= maxOLEDLineBytes {
		return parts
	}
	overflow := len(line) - maxOLEDLineBytes
	last := len(parts) - 1
	value := []rune(parts[last])
	keep := len(value) - overflow
	if keep < 0 {
		keep = 0
	}
	out := append([]string{}, parts...)
	out[last] = string(value[:keep])
	log.Printf("oled: %q value truncated to fit the wire limit", parts[0])
	return out
}

// updateOLEDTrack pushes a full batched update — BEGIN/.../END so the
// display redraws only once, after every field has landed, instead of
// flashing an in-between frame.
func updateOLEDTrack(m *oledManager, status mpdclient.Status) {
	title := status.Title
	if title == "" {
		title = status.Song
	}
	m.send("BEGIN")
	m.send("TITLE", oledSanitize(oledTruncate(title, oledTitleMax)))
	m.send("ARTIST", oledSanitize(oledTruncate(status.Artist, oledTextMax)))
	m.send("ALBUM", oledSanitize(oledTruncate(status.Album, oledTextMax)))
	m.send("DURATION", strconv.Itoa(int(status.Duration)))
	m.send("TIME", strconv.Itoa(int(status.Elapsed)))
	m.send("STATE", oledState(status.State))
	m.send("END")
}

// updateOLEDElapsed pushes just the elapsed-time field, for the once-a-
// second progress tick where nothing else about the track changed —
// cheaper than a full BEGIN/END batch for every tick.
func updateOLEDElapsed(m *oledManager, elapsedSeconds float64) {
	m.send("TIME", strconv.Itoa(int(elapsedSeconds)))
}
