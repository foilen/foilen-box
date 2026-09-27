package webserver

import (
	"os"
	"path/filepath"

	"foilen-realm/jsondb"
)

const uiConfigFileName = "webui.json"

type uiConfig struct {
	RandomPort         bool           `json:"randomPort"`
	Port               int            `json:"port"`
	ClearLogsOnStartup *bool          `json:"clearLogsOnStartup,omitempty"`
	TabLoadCounts      map[string]int `json:"tabLoadCounts,omitempty"`
	SubtabLoadCounts   map[string]int `json:"subtabLoadCounts,omitempty"`
}

type uiConfigService struct {
	configFile string
}

func newUIConfigService(configDir string) (*uiConfigService, error) {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, err
	}
	return &uiConfigService{configFile: filepath.Join(configDir, uiConfigFileName)}, nil
}

func (s *uiConfigService) Load() uiConfig {
	cfg := jsondb.LoadFile(s.configFile, uiConfig{RandomPort: true})
	if cfg.ClearLogsOnStartup == nil {
		cfg.ClearLogsOnStartup = boolPtr(true)
	}
	return cfg
}

func boolPtr(b bool) *bool { return &b }

func (s *uiConfigService) Save(cfg uiConfig) error {
	return jsondb.SaveFile(s.configFile, cfg)
}
