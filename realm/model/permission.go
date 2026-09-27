package model

type PermissionAction string

type Permission struct {
	Action    PermissionAction `json:"action"`
	PeerID    string           `json:"peerId,omitempty"`
	GroupName string           `json:"groupName,omitempty"`
}
