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

const LogFileName = "foilen-box.log"

const metaFileName = "foilen-box.log.meta"

const (
	maxSize = 100 * 1024 * 1024
	maxAge  = 24 * time.Hour
)

var current *rotatingWriter

func Setup(dir string, clear bool) error {
	w, err := newRotatingWriter(dir, clear)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	log.SetOutput(w)
	log.SetFlags(log.Ldate | log.Ltime)
	current = w
	return nil
}

func Clear() error {
	if current == nil {
		return nil
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	return current.rotate()
}

type rotatingWriter struct {
	mu       sync.Mutex
	dir      string
	file     *os.File
	size     int64
	openedAt time.Time
}

func newRotatingWriter(dir string, clear bool) (*rotatingWriter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	w := &rotatingWriter{dir: dir}
	if err := w.open(clear); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) logPath() string  { return filepath.Join(w.dir, LogFileName) }
func (w *rotatingWriter) metaPath() string { return filepath.Join(w.dir, metaFileName) }

func (w *rotatingWriter) open(clear bool) error {
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if clear {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	f, err := os.OpenFile(w.logPath(), flags, 0o644)
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

const maxTailBytes = 512 * 1024

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
	return w.open(true)
}
