package webserver

import (
	"encoding/json"
	"fmt"

	"foilen-box/internal/browseropen"

	realmannounce "foilen-realm/features/announce"
	realmmodel "foilen-realm/model"
)

func (a *api) serviceExists(name string) bool {
	for _, svc := range a.realmConfig.Load().Services {
		if svc.Name == name {
			return true
		}
	}
	return false
}

var validServiceTypes = map[string]bool{
	realmmodel.ServiceTypeTCP:   true,
	realmmodel.ServiceTypeUDP:   true,
	realmmodel.ServiceTypeHTTP:  true,
	realmmodel.ServiceTypeHTTPS: true,
	realmmodel.ServiceTypeVNC:   true,
	realmmodel.ServiceTypeVPN:   true,
	realmmodel.ServiceTypeRDP:   true,
	realmmodel.ServiceTypeSSH:   true,
	realmmodel.ServiceTypeRTSP:  true,
}

func parseServiceParams(params json.RawMessage) (realmmodel.Service, error) {
	var svc realmmodel.Service
	if err := json.Unmarshal(params, &svc); err != nil {
		return realmmodel.Service{}, err
	}
	if svc.Name == "" || svc.Hostname == "" || svc.Port <= 0 {
		return realmmodel.Service{}, fmt.Errorf("please enter a name, a hostname, and a valid port")
	}
	if !validServiceTypes[svc.Type] {
		return realmmodel.Service{}, fmt.Errorf("invalid service type: %s", svc.Type)
	}
	return svc, nil
}

func handleRealmAddService(a *api, params json.RawMessage) (any, error) {
	svc, err := parseServiceParams(params)
	if err != nil {
		return nil, err
	}
	if a.serviceExists(svc.Name) {
		return nil, fmt.Errorf("a service named %q already exists", svc.Name)
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		c.Services = append(c.Services, svc)
	})
	if err != nil {
		return nil, err
	}
	realmannounce.AnnounceServiceNow(a.realmMapsFeature, cfg, svc)
	return realmConfigResponse(a, cfg), nil
}

func handleRealmUpdateService(a *api, params json.RawMessage) (any, error) {
	svc, err := parseServiceParams(params)
	if err != nil {
		return nil, err
	}
	if !a.serviceExists(svc.Name) {
		return nil, fmt.Errorf("no service named %q", svc.Name)
	}
	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		for i := range c.Services {
			if c.Services[i].Name == svc.Name {
				c.Services[i] = svc
				break
			}
		}
	})
	if err != nil {
		return nil, err
	}
	realmannounce.AnnounceServiceNow(a.realmMapsFeature, cfg, svc)
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
	realmannounce.RetractServiceNow(a.realmMapsFeature, cfg, p.Name)
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
	case realmmodel.ServiceTypeVPN:
		openErr = browseropen.OpenOpenVPN(port, a.realmEngine.ConnectedHosts(p.PeerId))
	case realmmodel.ServiceTypeRTSP:
		openErr = browseropen.OpenRTSP(port)
	default:
		opened = false
	}

	result := map[string]any{"localPort": port, "opened": opened && openErr == nil}
	if openErr != nil {
		result["error"] = openErr.Error()
	}
	return result, nil
}
