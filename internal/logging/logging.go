// Package logging redirects the standard "log" package's output (used
// throughout this codebase for all console/status output) to a rotating
// file, since the desktop tray build and the Android WebView build have no
// console for the user to see it in. The file is rotated once it reaches
// 100MB or 1 day old, whichever comes first; rotation discards the old
// file rather than keeping it around.
package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LogFileName is the current (not yet rotated) log file's name, inside the
// directory passed to Setup.
const LogFileName = "foilen-box.log"

const metaFileName = "foilen-box.log.meta"

const (
	maxSize = 100 * 1024 * 1024 // 100MB
	maxAge  = 24 * time.Hour
)

// current is the writer installed by Setup, kept around so Clear can reach
// it (the standard "log" package only exposes the writer for output, not for
// operations like truncation).
var current *rotatingWriter

// Setup points the standard logger at a rotating file inside dir, creating
// dir if needed, and clears any existing log file so each process start
// begins with an empty log. It's meant to be called once, as early as
// possible, by each entry point (desktop, Android) before any other package
// logs anything.
func Setup(dir string) error {
	w, err := newRotatingWriter(dir)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	log.SetOutput(w)
	// Set explicitly (rather than relying on the "log" package's own
	// default) so a date/time prefix on every line doesn't depend on no
	// dependency ever having called log.SetFlags on the shared standard
	// logger before Setup runs.
	log.SetFlags(log.Ldate | log.Ltime)
	current = w
	return nil
}

// Clear empties the current log file in place, same as a rotation but
// without waiting for the size/age threshold — used by the web UI's Logs
// tab "Clear" button. No-op if Setup hasn't been called yet.
func Clear() error {
	if current == nil {
		return nil
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	return current.rotate()
}

// rotatingWriter is an io.Writer appending to LogFileName inside dir. Once a
// write would push the file past maxSize, or the file has been open longer
// than maxAge, it's deleted and a fresh one is started.
type rotatingWriter struct {
	mu       sync.Mutex
	dir      string
	file     *os.File
	size     int64
	openedAt time.Time
}

func newRotatingWriter(dir string) (*rotatingWriter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	w := &rotatingWriter{dir: dir}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) logPath() string  { return filepath.Join(w.dir, LogFileName) }
func (w *rotatingWriter) metaPath() string { return filepath.Join(w.dir, metaFileName) }

func (w *rotatingWriter) open() error {
	f, err := os.OpenFile(w.logPath(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.file = f
	w.size = info.Size()
	w.openedAt = w.readOrInitOpenedAt()
	return nil
}

// readOrInitOpenedAt returns when the current log file was first created,
// persisted in a sidecar file next to it so age-based rotation survives
// process restarts (a file's OS-level creation time isn't reliably
// available cross-platform, and its mtime changes on every append). If the
// sidecar is missing or unreadable, it's (re)stamped with now.
func (w *rotatingWriter) readOrInitOpenedAt() time.Time {
	if data, err := os.ReadFile(w.metaPath()); err == nil {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data))); err == nil {
			return t
		}
	}
	now := time.Now()
	_ = os.WriteFile(w.metaPath(), []byte(now.Format(time.RFC3339)), 0o644)
	return now
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.size+int64(len(p)) > maxSize || time.Since(w.openedAt) > maxAge {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// maxTailBytes caps how much of the current log file ReadTail returns, so
// the web UI never has to load/render the full (up to 100MB) file.
const maxTailBytes = 512 * 1024

// ReadTail returns up to the last maxTailBytes of the current (not yet
// rotated) log file inside dir, for display in the web UI's Logs tab. If
// search is non-empty, only lines containing it (case-insensitive) are kept.
func ReadTail(dir string, search string) (string, error) {
	f, err := os.Open(filepath.Join(dir, LogFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", err
	}

	size := info.Size()
	offset := int64(0)
	truncated := false
	if size > maxTailBytes {
		offset = size - maxTailBytes
		truncated = true
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}

	text := string(data)
	if search != "" {
		text = grepLines(text, search)
	}
	if truncated {
		return "... (truncated) ...\n" + text, nil
	}
	return text, nil
}

// grepLines returns only the lines of text that contain search, matched
// case-insensitively.
func grepLines(text string, search string) string {
	search = strings.ToLower(search)
	lines := strings.Split(text, "\n")
	matched := lines[:0]
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), search) {
			matched = append(matched, line)
		}
	}
	return strings.Join(matched, "\n")
}

func (w *rotatingWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	if err := os.Remove(w.logPath()); err != nil {
		return err
	}
	_ = os.Remove(w.metaPath())
	return w.open()
}
