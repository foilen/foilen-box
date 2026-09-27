package config

import (
	"os"
	"path/filepath"

	"foilen-realm/jsondb"
	"foilen-realm/model"
)

const configFileName = "realm.json"

type Service struct {
	configFile     string
	defaultDhtMode string
}

func New(configDir string, defaultDhtMode string) (*Service, error) {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, err
	}
	return &Service{
		configFile:     filepath.Join(configDir, configFileName),
		defaultDhtMode: defaultDhtMode,
	}, nil
}

func (s *Service) Dir() string {
	return filepath.Dir(s.configFile)
}

func (s *Service) Load() model.Config {
	return jsondb.LoadFile(s.configFile, model.Config{
		DhtMode:            s.defaultDhtMode,
		EnableUdpBroadcast: true,
		EnableDht:          true,
	})
}

func (s *Service) Save(cfg model.Config) error {
	return jsondb.SaveFile(s.configFile, cfg)
}
