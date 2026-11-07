// TODO - Human verify
package realm

import (
	"github.com/libp2p/go-libp2p/core/peer"
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
