package webserver

import (
	"encoding/json"
	"fmt"
)

// mapEntryResult is the wire shape of one key-value entry, keyed by its map
// key in mapResult.Entries.
type mapEntryResult struct {
	Value               string `json:"value"`
	UpdatedAtUnixMillis int64  `json:"updatedAtUnixMillis"`
	OriginPeerID        string `json:"originPeerId"`
}

type mapResult struct {
	ScopeID   string                    `json:"scopeId"`
	StoreName string                    `json:"storeName"`
	Entries   map[string]mapEntryResult `json:"entries"`
}

type mapSummaryResult struct {
	ScopeID             string `json:"scopeId"`
	GroupName           string `json:"groupName"`
	StoreName           string `json:"storeName"`
	EntryCount          int    `json:"entryCount"`
	UpdatedAtUnixMillis int64  `json:"updatedAtUnixMillis"`
}

func handleRealmListMaps(a *api, _ json.RawMessage) (any, error) {
	summaries := a.realmMapsFeature.ListSummaries()
	result := make([]mapSummaryResult, 0, len(summaries))
	for _, s := range summaries {
		result = append(result, mapSummaryResult{
			ScopeID:             s.ScopeID,
			GroupName:           s.GroupName,
			StoreName:           s.StoreName,
			EntryCount:          s.EntryCount,
			UpdatedAtUnixMillis: s.UpdatedAtUnixMillis,
		})
	}
	return map[string]any{"maps": result}, nil
}

func handleRealmGetMap(a *api, params json.RawMessage) (any, error) {
	var p struct {
		ScopeID   string `json:"scopeId"`
		StoreName string `json:"storeName"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.ScopeID == "" || p.StoreName == "" {
		return nil, fmt.Errorf("scopeId and storeName are required")
	}
	rm := a.realmMapsFeature.GetMap(p.ScopeID, p.StoreName)
	entries := make(map[string]mapEntryResult, len(rm.Entries))
	for k, e := range rm.Entries {
		entries[k] = mapEntryResult{Value: e.Value, UpdatedAtUnixMillis: e.UpdatedAtUnixMillis, OriginPeerID: e.OriginPeerID}
	}
	return mapResult{ScopeID: rm.ScopeID, StoreName: rm.StoreName, Entries: entries}, nil
}

func handleRealmCreateMap(a *api, params json.RawMessage) (any, error) {
	var p struct {
		ScopeID   string `json:"scopeId"`
		StoreName string `json:"storeName"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.ScopeID == "" || p.StoreName == "" {
		return nil, fmt.Errorf("please select a group and enter a store name")
	}
	if err := a.realmMapsFeature.CreateMap(p.ScopeID, p.StoreName); err != nil {
		return nil, err
	}
	return handleRealmListMaps(a, nil)
}

func handleRealmSetMapValue(a *api, params json.RawMessage) (any, error) {
	var p struct {
		ScopeID   string `json:"scopeId"`
		StoreName string `json:"storeName"`
		Key       string `json:"key"`
		Value     string `json:"value"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.ScopeID == "" || p.StoreName == "" || p.Key == "" {
		return nil, fmt.Errorf("scopeId, storeName, and key are required")
	}
	if err := a.realmMapsFeature.SetValue(p.ScopeID, p.StoreName, p.Key, p.Value); err != nil {
		return nil, err
	}
	return handleRealmGetMap(a, params)
}

func handleRealmDeleteMapValue(a *api, params json.RawMessage) (any, error) {
	var p struct {
		ScopeID   string `json:"scopeId"`
		StoreName string `json:"storeName"`
		Key       string `json:"key"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.ScopeID == "" || p.StoreName == "" || p.Key == "" {
		return nil, fmt.Errorf("scopeId, storeName, and key are required")
	}
	if err := a.realmMapsFeature.DeleteValue(p.ScopeID, p.StoreName, p.Key); err != nil {
		return nil, err
	}
	return handleRealmGetMap(a, params)
}

func handleRealmDeleteMap(a *api, params json.RawMessage) (any, error) {
	var p struct {
		ScopeID   string `json:"scopeId"`
		StoreName string `json:"storeName"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.ScopeID == "" || p.StoreName == "" {
		return nil, fmt.Errorf("scopeId and storeName are required")
	}
	if err := a.realmMapsFeature.DeleteMap(p.ScopeID, p.StoreName); err != nil {
		return nil, err
	}
	return handleRealmListMaps(a, nil)
}
