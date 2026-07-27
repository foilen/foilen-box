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

// RealmMap is one shared key-value store: ScopeID is the public id of the
// group whose private key authorizes writes to it (see model.Group,
// model.KeyPair.ID), StoreName distinguishes multiple maps within the same
// group.
type RealmMap struct {
	ScopeID   string              `json:"scopeId"`
	StoreName string              `json:"storeName"`
	Entries   map[string]MapEntry `json:"entries"`
}

// RealmMapSummary is one list-view row: a (ScopeID, StoreName) pair with
// GroupName resolved for display (looked up against the locally-configured
// Config.Groups, since ScopeID alone isn't human-readable) and aggregate
// stats over its current entries.
type RealmMapSummary struct {
	ScopeID             string `json:"scopeId"`
	GroupName           string `json:"groupName"`
	StoreName           string `json:"storeName"`
	EntryCount          int    `json:"entryCount"`
	UpdatedAtUnixMillis int64  `json:"updatedAtUnixMillis"`
}

// MapEvent is one mutation, flat/list-shaped (as opposed to MapEntry, which
// is keyed externally by a RealmMap's Entries) — the on-disk shape of each
// map's event-log file and the unsigned payload EventsSince returns.
type MapEvent struct {
	ScopeID             string `json:"scopeId"`
	StoreName           string `json:"storeName"`
	Key                 string `json:"key"`
	Value               string `json:"value,omitempty"`
	Deleted             bool   `json:"deleted,omitempty"`
	UpdatedAtUnixMillis int64  `json:"updatedAtUnixMillis"`
	OriginPeerID        string `json:"originPeerId"`
}

// SigningBytes returns the canonical bytes signed/verified for the event,
// mirroring NotificationEnvelope.SigningBytes.
func (e MapEvent) SigningBytes() []byte {
	deleted := "0"
	if e.Deleted {
		deleted = "1"
	}
	return []byte(fmt.Sprintf("%s|%s|%s|%s|%s|%d|%s", e.ScopeID, e.StoreName, e.Key, e.Value, deleted, e.UpdatedAtUnixMillis, e.OriginPeerID))
}

// MapEventEnvelope is a MapEvent plus its signature (made with the scope
// group's private key — every member holds it, so a valid signature both
// proves and is the sole check for write authorization), sent both over the
// live push stream and inside a sync response.
type MapEventEnvelope struct {
	MapEvent
	Signature []byte `json:"signature"`
}
