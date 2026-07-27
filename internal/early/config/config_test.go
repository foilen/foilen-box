package config

import (
	"os"
	"path/filepath"
	"testing"

	"foilen-box/internal/early/model"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	svc, err := NewInDir(dir)
	if err != nil {
		t.Fatalf("NewInDir() error = %v", err)
	}

	want := model.ConfigEarly{APIKey: "key123", APISecret: "secret456"}
	if err := svc.Save(want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got := svc.Load()
	if got != want {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadMissingFileReturnsZeroValue(t *testing.T) {
	dir := t.TempDir()
	svc, err := NewInDir(dir)
	if err != nil {
		t.Fatalf("NewInDir() error = %v", err)
	}

	got := svc.Load()
	if got != (model.ConfigEarly{}) {
		t.Errorf("Load() = %+v, want zero value", got)
	}
}

func TestLoadCorruptFileReturnsZeroValue(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, configFileName), []byte("not json"), 0o644); err != nil {
		t.Fatalf("setup WriteFile error = %v", err)
	}

	svc, err := NewInDir(dir)
	if err != nil {
		t.Fatalf("NewInDir() error = %v", err)
	}

	got := svc.Load()
	if got != (model.ConfigEarly{}) {
		t.Errorf("Load() = %+v, want zero value", got)
	}
}
