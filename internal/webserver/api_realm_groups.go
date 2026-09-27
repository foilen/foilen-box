package webserver

import (
	"encoding/json"
	"fmt"

	realmkeypair "foilen-realm/keypair"
	realmmodel "foilen-realm/model"
)

func (a *api) groupExists(name string) bool {
	_, ok := realmmodel.FindGroupByName(a.realmConfig.Load().Groups, name)
	return ok
}

func (a *api) createGroup(name string, kp realmmodel.KeyPair, actionNames []string) (realmmodel.Config, error) {
	actions, err := parseActions(a, actionNames)
	if err != nil {
		return realmmodel.Config{}, err
	}
	return a.updateRealmConfig(func(c *realmmodel.Config) {
		c.Groups = append(c.Groups, realmmodel.Group{Name: name, KeyPair: kp})
		for _, action := range actions {
			c.Permissions = append(c.Permissions, realmmodel.Permission{Action: action, GroupName: name})
		}
	})
}

func handleRealmAddGroup(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Name    string   `json:"name"`
		Actions []string `json:"actions"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Name == "" {
		return nil, fmt.Errorf("please enter a group name")
	}
	if a.groupExists(p.Name) {
		return nil, fmt.Errorf("a group named %q already exists", p.Name)
	}
	kp, err := realmkeypair.Generate()
	if err != nil {
		return nil, err
	}
	cfg, err := a.createGroup(p.Name, kp, p.Actions)
	if err != nil {
		return nil, err
	}
	return realmConfigResponse(a, cfg), nil
}

func handleRealmImportGroup(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Name             string   `json:"name"`
		PrivateKeyBase64 string   `json:"privateKeyBase64"`
		Actions          []string `json:"actions"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Name == "" || p.PrivateKeyBase64 == "" {
		return nil, fmt.Errorf("please enter both a group name and the private key")
	}
	if a.groupExists(p.Name) {
		return nil, fmt.Errorf("a group named %q already exists", p.Name)
	}
	kp, err := realmkeypair.Import(p.PrivateKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}
	cfg, err := a.createGroup(p.Name, kp, p.Actions)
	if err != nil {
		return nil, err
	}
	return realmConfigResponse(a, cfg), nil
}

func handleRealmDeleteGroup(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		filtered := c.Groups[:0]
		for _, g := range c.Groups {
			if g.Name != p.Name {
				filtered = append(filtered, g)
			}
		}
		c.Groups = filtered

		filteredPerms := c.Permissions[:0]
		for _, perm := range c.Permissions {
			if perm.GroupName != p.Name {
				filteredPerms = append(filteredPerms, perm)
			}
		}
		c.Permissions = filteredPerms
	})
	if err != nil {
		return nil, err
	}
	return realmConfigResponse(a, cfg), nil
}

type onlinePeerResult struct {
	ID        string   `json:"id"`
	Addresses []string `json:"addresses"`
}

type exportGroupResult struct {
	Name             string             `json:"name"`
	PrivateKeyBase64 string             `json:"privateKeyBase64"`
	OnlinePeers      []onlinePeerResult `json:"onlinePeers"`
}

func handleRealmExportGroup(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	cfg := a.realmConfig.Load()
	group, ok := realmmodel.FindGroupByName(cfg.Groups, p.Name)
	if !ok {
		return nil, fmt.Errorf("no group named %q", p.Name)
	}

	onlinePeers := []onlinePeerResult{
		{ID: cfg.PeerID.ID, Addresses: a.realmEngine.Addrs()},
	}
	for _, peer := range a.realmPeers.List() {
		if len(onlinePeers) == 3 {
			break
		}
		if !peer.Connected || peer.ID == cfg.PeerID.ID {
			continue
		}
		for _, groupName := range peer.GroupNames {
			if groupName == p.Name {
				onlinePeers = append(onlinePeers, onlinePeerResult{ID: peer.ID, Addresses: peer.Addresses})
				break
			}
		}
	}
	return exportGroupResult{
		Name:             group.Name,
		PrivateKeyBase64: group.KeyPair.PrivateKeyBase64,
		OnlinePeers:      onlinePeers,
	}, nil
}

func handleRealmPushGroup(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Name   string `json:"name"`
		PeerId string `json:"peerId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Name == "" || p.PeerId == "" {
		return nil, fmt.Errorf("please select both a group and a peer")
	}
	group, ok := realmmodel.FindGroupByName(a.realmConfig.Load().Groups, p.Name)
	if !ok {
		return nil, fmt.Errorf("no group named %q", p.Name)
	}
	if err := a.realmGroup.Push(p.PeerId, p.Name, group.KeyPair); err != nil {
		return nil, err
	}
	return map[string]any{"pushed": true}, nil
}
