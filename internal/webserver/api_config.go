package webserver

import (
	"encoding/json"
	"fmt"
)

// configResult mirrors uiConfig for the WebSocket API, except Port always
// reflects a usable default: the pinned port if one is saved, otherwise the
// port this server instance actually bound this run (so the textbox starts
// pre-filled with something sensible when the user unchecks "Random").
type configResult struct {
	RandomPort bool `json:"randomPort"`
	Port       int  `json:"port"`
}

func handleConfigLoadConfig(a *api, _ json.RawMessage) (any, error) {
	cfg := a.uiConfig.Load()
	port := cfg.Port
	if port == 0 {
		port = a.currentPort
	}
	return configResult{RandomPort: cfg.RandomPort, Port: port}, nil
}

func handleConfigSaveConfig(a *api, params json.RawMessage) (any, error) {
	var p struct {
		RandomPort bool `json:"randomPort"`
		Port       int  `json:"port"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if !p.RandomPort && (p.Port < 1 || p.Port > 65535) {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	cfg := uiConfig{RandomPort: p.RandomPort, Port: p.Port}
	if err := a.uiConfig.Save(cfg); err != nil {
		return nil, err
	}
	return nil, nil
}
