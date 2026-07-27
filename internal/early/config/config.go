// Package config persists the local Early configuration (API key/secret) as
// pretty-printed JSON at $FOILEN_BOX_CONFIG_DIR/early.json, or
// ~/.foilen-box/early.json if that env var isn't set.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"foilen-box/internal/early/model"
)

const configFileName = "early.json"

// Service loads/saves ConfigEarly from a directory resolved at construction
// time (desktop: env var or home dir; Android: pass the app's files dir).
type Service struct {
	configFile string
}

// New resolves the config directory from $FOILEN_BOX_CONFIG_DIR, falling
// back to ~/.foilen-box, creates it if needed, and returns a Service backed
// by early.json inside it.
func New() (*Service, error) {
	dir := os.Getenv("FOILEN_BOX_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, ".foilen-box")
	}
	return NewInDir(dir)
}

// NewInDir builds a Service backed by early.json inside the given directory,
// creating the directory if needed (used on Android with the app's files dir).
func NewInDir(configDir string) (*Service, error) {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, err
	}
	return &Service{configFile: filepath.Join(configDir, configFileName)}, nil
}

// Load returns the persisted config, or a zero-value ConfigEarly if the file
// is missing or unreadable/corrupt — errors are intentionally swallowed here,
// matching the original Java behavior.
func (s *Service) Load() model.ConfigEarly {
	data, err := os.ReadFile(s.configFile)
	if err != nil {
		return model.ConfigEarly{}
	}
	var cfg model.ConfigEarly
	if err := json.Unmarshal(data, &cfg); err != nil {
		return model.ConfigEarly{}
	}
	return cfg
}

// Save writes the config as pretty-printed JSON.
func (s *Service) Save(cfg model.ConfigEarly) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to save Early config: %w", err)
	}
	if err := os.WriteFile(s.configFile, data, 0o644); err != nil {
		return fmt.Errorf("failed to save Early config: %w", err)
	}
	return nil
}
