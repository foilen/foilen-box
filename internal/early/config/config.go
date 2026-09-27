package config

import (
	"os"
	"path/filepath"

	"foilen-realm/jsondb"

	"foilen-box/internal/early/model"
)

const configFileName = "early.json"

type Service struct {
	configFile string
}

func New(configDir string) (*Service, error) {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, err
	}
	return &Service{configFile: filepath.Join(configDir, configFileName)}, nil
}

func (s *Service) Load() model.ConfigEarly {
	return jsondb.LoadFile(s.configFile, model.ConfigEarly{})
}

func (s *Service) Save(cfg model.ConfigEarly) error {
	return jsondb.SaveFile(s.configFile, cfg)
}
