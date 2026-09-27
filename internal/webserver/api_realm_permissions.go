package webserver

import (
	"encoding/json"
	"fmt"

	realmmodel "foilen-realm/model"
)

func parseActions(api *api, actions []string) ([]realmmodel.PermissionAction, error) {
	available := api.realmEngine.AvailableActions()
	result := make([]realmmodel.PermissionAction, 0, len(actions))
	for _, a := range actions {
		action := realmmodel.PermissionAction(a)
		valid := false
		for _, known := range available {
			if action == known {
				valid = true
				break
			}
		}
		if !valid {
			return nil, fmt.Errorf("unknown action: %s", a)
		}
		result = append(result, action)
	}
	return result, nil
}

func handleRealmAddPermission(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Action    string `json:"action"`
		PeerID    string `json:"peerId"`
		GroupName string `json:"groupName"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if (p.PeerID == "") == (p.GroupName == "") {
		return nil, fmt.Errorf("please specify exactly one of peerId or groupName")
	}
	actions, err := parseActions(a, []string{p.Action})
	if err != nil {
		return nil, err
	}
	action := actions[0]
	cfg := a.realmConfig.Load()
	if p.GroupName != "" && !a.groupExists(p.GroupName) {
		return nil, fmt.Errorf("no group named %q", p.GroupName)
	}
	for _, perm := range cfg.Permissions {
		if perm.Action == action && perm.PeerID == p.PeerID && perm.GroupName == p.GroupName {
			return nil, fmt.Errorf("this permission rule already exists")
		}
	}
	cfg, err = a.updateRealmConfig(func(c *realmmodel.Config) {
		c.Permissions = append(c.Permissions, realmmodel.Permission{Action: action, PeerID: p.PeerID, GroupName: p.GroupName})
	})
	if err != nil {
		return nil, err
	}
	return realmConfigResponse(a, cfg), nil
}

func handleRealmDeletePermission(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Action    string `json:"action"`
		PeerID    string `json:"peerId"`
		GroupName string `json:"groupName"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		filtered := c.Permissions[:0]
		for _, perm := range c.Permissions {
			if !(string(perm.Action) == p.Action && perm.PeerID == p.PeerID && perm.GroupName == p.GroupName) {
				filtered = append(filtered, perm)
			}
		}
		c.Permissions = filtered
	})
	if err != nil {
		return nil, err
	}
	return realmConfigResponse(a, cfg), nil
}
