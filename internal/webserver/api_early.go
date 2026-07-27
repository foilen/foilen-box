package webserver

import (
	"encoding/json"
	"fmt"

	earlymodel "foilen-box/internal/early/model"
	earlyreport "foilen-box/internal/early/report"
)

func handleEarlyLoadConfig(a *api, _ json.RawMessage) (any, error) {
	cfg := a.earlyConfig.Load()
	return map[string]string{"apiKey": cfg.APIKey, "apiSecret": cfg.APISecret}, nil
}

func handleEarlySaveConfig(a *api, params json.RawMessage) (any, error) {
	var cfg earlymodel.ConfigEarly
	if err := json.Unmarshal(params, &cfg); err != nil {
		return nil, err
	}
	if cfg.APIKey == "" || cfg.APISecret == "" {
		return nil, fmt.Errorf("please enter both API Key and API Secret")
	}
	if err := a.earlyConfig.Save(cfg); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

func handleEarlyAggregate(a *api, _ json.RawMessage) (any, error) {
	result, err := a.earlyAggregate.Aggregate()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"text":          earlyreport.Format(result),
		"activityNames": result.ActivityNames,
	}, nil
}

func handleEarlyDelete(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Activity string `json:"activity"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Activity == "" {
		return nil, fmt.Errorf("no activity selected")
	}
	count, err := a.earlyAggregate.DeleteByActivity(p.Activity)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"text":  fmt.Sprintf("Deleted %d time entries for activity %q.", count, p.Activity),
		"count": count,
	}, nil
}
