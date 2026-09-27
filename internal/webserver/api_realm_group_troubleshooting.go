package webserver

import (
	"encoding/json"
	"fmt"

	grouptroubleshooting "foilen-box/internal/grouptroubleshooting"
)

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
