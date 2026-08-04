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
// ClearLogsOnStartup controls whether the log file is truncated on each
// start (default true); it's a pointer so a config file saved before this
// field existed can still be told apart from one that explicitly disabled
// it, and defaults to true in both Load cases.
// TabLoadCounts/SubtabLoadCounts count how many times each tab/subtab has
// been activated, keyed by its data-tab/data-subtab id, so the UI can show
// the most-used ones first on the next page load.
type uiConfig struct {
	RandomPort         bool           `json:"randomPort"`
	Port               int            `json:"port"`
	ClearLogsOnStartup *bool          `json:"clearLogsOnStartup,omitempty"`
	TabLoadCounts      map[string]int `json:"tabLoadCounts,omitempty"`
	SubtabLoadCounts   map[string]int `json:"subtabLoadCounts,omitempty"`
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
// ClearLogsOnStartup always defaults to true unless explicitly saved false.
func (s *uiConfigService) Load() uiConfig {
	data, err := os.ReadFile(s.configFile)
	if err != nil {
		return uiConfig{RandomPort: true, ClearLogsOnStartup: boolPtr(true)}
	}
	var cfg uiConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return uiConfig{RandomPort: true, ClearLogsOnStartup: boolPtr(true)}
	}
	if cfg.ClearLogsOnStartup == nil {
		cfg.ClearLogsOnStartup = boolPtr(true)
	}
	return cfg
}

func boolPtr(b bool) *bool { return &b }

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
