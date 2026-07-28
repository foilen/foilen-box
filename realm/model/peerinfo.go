package model

import "time"

// PeerInfo is our app-level usage-data view of a group peer, persisted
// across restarts independently of go-libp2p's own (in-memory) peerstore.
// Hostname, Description and RelayServiceEnabled are the peer's self-reported
// identity info, fetched automatically whenever the peer connects (see
// Engine.onConnected); they are not preserved across an Upsert (e.g. a
// fresh discovery callback), only set via Store.SetHostnameDescription.
type PeerInfo struct {
	ID       string    `json:"id"`
	LastSeen time.Time `json:"lastSeen"`
	// Addresses is the merged view across all sources, ordered by
	// Store's address source priority (LAN-discovered first) and
	// deduplicated; see Store.Upsert. Not set directly by callers.
	Addresses []string `json:"addresses"`
	// AddressesBySource holds each source's own last-reported address
	// list (e.g. "mdns", "dht", "announce"), so one source's discovery
	// results never clobber another's. Addresses is derived from this.
	AddressesBySource map[string][]string `json:"addressesBySource,omitempty"`
	GroupNames        []string            `json:"groupNames"`
	Connected         bool                `json:"connected"`
	Hostname          string              `json:"hostname"`
	Description       string              `json:"description"`
	// RelayServiceEnabled is whether this peer last reported running with
	// cfg.EnableRelayService, i.e. whether it's worth trying it as a
	// circuit-relay-v2 candidate (see Engine.relayPeerSource) instead of
	// blindly probing every known peer.
	RelayServiceEnabled bool `json:"relayServiceEnabled"`
}
