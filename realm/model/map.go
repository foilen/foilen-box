package model

import "fmt"

// MapEntry is the current value of a single key inside a RealmMap, keyed
// externally by RealmMap.Entries. Deleted is a tombstone (rather than
// simply removing the key) so a delete can itself be replicated to peers
// and merged with last-write-wins, the same as any other mutation.
type MapEntry struct {
	Value               string `json:"value"`
	Deleted             bool   `json:"deleted,omitempty"`
	UpdatedAtUnixMillis int64  `json:"updatedAtUnixMillis"`
	OriginPeerID        string `json:"originPeerId"`
}

// RealmMap is one shared key-value store: GroupID is the public id of the
// group whose private key authorizes writes to it (see model.Group,
// model.KeyPair.ID), StoreName distinguishes multiple maps within the same
// group.
type RealmMap struct {
	GroupID   string              `json:"groupId"`
	StoreName string              `json:"storeName"`
	Entries   map[string]MapEntry `json:"entries"`
}

// RealmMapConfig is the per-map settings blob stored as the Value of each
// entry in the reserved _realmMaps config store (see
// features/maps.SystemConfigStoreName) — one entry per map name.
type RealmMapConfig struct {
	AutoDeleteEntriesHours int64 `json:"autoDeleteEntriesHours,omitempty"`
}

// RealmMapSummary is one list-view row: a (GroupID, StoreName) pair with
// GroupName resolved for display (looked up against the locally-configured
// Config.Groups, since GroupID alone isn't human-readable) and aggregate
// stats over its current entries.
type RealmMapSummary struct {
	GroupID                string `json:"groupId"`
	GroupName              string `json:"groupName"`
	StoreName              string `json:"storeName"`
	EntryCount             int    `json:"entryCount"`
	UpdatedAtUnixMillis    int64  `json:"updatedAtUnixMillis"`
	AutoDeleteEntriesHours int64  `json:"autoDeleteEntriesHours,omitempty"`
}

// MapEvent is one mutation, flat/list-shaped (as opposed to MapEntry, which
// is keyed externally by a RealmMap's Entries) — the on-disk shape of each
// map's event-log file and the unsigned payload EventsSinceForStore returns.
type MapEvent struct {
	GroupID             string `json:"groupId"`
	StoreName           string `json:"storeName"`
	Key                 string `json:"key"`
	Value               string `json:"value,omitempty"`
	Deleted             bool   `json:"deleted,omitempty"`
	UpdatedAtUnixMillis int64  `json:"updatedAtUnixMillis"`
	OriginPeerID        string `json:"originPeerId"`
}

// SigningBytes returns the canonical bytes signed/verified for the event.
func (e MapEvent) SigningBytes() []byte {
	deleted := "0"
	if e.Deleted {
		deleted = "1"
	}
	return []byte(fmt.Sprintf("%s|%s|%s|%s|%s|%d|%s", e.GroupID, e.StoreName, e.Key, e.Value, deleted, e.UpdatedAtUnixMillis, e.OriginPeerID))
}

// MapEventEnvelope is a MapEvent plus its signature (made with the group's
// private key — every member holds it, so a valid signature both proves and
// is the sole check for write authorization), sent both over the live push
// stream and inside a subscribe response.
type MapEventEnvelope struct {
	MapEvent
	Signature []byte `json:"signature"`
}

// ChangeType describes what kind of observable change a ChangeEvent
// represents.
type ChangeType int

const (
	EntryAdded ChangeType = iota
	EntryUpdated
	EntryDeleted
)

// ChangeEvent describes one observable change to a RealmMap entry. Old is
// nil for EntryAdded (nothing existed before); New is nil for EntryDeleted
// (nothing is visible after) — a tombstone is still stored internally, but
// is not itself a value a listener should see.
type ChangeEvent struct {
	GroupID   string
	StoreName string
	Type      ChangeType
	Key       string
	Old       *MapEntry
	New       *MapEntry
}
