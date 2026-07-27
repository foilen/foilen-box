// Package jsondb provides a generic, debounced JSON-file-backed store: the
// value is loaded into memory once at startup, mutations happen in memory,
// and writes to disk are coalesced — after a change, the store waits
// SaveDelay and writes once, with further changes inside that window
// resetting the timer rather than queuing another write.
package jsondb

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultSaveDelay is how long a Store waits after a change before writing
// to disk, unless overridden via SaveDelay.
const DefaultSaveDelay = 10 * time.Second

// Store holds an in-memory value of type T backed by a JSON file on disk.
type Store[T any] struct {
	// SaveDelay overrides DefaultSaveDelay if set before the first Update.
	SaveDelay time.Duration

	mu    sync.Mutex
	file  string
	value T
	timer *time.Timer
}

// NewStore creates the directory if needed and returns a Store backed by
// filename inside it, loading any existing contents into memory. Read
// errors (missing/corrupt file) are swallowed and leave the value at its
// zero value, matching early/config.Service.Load.
func NewStore[T any](dir, filename string) (*Store[T], error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store[T]{file: filepath.Join(dir, filename), SaveDelay: DefaultSaveDelay}
	if data, err := os.ReadFile(s.file); err == nil {
		var v T
		if err := json.Unmarshal(data, &v); err == nil {
			s.value = v
		}
	}
	return s, nil
}

// Get returns a copy of the current in-memory value.
func (s *Store[T]) Get() T {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

// Update runs fn with exclusive access to the in-memory value, then
// schedules a debounced save.
func (s *Store[T]) Update(fn func(value *T)) {
	s.mu.Lock()
	fn(&s.value)
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(s.SaveDelay, s.saveAsync)
	s.mu.Unlock()
}

// saveAsync runs on the debounce timer; failures are logged since there is
// no caller left to return an error to.
func (s *Store[T]) saveAsync() {
	if err := s.Flush(); err != nil {
		log.Printf("jsondb: failed to save %s: %v", s.file, err)
	}
}

// Flush writes the current in-memory value to disk immediately, bypassing
// the debounce timer.
func (s *Store[T]) Flush() error {
	s.mu.Lock()
	value := s.value
	s.mu.Unlock()

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal %s: %w", s.file, err)
	}
	if err := os.WriteFile(s.file, data, 0o644); err != nil {
		return fmt.Errorf("failed to save %s: %w", s.file, err)
	}
	return nil
}
