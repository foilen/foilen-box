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

const DefaultSaveDelay = 10 * time.Second

type Store[T any] struct {
	SaveDelay time.Duration

	mu    sync.Mutex
	file  string
	value T
	timer *time.Timer
}

func NewStore[T any](dir, filename string) (*Store[T], error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	file := filepath.Join(dir, filename)
	var zero T
	return &Store[T]{file: file, SaveDelay: DefaultSaveDelay, value: LoadFile(file, zero)}, nil
}

func (s *Store[T]) Get() T {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

func (s *Store[T]) Update(fn func(value *T)) {
	s.mu.Lock()
	fn(&s.value)
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(s.SaveDelay, s.saveAsync)
	s.mu.Unlock()
}

func (s *Store[T]) saveAsync() {
	if err := s.Flush(); err != nil {
		log.Printf("jsondb: failed to save %s: %v", s.file, err)
	}
}

func (s *Store[T]) Flush() error {
	s.mu.Lock()
	value := s.value
	s.mu.Unlock()

	return SaveFile(s.file, value)
}

func LoadFile[T any](file string, fallback T) T {
	data, err := os.ReadFile(file)
	if err != nil {
		return fallback
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return fallback
	}
	return value
}

func SaveFile(file string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal %s: %w", file, err)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		return fmt.Errorf("failed to save %s: %w", file, err)
	}
	return nil
}
