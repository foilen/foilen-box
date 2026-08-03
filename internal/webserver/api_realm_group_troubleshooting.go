package webserver

import (
	"encoding/json"
	"fmt"

	grouptroubleshooting "foilen-box/internal/grouptroubleshooting"
)

// handleGroupTroubleshootingStart starts a fixed-length "Group Troubleshooting"
// session for a group (see internal/grouptroubleshooting), erroring if one is
// already running. The resulting "common" map (with its fresh
// groupTroubleshooting/expiration entry) is returned the same shape as
// realm.getMap so the UI can render it immediately.
func handleGroupTroubleshootingStart(a *api, params json.RawMessage) (any, error) {
	var p struct {
		GroupID string `json:"groupId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.GroupID == "" {
		return nil, fmt.Errorf("please select a group")
	}
	if err := a.realmGroupTroubleshooting.StartSession(p.GroupID); err != nil {
		return nil, err
	}
	getParams, err := json.Marshal(struct {
		GroupID   string `json:"groupId"`
		StoreName string `json:"storeName"`
	}{GroupID: p.GroupID, StoreName: grouptroubleshooting.CommonStoreName})
	if err != nil {
		return nil, err
	}
	return handleRealmGetMap(a, getParams)
}
