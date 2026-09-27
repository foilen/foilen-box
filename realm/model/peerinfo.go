package model

import "time"

type PeerInfo struct {
	ID       string    `json:"id"`
	LastSeen time.Time `json:"lastSeen"`

	Addresses []string `json:"addresses"`

	AddressesBySource map[string][]string `json:"addressesBySource,omitempty"`
	GroupNames        []string            `json:"groupNames"`
	Connected         bool                `json:"connected"`
	Hostname          string              `json:"hostname"`
	Description       string              `json:"description"`

	RelayServiceEnabled bool `json:"relayServiceEnabled"`

	Version string `json:"version"`
}
