package webserver

import (
	"encoding/json"
	"fmt"
	"time"

	"foilen-box/internal/browseropen"

	realmkeypair "foilen-realm/keypair"
	realmmodel "foilen-realm/model"
)

// realmConfigResult is the JSON shape returned by every handler that
// mutates or loads the Realm config.
type realmConfigResult struct {
	PeerID             string                        `json:"peerId"`
	Permissions        []permissionResult            `json:"permissions"`
	AvailableActions   []realmmodel.PermissionAction `json:"availableActions"`
	Hostname           string                        `json:"hostname"`
	Description        string                        `json:"description"`
	Enabled            bool                          `json:"enabled"`
	DhtMode            string                        `json:"dhtMode"`
	EnableMdns         bool                          `json:"enableMdns"`
	EnableDht          bool                          `json:"enableDht"`
	EnableRelayService bool                          `json:"enableRelayService"`
	PeerRetentionDays  int                           `json:"peerRetentionDays"`
	Groups             []groupResult                 `json:"groups"`
	Scripts            []scriptResult                `json:"scripts"`
	Services           []serviceResult               `json:"services"`

	ExposeWebEnabled          bool   `json:"exposeWebEnabled"`
	ExposeWebListenProtocol   string `json:"exposeWebListenProtocol"`
	ExposeWebListenPort       int    `json:"exposeWebListenPort"`
	ExposeWebAnnounceHost     string `json:"exposeWebAnnounceHost"`
	ExposeWebAnnouncePort     int    `json:"exposeWebAnnouncePort"`
	ExposeWebAnnounceProtocol string `json:"exposeWebAnnounceProtocol"`
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

type groupResult struct {
	Name             string `json:"name"`
	ID               string `json:"id"`
	PrivateKeyBase64 string `json:"privateKeyBase64"`
}

type permissionResult struct {
	Action    string `json:"action"`
	PeerID    string `json:"peerId"`
	GroupName string `json:"groupName"`
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
		Description:        cfg.Description,
		Enabled:            !cfg.Disabled,
		DhtMode:            cfg.DhtMode,
		EnableMdns:         cfg.EnableMdns,
		EnableDht:          cfg.EnableDht,
		EnableRelayService: cfg.EnableRelayService,
		PeerRetentionDays:  cfg.PeerRetentionDays,
		Groups:             groups,
		Scripts:            scripts,
		Services:           services,

		ExposeWebEnabled:          cfg.ExposeWebEnabled,
		ExposeWebListenProtocol:   cfg.ExposeWebListenProtocol,
		ExposeWebListenPort:       cfg.ExposeWebListenPort,
		ExposeWebAnnounceHost:     cfg.ExposeWebAnnounceHost,
		ExposeWebAnnouncePort:     cfg.ExposeWebAnnouncePort,
		ExposeWebAnnounceProtocol: cfg.ExposeWebAnnounceProtocol,
	}
}

// parseActions validates that each string in actions is a known action per
// api's engine (i.e. declared by one of its registered features), returning
// the typed slice.
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

// groupExists reports whether a group named name already exists in the
// current Realm config.
func (a *api) groupExists(name string) bool {
	for _, g := range a.realmConfig.Load().Groups {
		if g.Name == name {
			return true
		}
	}
	return false
}

// createGroup adds a new group under kp and grants it actionNames,
// persisting and reconciling the engine. Shared by realm.addGroup (a
// freshly generated keypair) and realm.importGroup (an imported one).
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

// scriptExists reports whether a script named name already exists in the
// current Realm config.
func (a *api) scriptExists(name string) bool {
	for _, sc := range a.realmConfig.Load().Scripts {
		if sc.Name == name {
			return true
		}
	}
	return false
}

func handleRealmAddScript(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Name             string   `json:"name"`
		Description      string   `json:"description"`
		Command          string   `json:"command"`
		Args             []string `json:"args"`
		WorkingDirectory string   `json:"workingDirectory"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Name == "" || p.Command == "" {
		return nil, fmt.Errorf("please enter both a script name and a command")
	}
	if a.scriptExists(p.Name) {
		return nil, fmt.Errorf("a script named %q already exists", p.Name)
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		c.Scripts = append(c.Scripts, realmmodel.Script{Name: p.Name, Description: p.Description, Command: p.Command, Args: p.Args, WorkingDirectory: p.WorkingDirectory})
	})
	if err != nil {
		return nil, err
	}
	return realmConfigResponse(a, cfg), nil
}

func handleRealmUpdateScript(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Name             string   `json:"name"`
		Description      string   `json:"description"`
		Command          string   `json:"command"`
		Args             []string `json:"args"`
		WorkingDirectory string   `json:"workingDirectory"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Name == "" || p.Command == "" {
		return nil, fmt.Errorf("please enter both a script name and a command")
	}
	if !a.scriptExists(p.Name) {
		return nil, fmt.Errorf("no script named %q", p.Name)
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		for i := range c.Scripts {
			if c.Scripts[i].Name == p.Name {
				c.Scripts[i].Description = p.Description
				c.Scripts[i].Command = p.Command
				c.Scripts[i].Args = p.Args
				c.Scripts[i].WorkingDirectory = p.WorkingDirectory
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

// serviceExists reports whether a service named name already exists in the
// current Realm config.
func (a *api) serviceExists(name string) bool {
	for _, svc := range a.realmConfig.Load().Services {
		if svc.Name == name {
			return true
		}
	}
	return false
}

// validServiceTypes are the recognized Service.Type values.
var validServiceTypes = map[string]bool{
	realmmodel.ServiceTypeTCP:   true,
	realmmodel.ServiceTypeUDP:   true,
	realmmodel.ServiceTypeHTTP:  true,
	realmmodel.ServiceTypeHTTPS: true,
	realmmodel.ServiceTypeVNC:   true,
	realmmodel.ServiceTypeVPN:   true,
	realmmodel.ServiceTypeRDP:   true,
	realmmodel.ServiceTypeSSH:   true,
}

func handleRealmAddService(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Hostname    string `json:"hostname"`
		Type        string `json:"type"`
		Port        int    `json:"port"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Name == "" || p.Hostname == "" || p.Port <= 0 {
		return nil, fmt.Errorf("please enter a name, a hostname, and a valid port")
	}
	if !validServiceTypes[p.Type] {
		return nil, fmt.Errorf("invalid service type: %s", p.Type)
	}
	if a.serviceExists(p.Name) {
		return nil, fmt.Errorf("a service named %q already exists", p.Name)
	}
	svc := realmmodel.Service{Name: p.Name, Description: p.Description, Hostname: p.Hostname, Type: p.Type, Port: p.Port}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		c.Services = append(c.Services, svc)
	})
	if err != nil {
		return nil, err
	}
	announceServiceNow(a.realmMapsFeature, cfg, svc)
	return realmConfigResponse(a, cfg), nil
}

func handleRealmUpdateService(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Hostname    string `json:"hostname"`
		Type        string `json:"type"`
		Port        int    `json:"port"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Name == "" || p.Hostname == "" || p.Port <= 0 {
		return nil, fmt.Errorf("please enter a name, a hostname, and a valid port")
	}
	if !validServiceTypes[p.Type] {
		return nil, fmt.Errorf("invalid service type: %s", p.Type)
	}
	if !a.serviceExists(p.Name) {
		return nil, fmt.Errorf("no service named %q", p.Name)
	}
	svc := realmmodel.Service{Name: p.Name, Description: p.Description, Hostname: p.Hostname, Type: p.Type, Port: p.Port}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		for i := range c.Services {
			if c.Services[i].Name == p.Name {
				c.Services[i].Description = p.Description
				c.Services[i].Hostname = p.Hostname
				c.Services[i].Type = p.Type
				c.Services[i].Port = p.Port
				break
			}
		}
	})
	if err != nil {
		return nil, err
	}
	announceServiceNow(a.realmMapsFeature, cfg, svc)
	return realmConfigResponse(a, cfg), nil
}

func handleRealmDeleteService(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		filtered := c.Services[:0]
		for _, svc := range c.Services {
			if svc.Name != p.Name {
				filtered = append(filtered, svc)
			}
		}
		c.Services = filtered
	})
	if err != nil {
		return nil, err
	}
	retractServiceNow(a.realmMapsFeature, cfg, p.Name)
	return realmConfigResponse(a, cfg), nil
}

func handleRealmScanLocalPorts(a *api, _ json.RawMessage) (any, error) {
	return map[string]any{"results": a.realmServices.ScanLocalPorts()}, nil
}

type activeProxyResult struct {
	PeerID      string `json:"peerId"`
	ServiceName string `json:"serviceName"`
	LocalPort   int    `json:"localPort"`
}

func handleRealmListActiveProxies(a *api, _ json.RawMessage) (any, error) {
	active := a.realmServices.ListActive()
	result := make([]activeProxyResult, 0, len(active))
	for _, p := range active {
		result = append(result, activeProxyResult{PeerID: p.PeerID, ServiceName: p.ServiceName, LocalPort: p.LocalPort})
	}
	return map[string]any{"proxies": result}, nil
}

func handleRealmStartServiceProxy(a *api, params json.RawMessage) (any, error) {
	var p struct {
		PeerId string `json:"peerId"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.PeerId == "" || p.Name == "" {
		return nil, fmt.Errorf("please select a peer and a service")
	}
	port, err := a.realmServices.StartProxy(p.PeerId, p.Name)
	if err != nil {
		return nil, err
	}
	return map[string]any{"localPort": port}, nil
}

func handleRealmStopServiceProxy(a *api, params json.RawMessage) (any, error) {
	var p struct {
		PeerId string `json:"peerId"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if err := a.realmServices.StopProxy(p.PeerId, p.Name); err != nil {
		return nil, err
	}
	return map[string]any{"stopped": true}, nil
}

func handleRealmConnectService(a *api, params json.RawMessage) (any, error) {
	var p struct {
		PeerId string `json:"peerId"`
		Name   string `json:"name"`
		Type   string `json:"type"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.PeerId == "" || p.Name == "" {
		return nil, fmt.Errorf("please select a peer and a service")
	}
	port, err := a.realmServices.StartProxy(p.PeerId, p.Name)
	if err != nil {
		return nil, err
	}

	var openErr error
	opened := true
	switch p.Type {
	case realmmodel.ServiceTypeHTTP:
		openErr = browseropen.OpenHTTP(port, false)
	case realmmodel.ServiceTypeHTTPS:
		openErr = browseropen.OpenHTTP(port, true)
	case realmmodel.ServiceTypeSSH:
		openErr = browseropen.OpenSSH(port)
	case realmmodel.ServiceTypeVNC:
		openErr = browseropen.OpenVNC(port)
	case realmmodel.ServiceTypeRDP:
		openErr = browseropen.OpenRDP(port)
	default:
		opened = false
	}

	result := map[string]any{"localPort": port, "opened": opened && openErr == nil}
	if openErr != nil {
		result["error"] = openErr.Error()
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
	var group *realmmodel.Group
	for _, g := range cfg.Groups {
		if g.Name == p.Name {
			group = &g
			break
		}
	}
	if group == nil {
		return nil, fmt.Errorf("no group named %q", p.Name)
	}
	// The local peer is always "online" and belongs to every group in its
	// own config, but it never appears in a.realmPeers (that store only
	// tracks remote peers seen over libp2p connections), so list it first.
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
		EnableMdns bool `json:"enableMdns"`
		EnableDht  bool `json:"enableDht"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		c.EnableMdns = p.EnableMdns
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

type peerResult struct {
	ID           string    `json:"id"`
	Hostname     string    `json:"hostname"`
	Description  string    `json:"description"`
	LastSeen     time.Time `json:"lastSeen"`
	Addresses    []string  `json:"addresses"`
	GroupNames   []string  `json:"groupNames"`
	Connected    bool      `json:"connected"`
	RelayEnabled bool      `json:"relayEnabled"`
}

func handleRealmListPeers(a *api, _ json.RawMessage) (any, error) {
	list := a.realmPeers.List()
	result := make([]peerResult, 0, len(list))
	for _, p := range list {
		result = append(result, peerResult{
			ID:           p.ID,
			Hostname:     p.Hostname,
			Description:  p.Description,
			LastSeen:     p.LastSeen,
			Addresses:    p.Addresses,
			GroupNames:   p.GroupNames,
			Connected:    p.Connected,
			RelayEnabled: p.RelayEnabled,
		})
	}
	return map[string]any{"peers": result}, nil
}

type swarmPeerResult struct {
	ID        string   `json:"id"`
	Addresses []string `json:"addresses"`
}

func handleRealmListSwarmPeers(a *api, _ json.RawMessage) (any, error) {
	list := a.realmEngine.SwarmPeers()
	result := make([]swarmPeerResult, 0, len(list))
	for _, p := range list {
		result = append(result, swarmPeerResult{ID: p.ID, Addresses: p.Addresses})
	}
	return map[string]any{"peers": result}, nil
}

type notificationResult struct {
	ID         string    `json:"id"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	SentAt     time.Time `json:"sentAt"`
	TTLSeconds int       `json:"ttlSeconds"`
	Direction  string    `json:"direction"`
	Delivered  bool      `json:"delivered"`
}

func notificationResponse(n realmmodel.Notification) notificationResult {
	return notificationResult{
		ID:         n.ID,
		From:       n.From,
		To:         n.To,
		Title:      n.Title,
		Body:       n.Body,
		SentAt:     n.SentAt,
		TTLSeconds: n.TTLSeconds,
		Direction:  string(n.Direction),
		Delivered:  n.Delivered,
	}
}

func handleNotificationSend(a *api, params json.RawMessage) (any, error) {
	var p struct {
		To         string `json:"to"`
		Title      string `json:"title"`
		Body       string `json:"body"`
		TTLSeconds int    `json:"ttlSeconds"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.To == "" || p.Title == "" {
		return nil, fmt.Errorf("please select a peer and enter a title")
	}
	if p.TTLSeconds <= 0 {
		return nil, fmt.Errorf("please select how long this notification should be kept")
	}
	n, err := a.realmNotifFeature.SendNotification(p.To, p.Title, p.Body, p.TTLSeconds)
	if err != nil {
		return nil, err
	}
	return notificationResponse(n), nil
}

func handleNotificationList(a *api, _ json.RawMessage) (any, error) {
	list := a.realmNotifications.List()
	result := make([]notificationResult, 0, len(list))
	for _, n := range list {
		result = append(result, notificationResponse(n))
	}
	return map[string]any{"notifications": result}, nil
}

