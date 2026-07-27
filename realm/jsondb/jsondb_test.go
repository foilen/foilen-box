package jsondb

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type testValue struct {
	Count int `json:"count"`
}

func TestLoadMissingFileReturnsZeroValue(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore[testValue](dir, "data.json")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if got := s.Get(); got != (testValue{}) {
		t.Errorf("Get() = %+v, want zero value", got)
	}
}

func TestLoadCorruptFileReturnsZeroValue(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.json"), []byte("not json"), 0o644); err != nil {
		t.Fatalf("setup WriteFile error = %v", err)
	}
	s, err := NewStore[testValue](dir, "data.json")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if got := s.Get(); got != (testValue{}) {
		t.Errorf("Get() = %+v, want zero value", got)
	}
}

func TestLoadExistingFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.json"), []byte(`{"count":42}`), 0o644); err != nil {
		t.Fatalf("setup WriteFile error = %v", err)
	}
	s, err := NewStore[testValue](dir, "data.json")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if got := s.Get(); got != (testValue{Count: 42}) {
		t.Errorf("Get() = %+v, want {Count: 42}", got)
	}
}

func TestFlushWritesImmediately(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore[testValue](dir, "data.json")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	s.Update(func(v *testValue) { v.Count = 1 })
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "data.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != `{
  "count": 1
}` {
		t.Errorf("unexpected file contents: %s", data)
	}
}

func TestUpdateDebouncesAndCoalescesWrites(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore[testValue](dir, "data.json")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	s.SaveDelay = 60 * time.Millisecond

	s.Update(func(v *testValue) { v.Count = 1 })
	time.Sleep(30 * time.Millisecond)
	s.Update(func(v *testValue) { v.Count = 2 })
	time.Sleep(30 * time.Millisecond)
	s.Update(func(v *testValue) { v.Count = 3 })

	// 40ms after the last Update: still within the reset debounce window,
	// so nothing should have been written yet.
	time.Sleep(40 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, "data.json")); err == nil {
		t.Fatalf("expected no file yet, debounce window should still be open")
	}

	// Wait past the debounce window for the single coalesced write.
	time.Sleep(80 * time.Millisecond)
	data, err := os.ReadFile(filepath.Join(dir, "data.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var got testValue
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got.Count != 3 {
		t.Errorf("Count = %d, want 3 (last value, single write)", got.Count)
	}
}
