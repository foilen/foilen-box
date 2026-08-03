// Package grouptroubleshooting implements the "Group Troubleshooting" Realm
// subtab: a time-boxed session that visualizes which peers of a group are
// currently connected to each other (direct vs relay). It piggybacks on each
// group's existing "common" realmmap (every group member already subscribes
// to it, see realm/features/maps.Feature.onPeerAvailable) under a
// "groupTroubleshooting/" key prefix, rather than owning a dedicated map:
// starting a session is then just an event ("groupTroubleshooting/expiration"
// moves into the future"), and entries are left in place afterward as a
// record of the last run — no map lifecycle to manage.
package grouptroubleshooting

// CommonStoreName is the realmmap store this package reads and writes into,
// shared with realm/features/announce and the specs/scripts/services subtabs.
const CommonStoreName = "common"

// keyPrefix namespaces every key this package owns inside CommonStoreName.
const keyPrefix = "groupTroubleshooting/"

// expirationKey holds the JSON-encoded Expiration entry that marks a session
// active and tells every member when to stop.
const expirationKey = keyPrefix + "expiration"

// connectionsKeyPrefix/Suffix bracket a peer id to build its
// "groupTroubleshooting/peer/<peerId>/connections" entry key (see
// connectionsKey).
const (
	connectionsKeyPrefix = keyPrefix + "peer/"
	connectionsKeySuffix = "/connections"
)

// Expiration is the JSON value of the expirationKey entry.
type Expiration struct {
	ExpiresAtUnixMillis int64 `json:"expiresAtUnixMillis"`
}

// Connection is one element of a connections entry's JSON array: one open
// connection from the owning peer to RemotePeerID over Address.
type Connection struct {
	RemotePeerID string `json:"remotePeerId"`
	Address      string `json:"address"`
}

// connectionsKey builds the key for peerID's own connections entry.
func connectionsKey(peerID string) string {
	return connectionsKeyPrefix + peerID + connectionsKeySuffix
}
