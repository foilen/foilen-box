package model

import "fmt"

type MapEntry struct {
	Value               string `json:"value"`
	Deleted             bool   `json:"deleted,omitempty"`
	UpdatedAtUnixMillis int64  `json:"updatedAtUnixMillis"`
	OriginPeerID        string `json:"originPeerId"`
	Nonce               string `json:"nonce,omitempty"`
	IdentitySignature   string `json:"identitySignature,omitempty"`
}

type RealmMap struct {
	GroupID   string              `json:"groupId"`
	StoreName string              `json:"storeName"`
	Entries   map[string]MapEntry `json:"entries"`
}

type RealmMapConfig struct {
	AutoDeleteEntriesHours int64                `json:"autoDeleteEntriesHours,omitempty"`
	Encryption             *MapEncryptionConfig `json:"encryption,omitempty"`
}

type MapEncryptionConfig struct {
	IdentityID            string `json:"identityId"`
	EncryptedSymmetricKey string `json:"encryptedSymmetricKey"`
}

type RealmMapSummary struct {
	GroupID                string `json:"groupId"`
	GroupName              string `json:"groupName"`
	StoreName              string `json:"storeName"`
	EntryCount             int    `json:"entryCount"`
	UpdatedAtUnixMillis    int64  `json:"updatedAtUnixMillis"`
	AutoDeleteEntriesHours int64  `json:"autoDeleteEntriesHours,omitempty"`
	EncryptionIdentityID   string `json:"encryptionIdentityId,omitempty"`
}

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

func (e MapEvent) SigningBytes() []byte {
	deleted := "0"
	if e.Deleted {
		deleted = "1"
	}
	return []byte(fmt.Sprintf("%s|%s|%s|%s|%s|%s|%d|%s|%s", e.GroupID, e.StoreName, e.Key, e.Value, e.Nonce, deleted, e.UpdatedAtUnixMillis, e.OriginPeerID, e.IdentitySignature))
}

func (e MapEvent) EncryptedSigningBytes() []byte {
	deleted := "0"
	if e.Deleted {
		deleted = "1"
	}
	return []byte(fmt.Sprintf("%s|%s|%s|%s|%s|%s|%d|%s", e.GroupID, e.StoreName, e.Key, e.Value, e.Nonce, deleted, e.UpdatedAtUnixMillis, e.OriginPeerID))
}

type MapEventEnvelope struct {
	MapEvent
	Signature []byte `json:"signature"`
}

type ChangeType int

const (
	EntryAdded ChangeType = iota
	EntryUpdated
	EntryDeleted
)

type ChangeEvent struct {
	GroupID   string
	StoreName string
	Type      ChangeType
	Key       string
	Old       *MapEntry
	New       *MapEntry
}
