package model

import "time"

type PeerSpec struct {
	PeerID    string    `json:"peerId"`
	Text      string    `json:"text"`
	OS        string    `json:"os,omitempty"`
	CPU       string    `json:"cpu,omitempty"`
	Mem       string    `json:"mem,omitempty"`
	Battery   string    `json:"battery,omitempty"`
	GPU       string    `json:"gpu,omitempty"`
	Disk      string    `json:"disk,omitempty"`
	FetchedAt time.Time `json:"fetchedAt"`
}
