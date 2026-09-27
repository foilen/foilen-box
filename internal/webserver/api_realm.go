package webserver

import (
	"encoding/json"
	"fmt"

	realmkeypair "foilen-realm/keypair"
	realmmodel "foilen-realm/model"
)

type realmConfigResult struct {
	PeerID             string                        `json:"peerId"`
	Permissions        []permissionResult            `json:"permissions"`
	AvailableActions   []realmmodel.PermissionAction `json:"availableActions"`
	Hostname           string                        `json:"hostname"`
	Addresses          []string                      `json:"addresses"`
	Description        string                        `json:"description"`
	Enabled            bool                          `json:"enabled"`
	DhtMode            string                        `json:"dhtMode"`
	EnableUdpBroadcast bool                          `json:"enableUdpBroadcast"`
	EnableDht          bool                          `json:"enableDht"`
	EnableRelayService bool                          `json:"enableRelayService"`
	PeerRetentionDays  int                           `json:"peerRetentionDays"`
	Groups             []groupResult                 `json:"groups"`
	Identities         []identityResult              `json:"identities"`
	Scripts            []scriptResult                `json:"scripts"`
	Services           []serviceResult               `json:"services"`

	RealmListenPort     int    `json:"realmListenPort"`
	RealmListenPortMode string `json:"realmListenPortMode"`

	ExposeWebEnabled          bool   `json:"exposeWebEnabled"`
	ExposeWebListenProtocol   string `json:"exposeWebListenProtocol"`
	ExposeWebListenPort       int    `json:"exposeWebListenPort"`
	ExposeWebAnnounceHost     string `json:"exposeWebAnnounceHost"`
	ExposeWebAnnouncePort     int    `json:"exposeWebAnnouncePort"`
	ExposeWebAnnounceProtocol string `json:"exposeWebAnnounceProtocol"`
}

type groupResult struct {
	Name             string `json:"name"`
	ID               string `json:"id"`
	PrivateKeyBase64 string `json:"privateKeyBase64"`
}

type identityResult struct {
	Name             string `json:"name"`
	ID               string `json:"id"`
	PrivateKeyBase64 string `json:"privateKeyBase64"`
}

type permissionResult struct {
	Action    string `json:"action"`
	PeerID    string `json:"peerId"`
	GroupName string `json:"groupName"`
}

type scriptResult struct {
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Command          string   `json:"command"`
	Args             []string `json:"args"`
	WorkingDirectory string   `json:"workingDirectory"`
}

type serviceResult struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Hostname    string `json:"hostname"`
	Type        string `json:"type"`
	Port        int    `json:"port"`
}

func realmConfigResponse(a *api, cfg realmmodel.Config) realmConfigResult {
	groups := make([]groupResult, 0, len(cfg.Groups))
	for _, g := range cfg.Groups {
		groups = append(groups, groupResult{
			Name:             g.Name,
			ID:               g.KeyPair.ID,
			PrivateKeyBase64: g.KeyPair.PrivateKeyBase64,
		})
	}
	identities := make([]identityResult, 0, len(cfg.Identities))
	for _, id := range cfg.Identities {
		identities = append(identities, identityResult{
			Name:             id.Name,
			ID:               id.KeyPair.ID,
			PrivateKeyBase64: id.KeyPair.PrivateKeyBase64,
		})
	}
	permissions := make([]permissionResult, 0, len(cfg.Permissions))
	for _, p := range cfg.Permissions {
		permissions = append(permissions, permissionResult{
			Action:    string(p.Action),
			PeerID:    p.PeerID,
			GroupName: p.GroupName,
		})
	}
	scripts := make([]scriptResult, 0, len(cfg.Scripts))
	for _, sc := range cfg.Scripts {
		scripts = append(scripts, scriptResult{
			Name:             sc.Name,
			Description:      sc.Description,
			Command:          sc.Command,
			Args:             sc.Args,
			WorkingDirectory: sc.WorkingDirectory,
		})
	}
	services := make([]serviceResult, 0, len(cfg.Services))
	for _, svc := range cfg.Services {
		services = append(services, serviceResult{
			Name:        svc.Name,
			Description: svc.Description,
			Hostname:    svc.Hostname,
			Type:        svc.Type,
			Port:        svc.Port,
		})
	}
	hostname := resolveHostname(a.hostnameOverride)
	return realmConfigResult{
		PeerID:             cfg.PeerID.ID,
		Permissions:        permissions,
		AvailableActions:   a.realmEngine.AvailableActions(),
		Hostname:           hostname,
		Addresses:          a.realmEngine.Addrs(),
		Description:        cfg.Description,
		Enabled:            !cfg.Disabled,
		DhtMode:            cfg.DhtMode,
		EnableUdpBroadcast: cfg.EnableUdpBroadcast,
		EnableDht:          cfg.EnableDht,
		EnableRelayService: cfg.EnableRelayService,
		PeerRetentionDays:  cfg.PeerRetentionDays,
		Groups:             groups,
		Identities:         identities,
		Scripts:            scripts,
		Services:           services,

		RealmListenPort:     cfg.RealmListenPort,
		RealmListenPortMode: cfg.RealmListenPortMode,

		ExposeWebEnabled:          cfg.ExposeWebEnabled,
		ExposeWebListenProtocol:   cfg.ExposeWebListenProtocol,
		ExposeWebListenPort:       cfg.ExposeWebListenPort,
		ExposeWebAnnounceHost:     cfg.ExposeWebAnnounceHost,
		ExposeWebAnnouncePort:     cfg.ExposeWebAnnouncePort,
		ExposeWebAnnounceProtocol: cfg.ExposeWebAnnounceProtocol,
	}
}

func handleRealmLoadConfig(a *api, _ json.RawMessage) (any, error) {
	return realmConfigResponse(a, a.realmConfig.Load()), nil
}

func handleRealmGeneratePeerID(a *api, _ json.RawMessage) (any, error) {
	kp, err := realmkeypair.Generate()
	if err != nil {
		return nil, err
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) { c.PeerID = kp })
	if err != nil {
		return nil, err
	}
	return realmConfigResponse(a, cfg), nil
}

func handleRealmSetDescription(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) { c.Description = p.Description })
	if err != nil {
		return nil, err
	}
	return realmConfigResponse(a, cfg), nil
}

func handleRealmSetDhtMode(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Mode != realmmodel.DhtModeClient && p.Mode != realmmodel.DhtModeServer {
		return nil, fmt.Errorf("invalid DHT mode: %s", p.Mode)
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) { c.DhtMode = p.Mode })
	if err != nil {
		return nil, err
	}
	return realmConfigResponse(a, cfg), nil
}

func handleRealmSetEnabled(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) { c.Disabled = !p.Enabled })
	if err != nil {
		return nil, err
	}
	if !p.Enabled {
		a.realmServices.StopAll()
	}
	if a.realmStateSink != nil {
		a.realmStateSink.SetRealmEnabled(p.Enabled)
	}
	return realmConfigResponse(a, cfg), nil
}

func handleRealmSetDiscoveryOptions(a *api, params json.RawMessage) (any, error) {
	var p struct {
		EnableUdpBroadcast bool `json:"enableUdpBroadcast"`
		EnableDht          bool `json:"enableDht"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		c.EnableUdpBroadcast = p.EnableUdpBroadcast
		c.EnableDht = p.EnableDht
	})
	if err != nil {
		return nil, err
	}
	return realmConfigResponse(a, cfg), nil
}

func handleRealmSetEnableRelayService(a *api, params json.RawMessage) (any, error) {
	var p struct {
		EnableRelayService bool `json:"enableRelayService"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) { c.EnableRelayService = p.EnableRelayService })
	if err != nil {
		return nil, err
	}
	return realmConfigResponse(a, cfg), nil
}

func handleRealmSetListenPort(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Mode string `json:"mode"`
		Port int    `json:"port"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Mode != realmmodel.ListenPortModeDefault && p.Mode != realmmodel.ListenPortModeSpecific {
		return nil, fmt.Errorf("unknown listen port mode: %s", p.Mode)
	}
	if p.Mode == realmmodel.ListenPortModeSpecific && (p.Port < 1 || p.Port > 65535) {
		return nil, fmt.Errorf("specific listen port must be between 1 and 65535")
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		c.RealmListenPortMode = p.Mode
		if p.Mode == realmmodel.ListenPortModeSpecific {
			c.RealmListenPort = p.Port
		} else {
			c.RealmListenPort = 0
		}
	})
	if err != nil {
		return nil, err
	}
	return realmConfigResponse(a, cfg), nil
}

func handleRealmSetExposeWeb(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Enabled          bool   `json:"enabled"`
		ListenProtocol   string `json:"listenProtocol"`
		ListenPort       int    `json:"listenPort"`
		AnnounceHost     string `json:"announceHost"`
		AnnouncePort     int    `json:"announcePort"`
		AnnounceProtocol string `json:"announceProtocol"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		c.ExposeWebEnabled = p.Enabled
		c.ExposeWebListenProtocol = p.ListenProtocol
		c.ExposeWebListenPort = p.ListenPort
		c.ExposeWebAnnounceHost = p.AnnounceHost
		c.ExposeWebAnnouncePort = p.AnnouncePort
		c.ExposeWebAnnounceProtocol = p.AnnounceProtocol
	})
	if err != nil {
		return nil, err
	}
	return realmConfigResponse(a, cfg), nil
}

func handleRealmSetPeerRetentionDays(a *api, params json.RawMessage) (any, error) {
	var p struct {
		PeerRetentionDays int `json:"peerRetentionDays"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) { c.PeerRetentionDays = p.PeerRetentionDays })
	if err != nil {
		return nil, err
	}
	return realmConfigResponse(a, cfg), nil
}
