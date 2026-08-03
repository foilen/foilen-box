package model

import "time"

// PeerInfo is our app-level usage-data view of a group peer, persisted
// across restarts independently of go-libp2p's own in-memory peerstore.
// Hostname/Description/RelayServiceEnabled are self-reported on connect
// (Engine.onConnected) via Store.SetHostnameDescription, not preserved across an Upsert.
type PeerInfo struct {
	ID       string    `json:"id"`
	LastSeen time.Time `json:"lastSeen"`
	// Addresses is the merged, deduplicated view across all sources, by
	// Store's address source priority. Not set directly by callers.
	Addresses []string `json:"addresses"`
	// AddressesBySource holds each source's own last-reported addresses
	// (e.g. "mdns", "dht", "announce") so none clobber each other; Addresses is derived from this.
	AddressesBySource map[string][]string `json:"addressesBySource,omitempty"`
	GroupNames        []string            `json:"groupNames"`
	Connected         bool                `json:"connected"`
	Hostname          string              `json:"hostname"`
	Description       string              `json:"description"`
	// RelayServiceEnabled is whether this peer last reported running
	// cfg.EnableRelayService — worth trying as a relay candidate
	// (Engine.connectedRelayPeers) instead of blindly probing every peer.
	RelayServiceEnabled bool `json:"relayServiceEnabled"`
	// Version is the peer's self-reported application name and version,
	// e.g. "FoilenBox - abc1234" (see Engine.SetAppVersion).
	Version string `json:"version"`
}
