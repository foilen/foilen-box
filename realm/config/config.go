// Package config persists the local Realm configuration (peer identity,
// groups, discovery settings) as pretty-printed JSON at
// $FOILEN_BOX_CONFIG_DIR/realm.json, or ~/.foilen-box/realm.json if that env
// var isn't set. Mirrors internal/early/config.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"foilen-realm/model"
)

const configFileName = "realm.json"

// Service loads/saves model.Config from a directory resolved at
// construction time (desktop: env var or home dir; Android: pass the app's
// files dir).
type Service struct {
	configFile     string
	defaultDhtMode string
}

// New resolves the config directory from $FOILEN_BOX_CONFIG_DIR, falling
// back to ~/.foilen-box, creates it if needed, and returns a Service backed
// by realm.json inside it. defaultDhtMode (model.DhtModeServer or
// model.DhtModeClient) is applied only when realm.json doesn't exist yet
// (decision 6) — once persisted, a loaded config's DhtMode always wins.
func New(defaultDhtMode string) (*Service, error) {
	dir := os.Getenv("FOILEN_BOX_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, ".foilen-box")
	}
	return NewInDir(dir, defaultDhtMode)
}

// NewInDir builds a Service backed by realm.json inside the given
// directory, creating the directory if needed (used on Android with the
// app's files dir). See New for defaultDhtMode.
func NewInDir(configDir string, defaultDhtMode string) (*Service, error) {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, err
	}
	return &Service{
		configFile:     filepath.Join(configDir, configFileName),
		defaultDhtMode: defaultDhtMode,
	}, nil
}

// Dir returns the directory realm.json lives in, so callers can locate
// sibling Realm state (peer store, DHT datastore) alongside it.
func (s *Service) Dir() string {
	return filepath.Dir(s.configFile)
}

// Load returns the persisted config, or a default config (per-platform
// DhtMode, mDNS/DHT both enabled) if the file is missing or
// unreadable/corrupt — read errors are intentionally swallowed here,
// matching early/config.Service.
func (s *Service) Load() model.Config {
	data, err := os.ReadFile(s.configFile)
	if err != nil {
		return s.defaultConfig()
	}
	var cfg model.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return s.defaultConfig()
	}
	return cfg
}

func (s *Service) defaultConfig() model.Config {
	return model.Config{
		DhtMode:    s.defaultDhtMode,
		EnableMdns: true,
		EnableDht:  true,
	}
}

// Save writes the config as pretty-printed JSON.
func (s *Service) Save(cfg model.Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to save Realm config: %w", err)
	}
	if err := os.WriteFile(s.configFile, data, 0o644); err != nil {
		return fmt.Errorf("failed to save Realm config: %w", err)
	}
	return nil
}
