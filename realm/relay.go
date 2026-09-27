package realm

import (
	"github.com/libp2p/go-libp2p/core/peer"

	"foilen-realm/model"
)

func (e *Engine) relayServiceEnabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg.EnableRelayService
}

func (e *Engine) peerInCommonGroup(id peer.ID) bool {
	info, ok := e.peers.Get(id.String())
	if !ok {
		return false
	}
	e.mu.Lock()
	groups := e.cfg.Groups
	e.mu.Unlock()
	return hasCommonGroup(info.GroupNames, groups)
}

func (e *Engine) isAllowed(id peer.ID, action model.PermissionAction) bool {
	e.mu.Lock()
	perms := e.cfg.Permissions
	e.mu.Unlock()

	idStr := id.String()
	info, hasInfo := e.peers.Get(idStr)

	for _, p := range perms {
		if p.Action != action {
			continue
		}
		if p.PeerID != "" && p.PeerID == idStr {
			return true
		}
		if p.GroupName != "" && hasInfo {
			for _, gn := range info.GroupNames {
				if gn == p.GroupName {
					return true
				}
			}
		}
	}
	return false
}
