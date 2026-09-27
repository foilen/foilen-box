package webserver

import (
	"encoding/json"
	"fmt"
	"time"

	realmmodel "foilen-realm/model"
)

func (a *api) scriptExists(name string) bool {
	for _, sc := range a.realmConfig.Load().Scripts {
		if sc.Name == name {
			return true
		}
	}
	return false
}

func parseScriptParams(params json.RawMessage) (realmmodel.Script, error) {
	var script realmmodel.Script
	if err := json.Unmarshal(params, &script); err != nil {
		return realmmodel.Script{}, err
	}
	if script.Name == "" || script.Command == "" {
		return realmmodel.Script{}, fmt.Errorf("please enter both a script name and a command")
	}
	return script, nil
}

func handleRealmAddScript(a *api, params json.RawMessage) (any, error) {
	script, err := parseScriptParams(params)
	if err != nil {
		return nil, err
	}
	if a.scriptExists(script.Name) {
		return nil, fmt.Errorf("a script named %q already exists", script.Name)
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		c.Scripts = append(c.Scripts, script)
	})
	if err != nil {
		return nil, err
	}
	return realmConfigResponse(a, cfg), nil
}

func handleRealmUpdateScript(a *api, params json.RawMessage) (any, error) {
	script, err := parseScriptParams(params)
	if err != nil {
		return nil, err
	}
	if !a.scriptExists(script.Name) {
		return nil, fmt.Errorf("no script named %q", script.Name)
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		for i := range c.Scripts {
			if c.Scripts[i].Name == script.Name {
				c.Scripts[i] = script
				break
			}
		}
	})
	if err != nil {
		return nil, err
	}
	return realmConfigResponse(a, cfg), nil
}

func handleRealmDeleteScript(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		filtered := c.Scripts[:0]
		for _, sc := range c.Scripts {
			if sc.Name != p.Name {
				filtered = append(filtered, sc)
			}
		}
		c.Scripts = filtered
	})
	if err != nil {
		return nil, err
	}
	return realmConfigResponse(a, cfg), nil
}

func handleRealmRunPeerScript(a *api, params json.RawMessage) (any, error) {
	var p struct {
		PeerId string `json:"peerId"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.PeerId == "" || p.Name == "" {
		return nil, fmt.Errorf("please select a peer and a script")
	}
	runID, err := a.realmScripts.RunScript(p.PeerId, p.Name)
	if err != nil {
		return nil, err
	}
	return map[string]any{"runId": runID, "started": true}, nil
}

type scriptRunResult struct {
	RunID      string    `json:"runId"`
	PeerID     string    `json:"peerId"`
	ScriptName string    `json:"scriptName"`
	StartedAt  time.Time `json:"startedAt"`
	Status     string    `json:"status"`
	ExitCode   int       `json:"exitCode"`
	Error      string    `json:"error,omitempty"`
}

func handleRealmListScriptRuns(a *api, _ json.RawMessage) (any, error) {
	runs := a.realmScripts.ListRuns()
	result := make([]scriptRunResult, 0, len(runs))
	for _, r := range runs {
		result = append(result, scriptRunResult{
			RunID:      r.RunID,
			PeerID:     r.PeerID,
			ScriptName: r.ScriptName,
			StartedAt:  r.StartedAt,
			Status:     r.Status,
			ExitCode:   r.ExitCode,
			Error:      r.Error,
		})
	}
	return map[string]any{"runs": result}, nil
}
