package webserver

import (
	"encoding/json"
	"fmt"
)

type configResult struct {
	RandomPort         bool `json:"randomPort"`
	Port               int  `json:"port"`
	ClearLogsOnStartup bool `json:"clearLogsOnStartup"`
}

func handleConfigLoadConfig(a *api, _ json.RawMessage) (any, error) {
	cfg := a.uiConfig.Load()
	port := cfg.Port
	if port == 0 {
		port = a.currentPort
	}
	return configResult{RandomPort: cfg.RandomPort, Port: port, ClearLogsOnStartup: *cfg.ClearLogsOnStartup}, nil
}

func handleConfigSaveConfig(a *api, params json.RawMessage) (any, error) {
	var p struct {
		RandomPort         bool `json:"randomPort"`
		Port               int  `json:"port"`
		ClearLogsOnStartup bool `json:"clearLogsOnStartup"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if !p.RandomPort && (p.Port < 1 || p.Port > 65535) {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	cfg := a.uiConfig.Load()
	cfg.RandomPort = p.RandomPort
	cfg.Port = p.Port
	cfg.ClearLogsOnStartup = &p.ClearLogsOnStartup
	if err := a.uiConfig.Save(cfg); err != nil {
		return nil, err
	}
	return nil, nil
}

type tabStatsResult struct {
	TabCounts    map[string]int `json:"tabCounts"`
	SubtabCounts map[string]int `json:"subtabCounts"`
}

func handleConfigLoadTabStats(a *api, _ json.RawMessage) (any, error) {
	cfg := a.uiConfig.Load()
	return tabStatsResult{TabCounts: cfg.TabLoadCounts, SubtabCounts: cfg.SubtabLoadCounts}, nil
}

func handleConfigRecordTabLoad(a *api, params json.RawMessage) (any, error) {
	var p struct {
		TabID string `json:"tabId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.TabID == "" {
		return nil, fmt.Errorf("tabId is required")
	}
	cfg := a.uiConfig.Load()
	if cfg.TabLoadCounts == nil {
		cfg.TabLoadCounts = map[string]int{}
	}
	cfg.TabLoadCounts[p.TabID]++
	if err := a.uiConfig.Save(cfg); err != nil {
		return nil, err
	}
	return nil, nil
}

func handleConfigRecordSubtabLoad(a *api, params json.RawMessage) (any, error) {
	var p struct {
		SubtabID string `json:"subtabId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.SubtabID == "" {
		return nil, fmt.Errorf("subtabId is required")
	}
	cfg := a.uiConfig.Load()
	if cfg.SubtabLoadCounts == nil {
		cfg.SubtabLoadCounts = map[string]int{}
	}
	cfg.SubtabLoadCounts[p.SubtabID]++
	if err := a.uiConfig.Save(cfg); err != nil {
		return nil, err
	}
	return nil, nil
}
