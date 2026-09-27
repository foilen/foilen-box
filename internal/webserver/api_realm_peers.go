package webserver

import (
	"encoding/json"
	"fmt"
	"time"
)

type peerResult struct {
	ID                  string    `json:"id"`
	Hostname            string    `json:"hostname"`
	Description         string    `json:"description"`
	LastSeen            time.Time `json:"lastSeen"`
	Addresses           []string  `json:"addresses"`
	GroupNames          []string  `json:"groupNames"`
	Connected           bool      `json:"connected"`
	ConnectedAddresses  []string  `json:"connectedAddresses"`
	RelayServiceEnabled bool      `json:"relayServiceEnabled"`
	Version             string    `json:"version"`
	MainPeer            bool      `json:"mainPeer"`
}

func handleRealmListPeers(a *api, _ json.RawMessage) (any, error) {
	list := a.realmPeers.List()
	result := make([]peerResult, 0, len(list))
	for _, p := range list {
		result = append(result, peerResult{
			ID:                  p.ID,
			Hostname:            p.Hostname,
			Description:         p.Description,
			LastSeen:            p.LastSeen,
			Addresses:           p.Addresses,
			GroupNames:          p.GroupNames,
			Connected:           p.Connected,
			ConnectedAddresses:  a.realmEngine.ConnectedAddresses(p.ID),
			RelayServiceEnabled: p.RelayServiceEnabled,
			Version:             p.Version,
			MainPeer:            a.realmEngine.IsRingNeighbor(p.ID),
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

func handleRealmClearPeerAddresses(a *api, params json.RawMessage) (any, error) {
	var p struct {
		PeerId string `json:"peerId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.PeerId == "" {
		return nil, fmt.Errorf("please select a peer")
	}
	if !a.realmPeers.ClearDiscoveredAddresses(p.PeerId) {
		return nil, fmt.Errorf("unknown peer %q", p.PeerId)
	}
	return map[string]any{"ok": true}, nil
}

func handleRealmClearAllPeerAddresses(a *api, _ json.RawMessage) (any, error) {
	a.realmPeers.ClearAllDiscoveredAddresses()
	return map[string]any{"ok": true}, nil
}

func handleRealmForcePeriodicTick(a *api, _ json.RawMessage) (any, error) {
	a.realmEngine.RunPeriodicNow()
	return map[string]any{"ok": true}, nil
}

func handleRealmDeletePeer(a *api, params json.RawMessage) (any, error) {
	var p struct {
		PeerId string `json:"peerId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.PeerId == "" {
		return nil, fmt.Errorf("please select a peer")
	}
	if err := a.realmEngine.RemovePeer(p.PeerId); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}
