package realm

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"foilen-realm/model"
)

func (e *Engine) onConnected(_ network.Network, conn network.Conn) {
	remote := conn.RemotePeer()

	if info, known := e.peers.Get(remote.String()); known && !info.Connected {
		log.Printf("realm engine: connected to peer %s", info.Label())
	}

	e.peers.SetConnected(remote.String(), true)

	if _, ok := e.peers.Get(remote.String()); ok {
		go e.fetchPeerIdentity(remote)
	}

	reg := &Registrar{e: e}
	for _, h := range e.peerConnectedHooks {
		go h.OnPeerConnected(reg, remote)
	}
}

func (e *Engine) onDisconnected(net network.Network, conn network.Conn) {
	remote := conn.RemotePeer()
	if net.Connectedness(remote) == network.Connected {
		return
	}
	e.peers.SetConnected(remote.String(), false)

	info, known := e.peers.Get(remote.String())
	if !known {
		return
	}
	if len(info.GroupNames) == 0 {
		log.Printf("realm engine: disconnected from peer %s (no confirmed group)", info.Label())
	} else {
		log.Printf("realm engine: disconnected from peer %s (groups: %v)", info.Label(), info.GroupNames)
	}

	for _, h := range e.peerDisconnectedHooks {
		go h.OnPeerDisconnected(remote)
	}

	if e.isRingNeighbor(remote.String()) {
		e.mu.Lock()
		ctx := e.ctx
		e.mu.Unlock()
		if ctx != nil {
			go e.reconnectRingPeerOnce(ctx, remote.String())
		}
	}
}

func (e *Engine) handleFoundPeer(info peer.AddrInfo, groupName, source string) {
	e.mu.Lock()
	h := e.host
	e.mu.Unlock()
	if h == nil || info.ID == h.ID() {
		return
	}

	id := info.ID.String()
	log.Printf("realm engine: peer found via %s: %s (group %q)", source, e.peers.Label(id), groupName)

	h.Peerstore().AddAddrs(info.ID, info.Addrs, time.Hour)

	addrs := addrsToStrings(info.Addrs)

	existing, _ := e.peers.Get(id)

	connected := h.Network().Connectedness(info.ID) == network.Connected
	e.peers.Upsert(model.PeerInfo{
		ID:                  id,
		LastSeen:            time.Now(),
		Addresses:           addrs,
		GroupNames:          existing.GroupNames,
		Connected:           connected,
		Hostname:            existing.Hostname,
		Description:         existing.Description,
		RelayServiceEnabled: existing.RelayServiceEnabled,
		Version:             existing.Version,
	}, source)

	if !connected && len(info.Addrs) > 0 {
		e.mu.Lock()
		dialCtx := e.ctx
		e.mu.Unlock()
		if dialCtx == nil {
			return
		}
		go func(dialCtx context.Context, info peer.AddrInfo) {
			connectCtx, cancel := context.WithTimeout(dialCtx, dialTimeout)
			defer cancel()
			log.Printf("realm engine: connecting to newly found peer %s", e.peers.Label(info.ID.String()))
			if err := h.Connect(connectCtx, info); err != nil {
				log.Printf("realm engine: failed to connect to newly found peer %s: %v", e.peers.Label(info.ID.String()), err)
			}
		}(dialCtx, info)
	}
}

func (e *Engine) keepAliveLoop(ctx context.Context) {
	e.runPeriodicHooks()
	e.maintainGroupRings(ctx)
	e.maintainDHTSwarm()
	e.pruneStalePeers()

	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			e.runPeriodicHooks()
			e.maintainGroupRings(ctx)
			e.maintainDHTSwarm()
			e.pruneStalePeers()
		case <-ctx.Done():
			return
		}
	}
}

func (e *Engine) RunPeriodicNow() {
	ctx := e.Context()
	if ctx == nil {
		return
	}
	e.runPeriodicHooks()
	e.maintainGroupRings(ctx)
	e.maintainDHTSwarm()
	e.pruneStalePeers()
}

func (e *Engine) pruneStalePeers() {
	e.mu.Lock()
	days := e.cfg.PeerRetentionDays
	e.mu.Unlock()

	if days < 0 {
		return
	}
	if days == 0 {
		days = model.DefaultPeerRetentionDays
	}

	cutoff := time.Now().AddDate(0, 0, -days)
	for _, info := range e.peers.PruneStale(cutoff) {
		log.Printf("realm engine: pruned peer %s, not seen in over %d days", info.Label(), days)
		for _, h := range e.peerRemovedHooks {
			h.OnPeerRemoved(info.ID)
		}
	}
}

func (e *Engine) RemovePeer(id string) error {
	if !e.peers.Remove(id) {
		return fmt.Errorf("peer %q is unknown or still connected", id)
	}
	for _, h := range e.peerRemovedHooks {
		h.OnPeerRemoved(id)
	}
	return nil
}

func (e *Engine) runPeriodicHooks() {
	reg := &Registrar{e: e}
	for _, h := range e.periodicHooks {
		h.RunPeriodic(reg)
	}
}

func hasCommonGroup(groupNames []string, groups []model.Group) bool {
	for _, g := range groups {
		for _, gn := range groupNames {
			if gn == g.Name {
				return true
			}
		}
	}
	return false
}

func groupsByKey(groups []model.Group) map[string]model.Group {
	m := make(map[string]model.Group, len(groups))
	for _, g := range groups {
		m[groupKey(g)] = g
	}
	return m
}

func groupKey(group model.Group) string {
	return group.KeyPair.PrivateKeyBase64
}

func (e *Engine) pruneRemovedGroups(prevGroups, newGroups []model.Group) {
	stillPresent := make(map[string]bool, len(newGroups))
	for _, g := range newGroups {
		stillPresent[g.Name] = true
	}
	for _, g := range prevGroups {
		if !stillPresent[g.Name] {
			e.peers.RemoveGroupName(g.Name)
		}
	}
}

func addedGroupKeys(prevGroups, newGroups []model.Group) []model.Group {
	prevKeys := make(map[string]bool, len(prevGroups))
	for _, g := range prevGroups {
		prevKeys[groupKey(g)] = true
	}
	var added []model.Group
	for _, g := range newGroups {
		if !prevKeys[groupKey(g)] {
			added = append(added, g)
		}
	}
	return added
}

func (e *Engine) notifyConnectedPeersOfGroups() {
	for _, info := range e.peers.List() {
		if !info.Connected {
			continue
		}
		pid, err := peer.Decode(info.ID)
		if err != nil {
			continue
		}
		go e.fetchPeerIdentity(pid)
	}
}
