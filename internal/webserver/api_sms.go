package webserver

import (
	"encoding/json"
	"fmt"
	"log"

	boxsms "foilen-box/internal/sms"

	realmmodel "foilen-realm/model"
)

// smsConfigResult is the wire shape of this device's local SMS management
// config (internal/sms.Config).
type smsConfigResult struct {
	Enabled   bool   `json:"enabled"`
	GroupID   string `json:"groupId"`
	StoreName string `json:"storeName"`
}

func smsConfigToResult(cfg boxsms.Config) smsConfigResult {
	return smsConfigResult{Enabled: cfg.Enabled, GroupID: cfg.GroupID, StoreName: cfg.StoreName}
}

func handleSmsLoadConfig(a *api, _ json.RawMessage) (any, error) {
	return smsConfigToResult(a.smsConfig.Load()), nil
}

// handleSmsSaveManagementConfig saves this device's SMS management config
// (Android-only, but not enforced here — the config UI is hidden on
// desktop). If createNew, a new "SMS-<suffix>" realmmap is created first;
// otherwise storeName selects an existing one. On the disabled->enabled
// transition, a full SMS history import is kicked off in the background.
func handleSmsSaveManagementConfig(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Enabled                bool   `json:"enabled"`
		GroupID                string `json:"groupId"`
		StoreName              string `json:"storeName"`
		CreateNew              bool   `json:"createNew"`
		Suffix                 string `json:"suffix"`
		IdentityID             string `json:"identityId"`
		AutoDeleteEntriesHours int64  `json:"autoDeleteEntriesHours"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.GroupID == "" {
		return nil, fmt.Errorf("please select a group")
	}

	storeName := p.StoreName
	if p.CreateNew {
		if p.Suffix == "" {
			return nil, fmt.Errorf("please enter a suffix")
		}
		if p.IdentityID == "" {
			return nil, fmt.Errorf("please select an identity to encrypt to — SMS content is never stored unencrypted")
		}
		storeName = boxsms.StoreNameFor(p.Suffix)
		config := realmmodel.RealmMapConfig{AutoDeleteEntriesHours: p.AutoDeleteEntriesHours}
		if err := a.realmMapsFeature.CreateMap(p.GroupID, storeName, config, p.IdentityID); err != nil {
			return nil, err
		}
	}
	if storeName == "" {
		return nil, fmt.Errorf("please select or create a store")
	}
	if !p.CreateNew && a.realmMapsFeature.EncryptionIdentityID(p.GroupID, storeName) == "" {
		return nil, fmt.Errorf("that store isn't encrypted to an identity — SMS content is never stored unencrypted")
	}

	previous := a.smsConfig.Load()
	newCfg := boxsms.Config{Enabled: p.Enabled, GroupID: p.GroupID, StoreName: storeName}
	if err := a.smsConfig.Save(newCfg); err != nil {
		return nil, err
	}
	a.realmSms.SyncEnabledMarker(previous, newCfg)

	if newCfg.Enabled && !(previous.Enabled && previous.GroupID == newCfg.GroupID && previous.StoreName == newCfg.StoreName) {
		groupID, storeNameCopy := newCfg.GroupID, newCfg.StoreName
		go func() {
			if err := a.realmSms.ImportHistory(groupID, storeNameCopy); err != nil {
				log.Printf("sms: history import failed: %v", err)
			}
		}()
	}

	return smsConfigToResult(newCfg), nil
}

// smsStoreResult is mapSummaryResult plus the peers currently managing this
// store (see boxsms.Manager.EnabledPeerIDs) — SMS-specific, so kept separate
// from the generic Maps tab's own wire shape rather than added there.
type smsStoreResult struct {
	mapSummaryResult
	EnabledPeerIds []string `json:"enabledPeerIds"`
}

func handleSmsListStores(a *api, _ json.RawMessage) (any, error) {
	summaries := a.realmMapsFeature.ListSummaries()
	result := make([]smsStoreResult, 0, len(summaries))
	for _, s := range summaries {
		if !boxsms.IsSmsStore(s.StoreName) {
			continue
		}
		result = append(result, smsStoreResult{
			mapSummaryResult: mapSummaryResult{
				GroupID:                s.GroupID,
				GroupName:              s.GroupName,
				StoreName:              s.StoreName,
				EntryCount:             s.EntryCount,
				UpdatedAtUnixMillis:    s.UpdatedAtUnixMillis,
				AutoDeleteEntriesHours: s.AutoDeleteEntriesHours,
				EncryptionIdentityID:   s.EncryptionIdentityID,
			},
			EnabledPeerIds: a.realmSms.EnabledPeerIDs(s.GroupID, s.StoreName),
		})
	}
	return map[string]any{"stores": result}, nil
}

func handleSmsListConversations(a *api, params json.RawMessage) (any, error) {
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
	conversations, encrypted, available := a.realmSms.ListConversations(p.GroupID, p.StoreName)
	return map[string]any{
		"conversations":       conversations,
		"encrypted":           encrypted,
		"encryptionAvailable": available,
	}, nil
}

func handleSmsListMessages(a *api, params json.RawMessage) (any, error) {
	var p struct {
		GroupID     string `json:"groupId"`
		StoreName   string `json:"storeName"`
		PhoneNumber string `json:"phoneNumber"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.GroupID == "" || p.StoreName == "" || p.PhoneNumber == "" {
		return nil, fmt.Errorf("groupId, storeName, and phoneNumber are required")
	}
	messages, encrypted, available := a.realmSms.ListMessages(p.GroupID, p.StoreName, p.PhoneNumber)
	return map[string]any{
		"messages":            messages,
		"encrypted":           encrypted,
		"encryptionAvailable": available,
	}, nil
}

func handleSmsSendMessage(a *api, params json.RawMessage) (any, error) {
	var p struct {
		GroupID     string `json:"groupId"`
		StoreName   string `json:"storeName"`
		PeerID      string `json:"peerId"`
		PhoneNumber string `json:"phoneNumber"`
		Body        string `json:"body"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if err := a.realmSms.RequestSend(p.GroupID, p.StoreName, p.PeerID, p.PhoneNumber, p.Body); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}
