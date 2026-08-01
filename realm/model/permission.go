package model

// PermissionAction identifies a Realm action a peer or group can be granted
// the right to invoke against this machine. Unlike a closed enum, the set of
// valid actions isn't fixed by this package: each registered Feature
// contributes its own actions, namespaced by its name (e.g.
// "common/scripts/run"). See Engine.AvailableActions for the runtime
// catalog.
type PermissionAction string

// Permission grants a peer or group the right to invoke Action against this
// machine. Exactly one of PeerID or GroupName is set. Actions with no
// matching Permission are refused (deny-by-default).
type Permission struct {
	Action    PermissionAction `json:"action"`
	PeerID    string           `json:"peerId,omitempty"`
	GroupName string           `json:"groupName,omitempty"`
}
