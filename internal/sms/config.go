package sms

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const configFileName = "sms.json"

// Config is the local, per-device SMS management setting: which realmmap (if
// any) this device is the owning/sending authority for. Never synced itself
// — every device decides this independently, the same as Early's API
// credentials.
type Config struct {
	Enabled   bool   `json:"enabled"`
	GroupID   string `json:"groupId"`
	StoreName string `json:"storeName"`
}

// Service loads/saves Config from a directory resolved at construction time
// (desktop: env var or home dir; Android: pass the app's files dir), mirroring
// internal/early/config's Service exactly.
type Service struct {
	configFile string
}

// New resolves the config directory from $FOILEN_BOX_CONFIG_DIR, falling
// back to ~/.foilen-box, creates it if needed, and returns a Service backed
// by sms.json inside it.
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

// NewInDir builds a Service backed by sms.json inside the given directory,
// creating the directory if needed (used on Android with the app's files
// dir).
func NewInDir(configDir string) (*Service, error) {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, err
	}
	return &Service{configFile: filepath.Join(configDir, configFileName)}, nil
}

// Load returns the persisted config, or a zero-value Config if the file is
// missing or unreadable/corrupt.
func (s *Service) Load() Config {
	data, err := os.ReadFile(s.configFile)
	if err != nil {
		return Config{}
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}
	}
	return cfg
}

// Save writes the config as pretty-printed JSON.
func (s *Service) Save(cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to save SMS config: %w", err)
	}
	if err := os.WriteFile(s.configFile, data, 0o644); err != nil {
		return fmt.Errorf("failed to save SMS config: %w", err)
	}
	return nil
}
