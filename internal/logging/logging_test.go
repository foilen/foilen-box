package logging

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSetupWritesToFile(t *testing.T) {
	dir := t.TempDir()
	orig := log.Writer()
	defer log.SetOutput(orig)

	if err := Setup(dir); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	log.Print("hello world")

	data, err := os.ReadFile(filepath.Join(dir, LogFileName))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(data), "hello world") {
		t.Errorf("log file missing written line, got: %q", data)
	}
}

func TestRotatesOnSize(t *testing.T) {
	dir := t.TempDir()
	w, err := newRotatingWriter(dir)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}

	chunk := strings.Repeat("x", 1024)
	// Pretend the file is already right at the size limit so the next
	// write forces a rotation without actually writing 100MB in a test.
	w.size = maxSize

	if _, err := w.Write([]byte(chunk)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var current bool
	for _, e := range entries {
		if e.Name() == LogFileName {
			current = true
		} else if strings.HasPrefix(e.Name(), "foilen-box-") && strings.HasSuffix(e.Name(), ".log") {
			t.Errorf("expected old log file to be deleted on rotation, found %v", e.Name())
		}
	}
	if !current {
		t.Errorf("expected a fresh current log file in %v", entries)
	}
	if w.size != int64(len(chunk)) {
		t.Errorf("expected fresh file to only contain the latest write, got size %d", w.size)
	}
}

func TestRotatesOnAge(t *testing.T) {
	dir := t.TempDir()
	w, err := newRotatingWriter(dir)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	w.openedAt = time.Now().Add(-25 * time.Hour)

	if _, err := w.Write([]byte("after a day\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	var current bool
	for _, e := range entries {
		if e.Name() == LogFileName {
			current = true
		} else if strings.HasPrefix(e.Name(), "foilen-box-") {
			t.Errorf("expected old log file to be deleted on rotation, found %v", e.Name())
		}
	}
	if !current {
		t.Errorf("expected a fresh current log file in %v", entries)
	}
	if time.Since(w.openedAt) > time.Minute {
		t.Errorf("expected openedAt to be reset on the fresh file, got %v", w.openedAt)
	}
}

func TestReadTailTruncates(t *testing.T) {
	dir := t.TempDir()
	data := strings.Repeat("y", maxTailBytes+1000)
	if err := os.WriteFile(filepath.Join(dir, LogFileName), []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	text, err := ReadTail(dir, "")
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}
	if !strings.HasPrefix(text, "... (truncated) ...\n") {
		t.Errorf("expected truncation marker, got prefix: %q", text[:40])
	}
	if len(text) > maxTailBytes+100 {
		t.Errorf("expected truncated output near maxTailBytes, got %d bytes", len(text))
	}
}

func TestReadTailMissingFile(t *testing.T) {
	dir := t.TempDir()
	text, err := ReadTail(dir, "")
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty text for missing file, got %q", text)
	}
}

func TestReadTailSearchFiltersLines(t *testing.T) {
	dir := t.TempDir()
	data := "alpha line\nbeta line\nAnother Alpha\n"
	if err := os.WriteFile(filepath.Join(dir, LogFileName), []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	text, err := ReadTail(dir, "alpha")
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}
	want := "alpha line\nAnother Alpha"
	if text != want {
		t.Errorf("expected %q, got %q", want, text)
	}
}
