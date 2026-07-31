package model

import "time"

// PeerSpec is a peer's self-reported system information report (see
// internal/spec.Report), captured the last time we successfully asked for
// it over the spec libp2p protocol. CPU/Mem/Battery/GPU/Disk are the same
// data in compact, one-line-per-field form (see internal/spec.Summary), for
// display in a table without parsing Text.
type PeerSpec struct {
	PeerID    string    `json:"peerId"`
	Text      string    `json:"text"`
	CPU       string    `json:"cpu,omitempty"`
	Mem       string    `json:"mem,omitempty"`
	Battery   string    `json:"battery,omitempty"`
	GPU       string    `json:"gpu,omitempty"`
	Disk      string    `json:"disk,omitempty"`
	FetchedAt time.Time `json:"fetchedAt"`
}
