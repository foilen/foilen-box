package webserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const uiConfigFileName = "webui.json"

// uiConfig is the local port-binding preference for the embedded web
// server: by default it binds a random free port every start (RandomPort
// true, Port ignored); unchecking it in the Config tab pins Port instead.
type uiConfig struct {
	RandomPort bool `json:"randomPort"`
	Port       int  `json:"port"`
}

// uiConfigService loads/saves uiConfig from a directory resolved at
// construction time, mirroring internal/early/config's Service.
type uiConfigService struct {
	configFile string
}

// newUIConfigService builds a uiConfigService backed by webui.json inside
// the given directory, creating the directory if needed.
func newUIConfigService(configDir string) (*uiConfigService, error) {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, err
	}
	return &uiConfigService{configFile: filepath.Join(configDir, uiConfigFileName)}, nil
}

// Load returns the persisted config, defaulting to RandomPort true (the
// pre-existing behavior) if the file is missing or unreadable/corrupt.
func (s *uiConfigService) Load() uiConfig {
	data, err := os.ReadFile(s.configFile)
	if err != nil {
		return uiConfig{RandomPort: true}
	}
	var cfg uiConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return uiConfig{RandomPort: true}
	}
	return cfg
}

// Save writes the config as pretty-printed JSON.
func (s *uiConfigService) Save(cfg uiConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to save web UI config: %w", err)
	}
	if err := os.WriteFile(s.configFile, data, 0o644); err != nil {
		return fmt.Errorf("failed to save web UI config: %w", err)
	}
	return nil
}
