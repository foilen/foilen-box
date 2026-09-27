package sms

import (
	"os"
	"path/filepath"

	"foilen-realm/jsondb"
)

const configFileName = "sms.json"

type Config struct {
	Enabled   bool   `json:"enabled"`
	GroupID   string `json:"groupId"`
	StoreName string `json:"storeName"`
}

type Service struct {
	configFile string
}

func NewConfigService(configDir string) (*Service, error) {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, err
	}
	return &Service{configFile: filepath.Join(configDir, configFileName)}, nil
}

func (s *Service) Load() Config {
	return jsondb.LoadFile(s.configFile, Config{})
}

func (s *Service) Save(cfg Config) error {
	return jsondb.SaveFile(s.configFile, cfg)
}
