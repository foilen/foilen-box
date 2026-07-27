package realm

import (
	"context"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"

	"foilen-realm/model"
)

// groupACL restricts the circuit-relay-v2 server (when cfg.EnableRelayService
// is on) to peers that share a group with us, so it can't be used as an open
// relay by strangers who merely find this peer on the public DHT.
type groupACL struct{ e *Engine }

func (a *groupACL) AllowReserve(p peer.ID, _ multiaddr.Multiaddr) bool {
	return a.e.peerInCommonGroup(p)
}

func (a *groupACL) AllowConnect(src peer.ID, _ multiaddr.Multiaddr, dest peer.ID) bool {
	return a.e.peerInCommonGroup(src) && a.e.peerInCommonGroup(dest)
}

// peerInCommonGroup reports whether id is a known peer sharing at least one
// currently-configured group with us.
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

// isAllowed reports whether id is granted action per the configured
// Permissions: either explicitly by peer id, or by membership (per the
// peer's stored GroupNames) in a group the rule names. Deny-by-default: no
// matching Permission means the action is refused.
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

// relayPeerSource implements autorelay.PeerSource: it offers AutoRelay this
// peer's own known, currently-in-common-group peers as relay candidates.
// AutoRelay tries a reservation on each; only peers running with
// cfg.EnableRelayService actually grant one (per groupACL, only to peers
// they share a group with) — others are simply rejected. There's no
// dedicated relay infrastructure to draw from, so known group peers are the
// only pool available in a private swarm.
func (e *Engine) relayPeerSource(ctx context.Context, num int) <-chan peer.AddrInfo {
	e.mu.Lock()
	groups := e.cfg.Groups
	e.mu.Unlock()

	out := make(chan peer.AddrInfo)
	go func() {
		defer close(out)
		for _, info := range e.peers.List() {
			if num <= 0 {
				return
			}
			if !hasCommonGroup(info.GroupNames, groups) {
				continue
			}
			pid, err := peer.Decode(info.ID)
			if err != nil {
				continue
			}
			addrs := parseMultiaddrs(info.Addresses)
			if len(addrs) == 0 {
				continue
			}
			select {
			case out <- peer.AddrInfo{ID: pid, Addrs: addrs}:
				num--
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}
