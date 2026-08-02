package webserver

import (
	"encoding/json"
	"fmt"

	realmmodel "foilen-realm/model"
)

// mapEntryResult is the wire shape of one key-value entry, keyed by its map
// key in mapResult.Entries.
type mapEntryResult struct {
	Value               string `json:"value"`
	UpdatedAtUnixMillis int64  `json:"updatedAtUnixMillis"`
	OriginPeerID        string `json:"originPeerId"`
}

type mapResult struct {
	GroupID              string                    `json:"groupId"`
	StoreName            string                    `json:"storeName"`
	Entries              map[string]mapEntryResult `json:"entries"`
	Encrypted            bool                      `json:"encrypted"`
	EncryptionAvailable  bool                      `json:"encryptionAvailable"`
	EncryptionIdentityID string                    `json:"encryptionIdentityId,omitempty"`
}

type mapSummaryResult struct {
	GroupID                string `json:"groupId"`
	GroupName              string `json:"groupName"`
	StoreName              string `json:"storeName"`
	EntryCount             int    `json:"entryCount"`
	UpdatedAtUnixMillis    int64  `json:"updatedAtUnixMillis"`
	AutoDeleteEntriesHours int64  `json:"autoDeleteEntriesHours"`
	EncryptionIdentityID   string `json:"encryptionIdentityId,omitempty"`
}

func handleRealmListMaps(a *api, _ json.RawMessage) (any, error) {
	summaries := a.realmMapsFeature.ListSummaries()
	result := make([]mapSummaryResult, 0, len(summaries))
	for _, s := range summaries {
		result = append(result, mapSummaryResult{
			GroupID:                s.GroupID,
			GroupName:              s.GroupName,
			StoreName:              s.StoreName,
			EntryCount:             s.EntryCount,
			UpdatedAtUnixMillis:    s.UpdatedAtUnixMillis,
			AutoDeleteEntriesHours: s.AutoDeleteEntriesHours,
			EncryptionIdentityID:   s.EncryptionIdentityID,
		})
	}
	return map[string]any{"maps": result}, nil
}

func handleRealmGetMap(a *api, params json.RawMessage) (any, error) {
	var p struct {
		GroupID   string `json:"groupId"`
		StoreName string `json:"storeName"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.GroupID == "" || p.StoreName == "" {
		return nil, fmt.Errorf("groupId and storeName are required")
	}
	rm, encrypted, available := a.realmMapsFeature.GetMap(p.GroupID, p.StoreName)
	entries := make(map[string]mapEntryResult, len(rm.Entries))
	for k, e := range rm.Entries {
		entries[k] = mapEntryResult{Value: e.Value, UpdatedAtUnixMillis: e.UpdatedAtUnixMillis, OriginPeerID: e.OriginPeerID}
	}
	result := mapResult{GroupID: rm.GroupID, StoreName: rm.StoreName, Entries: entries, Encrypted: encrypted, EncryptionAvailable: available}
	if encrypted {
		result.EncryptionIdentityID = a.realmMapsFeature.EncryptionIdentityID(p.GroupID, p.StoreName)
	}
	return result, nil
}

func handleRealmCreateMap(a *api, params json.RawMessage) (any, error) {
	var p struct {
		GroupID                string `json:"groupId"`
		StoreName              string `json:"storeName"`
		AutoDeleteEntriesHours int64  `json:"autoDeleteEntriesHours"`
		EncryptToIdentityID    string `json:"encryptToIdentityId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.GroupID == "" || p.StoreName == "" {
		return nil, fmt.Errorf("please select a group and enter a store name")
	}
	config := realmmodel.RealmMapConfig{AutoDeleteEntriesHours: p.AutoDeleteEntriesHours}
	if err := a.realmMapsFeature.CreateMap(p.GroupID, p.StoreName, config, p.EncryptToIdentityID); err != nil {
		return nil, err
	}
	return handleRealmListMaps(a, nil)
}

func handleRealmSetMapValue(a *api, params json.RawMessage) (any, error) {
	var p struct {
		GroupID   string `json:"groupId"`
		StoreName string `json:"storeName"`
		Key       string `json:"key"`
		Value     string `json:"value"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.GroupID == "" || p.StoreName == "" || p.Key == "" {
		return nil, fmt.Errorf("groupId, storeName, and key are required")
	}
	if err := a.realmMapsFeature.SetValue(p.GroupID, p.StoreName, p.Key, p.Value); err != nil {
		return nil, err
	}
	return handleRealmGetMap(a, params)
}

func handleRealmDeleteMapValue(a *api, params json.RawMessage) (any, error) {
	var p struct {
		GroupID   string `json:"groupId"`
		StoreName string `json:"storeName"`
		Key       string `json:"key"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.GroupID == "" || p.StoreName == "" || p.Key == "" {
		return nil, fmt.Errorf("groupId, storeName, and key are required")
	}
	if err := a.realmMapsFeature.DeleteValue(p.GroupID, p.StoreName, p.Key); err != nil {
		return nil, err
	}
	return handleRealmGetMap(a, params)
}

func handleRealmDeleteMap(a *api, params json.RawMessage) (any, error) {
	var p struct {
		GroupID   string `json:"groupId"`
		StoreName string `json:"storeName"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.GroupID == "" || p.StoreName == "" {
		return nil, fmt.Errorf("groupId and storeName are required")
	}
	if err := a.realmMapsFeature.DeleteMap(p.GroupID, p.StoreName); err != nil {
		return nil, err
	}
	return handleRealmListMaps(a, nil)
}
