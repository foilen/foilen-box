package model

import "fmt"

// MapEntry is the current value of a key inside a RealmMap. Deleted is a
// tombstone (not a removed key) so deletes replicate and merge
// last-write-wins like any other mutation.
//
// Nonce and IdentitySignature are set only when the map is encrypted (see
// RealmMapConfig.Encryption): Value is then ciphertext, the external key is
// hash(identityId+realKey), Nonce is the secretbox nonce, and
// IdentitySignature is an Ed25519 signature (identity's private key, over
// EncryptedSigningBytes) proving an identity holder produced it.
type MapEntry struct {
	Value               string `json:"value"`
	Deleted             bool   `json:"deleted,omitempty"`
	UpdatedAtUnixMillis int64  `json:"updatedAtUnixMillis"`
	OriginPeerID        string `json:"originPeerId"`
	Nonce               string `json:"nonce,omitempty"`
	IdentitySignature   string `json:"identitySignature,omitempty"`
}

// RealmMap is one shared key-value store: GroupID is the group whose private
// key authorizes writes; StoreName distinguishes maps within the same group.
type RealmMap struct {
	GroupID   string              `json:"groupId"`
	StoreName string              `json:"storeName"`
	Entries   map[string]MapEntry `json:"entries"`
}

// RealmMapConfig is the per-map settings blob stored in the reserved
// _realmMaps config store (features/maps.SystemConfigStoreName), never
// itself encrypted — every group member can see whether a map is encrypted
// and by which identity, without being able to read it.
type RealmMapConfig struct {
	AutoDeleteEntriesHours int64                `json:"autoDeleteEntriesHours,omitempty"`
	Encryption             *MapEncryptionConfig `json:"encryption,omitempty"`
}

// MapEncryptionConfig makes a map's entries confidential to one Identity:
// EncryptedSymmetricKey is the map's random symmetric key, sealed
// (libsodium crypto_box_seal-style) to that identity's public key — only its
// private key holder can recover it.
type MapEncryptionConfig struct {
	IdentityID            string `json:"identityId"`
	EncryptedSymmetricKey string `json:"encryptedSymmetricKey"`
}

// RealmMapSummary is one list-view row: a (GroupID, StoreName) pair with
// GroupName resolved for display and aggregate stats over current entries.
type RealmMapSummary struct {
	GroupID                string `json:"groupId"`
	GroupName              string `json:"groupName"`
	StoreName              string `json:"storeName"`
	EntryCount             int    `json:"entryCount"`
	UpdatedAtUnixMillis    int64  `json:"updatedAtUnixMillis"`
	AutoDeleteEntriesHours int64  `json:"autoDeleteEntriesHours,omitempty"`
	EncryptionIdentityID   string `json:"encryptionIdentityId,omitempty"`
}

// MapEvent is one mutation, flat/list-shaped (unlike MapEntry, keyed by
// RealmMap.Entries) — the on-disk event-log shape and EventsSinceForStore's
// unsigned payload. See MapEntry for Nonce/IdentitySignature.
type MapEvent struct {
	GroupID             string `json:"groupId"`
	StoreName           string `json:"storeName"`
	Key                 string `json:"key"`
	Value               string `json:"value,omitempty"`
	Deleted             bool   `json:"deleted,omitempty"`
	UpdatedAtUnixMillis int64  `json:"updatedAtUnixMillis"`
	OriginPeerID        string `json:"originPeerId"`
	Nonce               string `json:"nonce,omitempty"`
	IdentitySignature   string `json:"identitySignature,omitempty"`
}

// SigningBytes returns the canonical bytes signed/verified with the group's
// key, including Nonce/IdentitySignature so neither can be swapped onto a
// different event without invalidating the signature.
func (e MapEvent) SigningBytes() []byte {
	deleted := "0"
	if e.Deleted {
		deleted = "1"
	}
	return []byte(fmt.Sprintf("%s|%s|%s|%s|%s|%s|%d|%s|%s", e.GroupID, e.StoreName, e.Key, e.Value, e.Nonce, deleted, e.UpdatedAtUnixMillis, e.OriginPeerID, e.IdentitySignature))
}

// EncryptedSigningBytes is SigningBytes minus IdentitySignature, which
// doesn't exist yet when the identity signs.
func (e MapEvent) EncryptedSigningBytes() []byte {
	deleted := "0"
	if e.Deleted {
		deleted = "1"
	}
	return []byte(fmt.Sprintf("%s|%s|%s|%s|%s|%s|%d|%s", e.GroupID, e.StoreName, e.Key, e.Value, e.Nonce, deleted, e.UpdatedAtUnixMillis, e.OriginPeerID))
}

// MapEventEnvelope is a MapEvent plus its group-key signature — the sole
// check for write authorization — sent over the live push stream and inside
// subscribe responses.
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
// nil for EntryAdded; New is nil for EntryDeleted (the tombstone itself
// isn't a value a listener should see).
type ChangeEvent struct {
	GroupID   string
	StoreName string
	Type      ChangeType
	Key       string
	Old       *MapEntry
	New       *MapEntry
}
