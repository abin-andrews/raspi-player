package mpdclient

import "github.com/fhs/gompd/v2/mpd"

// StatusWatcher wraps an mpd idle connection, delivering subsystem-change
// notifications so callers can push status updates without polling. It's
// the only supported way to observe mpd idle events outside this package,
// since mpdclient is the only package allowed to talk to mpd directly.
type StatusWatcher struct {
	w *mpd.Watcher
}

// NewStatusWatcher connects to the mpd server at addr over network and
// watches the given subsystems (e.g. "player", "mixer") for changes. If no
// subsystems are given, all changes are reported.
func NewStatusWatcher(network, addr string, subsystems ...string) (*StatusWatcher, error) {
	w, err := mpd.NewWatcher(network, addr, "", subsystems...)
	if err != nil {
		return nil, err
	}
	return &StatusWatcher{w: w}, nil
}

// Events returns the channel on which changed subsystem names are
// delivered.
func (sw *StatusWatcher) Events() <-chan string {
	return sw.w.Event
}

// Close closes the watcher's Event/Error channels and its mpd connection.
func (sw *StatusWatcher) Close() error {
	return sw.w.Close()
}
