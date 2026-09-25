// Package serial is a thin, testable transport for talking to the Arduino
// OLED controller (see arduino/control.ino) over its USB serial connection:
// one newline-terminated command per line, acknowledged with a reply line.
// This package knows nothing about the OLED's own command vocabulary
// (BEGIN/TITLE/.../END) — that's the caller's concern; this just gets bytes
// to the device and a reply line back reliably.
package serial

import (
	"bytes"
	"fmt"
	"sync"
	"time"

	"go.bug.st/serial"
)

const (
	// chunkSize mirrors arduino/test.py: writing in small pieces rather
	// than the whole line at once avoids overrunning the Uno's small
	// hardware RX buffer.
	chunkSize = 12
	chunkDelay = 40 * time.Millisecond
	// replyTimeout is the overall deadline for a command's reply line.
	replyTimeout = 3 * time.Second
	// pollTimeout is how long a single Read waits before we check the
	// overall deadline again.
	pollTimeout = 100 * time.Millisecond
	// resetDelay accounts for a classic Uno resetting when its serial port
	// is opened; sending before the sketch is back up would be dropped.
	resetDelay = 2500 * time.Millisecond
)

// port is the subset of go.bug.st/serial.Port this package needs; narrowing
// it lets tests supply an in-memory fake instead of a real serial port.
type port interface {
	Write(p []byte) (int, error)
	Read(p []byte) (int, error)
	SetReadTimeout(t time.Duration) error
	Close() error
}

// Client sends line-based commands and waits for a reply line. Safe for
// concurrent use — one command's write-then-reply is atomic with respect to
// any other, so callers don't need their own locking.
type Client struct {
	mu   sync.Mutex
	port port
}

// Open opens the named serial port (e.g. "/dev/ttyACM0") at the given baud
// rate. Opening a classic Uno's serial port resets it, so Open waits for the
// bootloader/sketch to come back up before returning, matching arduino/test.py.
func Open(name string, baud int) (*Client, error) {
	p, err := serial.Open(name, &serial.Mode{BaudRate: baud})
	if err != nil {
		return nil, fmt.Errorf("open serial port %s: %w", name, err)
	}
	time.Sleep(resetDelay)
	if err := p.ResetInputBuffer(); err != nil {
		p.Close()
		return nil, fmt.Errorf("reset input buffer on %s: %w", name, err)
	}
	return &Client{port: p}, nil
}

// Send writes line (with a newline appended), chunked to avoid overrunning
// the Uno's RX buffer, then reads back and returns one trimmed reply line.
func (c *Client) Send(line string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	payload := append([]byte(line), '\n')
	for offset := 0; offset < len(payload); offset += chunkSize {
		end := offset + chunkSize
		if end > len(payload) {
			end = len(payload)
		}
		if _, err := c.port.Write(payload[offset:end]); err != nil {
			return "", fmt.Errorf("write serial command: %w", err)
		}
		time.Sleep(chunkDelay)
	}

	return c.readLine(line)
}

// readLine polls the port for up to replyTimeout, since a timed-out Read on
// go.bug.st/serial returns (0, nil) rather than an error — a tight
// zero-byte-read loop would otherwise never terminate on a silent device.
// sent is only used to make a timeout error message more useful.
func (c *Client) readLine(sent string) (string, error) {
	if err := c.port.SetReadTimeout(pollTimeout); err != nil {
		return "", fmt.Errorf("set serial read timeout: %w", err)
	}
	var buf []byte
	tmp := make([]byte, 64)
	deadline := time.Now().Add(replyTimeout)
	for {
		n, err := c.port.Read(tmp)
		if err != nil {
			return "", fmt.Errorf("read serial reply: %w", err)
		}
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if idx := bytes.IndexByte(buf, '\n'); idx >= 0 {
				return string(bytes.TrimSpace(buf[:idx])), nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("serial reply timeout waiting for %q", sent)
		}
	}
}

// Close closes the underlying port.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.port.Close()
}
