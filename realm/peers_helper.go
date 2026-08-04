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

	// Skip unknown peers and extra simultaneous Conns to an already-connected peer.
	if info, known := e.peers.Get(remote.String()); known && !info.Connected {
		log.Printf("realm engine: connected to peer %s (%s)", remote, info.Hostname)
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

// onDisconnected fires once per closed Conn, not once per peer actually
// unreachable; only a disconnect dropping the peer's last Conn is
// logged/recorded. Unknown peers (e.g. DHT routing strangers) are skipped.
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
		log.Printf("realm engine: disconnected from peer %s (%s, no confirmed group)", remote, info.Hostname)
	} else {
		log.Printf("realm engine: disconnected from peer %s (%s, groups: %v)", remote, info.Hostname, info.GroupNames)
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

// handleFoundPeer records a peer surfaced by mDNS/DHT discovery under
// groupName's rendezvous channel. The channel only narrows the search and
// doesn't prove membership, so groupName isn't trusted here — GroupNames is
// only populated once the peer passes a signed group-challenge (peer_identify.go).
func (e *Engine) handleFoundPeer(info peer.AddrInfo, groupName, source string) {
	e.mu.Lock()
	h := e.host
	e.mu.Unlock()
	if h == nil || info.ID == h.ID() {
		return
	}

	log.Printf("realm engine: peer found via %s: %s (group %q)", source, info.ID, groupName)

	h.Peerstore().AddAddrs(info.ID, info.Addrs, time.Hour)

	addrs := addrsToStrings(info.Addrs)

	id := info.ID.String()
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
			log.Printf("realm engine: connecting to newly found peer %s", info.ID)
			if err := h.Connect(connectCtx, info); err != nil {
				log.Printf("realm engine: failed to connect to newly found peer %s: %v", info.ID, err)
			}
		}(dialCtx, info)
	}
}

// keepAliveLoop runs relay reservation upkeep, every feature's PeriodicHook,
// ring maintenance, and DHT swarm trimming every keepAliveInterval.
// Relay reservation upkeep runs first so a reservation obtained this tick is
// reflected by "common/announce" (which reads it via AddrsFactory) in the
// same tick instead of lagging a full keepAliveInterval behind. PeriodicHooks
// then run before maintainGroupRings since "common/announce" merges gossiped
// reachability addresses into the peer store before the ring reconnect dials.
func (e *Engine) keepAliveLoop(ctx context.Context) {
	e.maintainManualRelayReservation(ctx)
	e.runPeriodicHooks()
	e.maintainGroupRings(ctx)
	e.maintainDHTSwarm()
	e.pruneStalePeers()

	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			e.maintainManualRelayReservation(ctx)
			e.runPeriodicHooks()
			e.maintainGroupRings(ctx)
			e.maintainDHTSwarm()
			e.pruneStalePeers()
		case <-ctx.Done():
			return
		}
	}
}

// RunPeriodicNow immediately runs one iteration of the keep-alive tick (relay
// upkeep, every feature's PeriodicHook, ring maintenance, DHT swarm
// trimming, and stale-peer pruning) instead of waiting for the next
// keepAliveInterval tick. No-op if the engine isn't running.
func (e *Engine) RunPeriodicNow() {
	ctx := e.Context()
	if ctx == nil {
		return
	}
	e.maintainManualRelayReservation(ctx)
	e.runPeriodicHooks()
	e.maintainGroupRings(ctx)
	e.maintainDHTSwarm()
	e.pruneStalePeers()
}

// pruneStalePeers removes known, disconnected peers not seen within the
// configured retention window (model.DefaultPeerRetentionDays if unset;
// pruning is skipped entirely if the configured value is negative).
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
	for _, id := range e.peers.PruneStale(cutoff) {
		log.Printf("realm engine: pruned peer %s, not seen in over %d days", id, days)
		for _, h := range e.peerRemovedHooks {
			h.OnPeerRemoved(id)
		}
	}
}

// RemovePeer deletes a known, disconnected peer and runs the same
// peerRemovedHooks as PruneStale. Refuses (returns an error) if the peer is
// unknown or currently connected.
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

// hasCommonGroup reports whether groupNames contains the name of any group
// in groups.
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

// groupKey identifies a group across Reconcile calls: the group's key pair
// is what actually defines its mDNS service and DHT topic, so a rename
// (same key pair, different Name) must not be treated as add+remove.
func groupKey(group model.Group) string {
	return group.KeyPair.PrivateKeyBase64
}

// pruneRemovedGroups strips any group name that's no longer in newGroups
// from every known peer's GroupNames, so a deleted group doesn't linger in
// the peer list. Safe to call regardless of whether the engine is running.
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

// addedGroupKeys returns every group in newGroups that isn't (by groupKey)
// present in prevGroups.
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

// notifyConnectedPeersOfGroups re-runs the identify exchange with every
// connected known peer, so a newly added group is announced immediately
// instead of only on the next reconnect.
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

// findGroupByID returns the locally-configured group whose public group id
// (KeyPair.ID) matches id.
func findGroupByID(groups []model.Group, id string) (model.Group, bool) {
	for _, g := range groups {
		if g.KeyPair.ID == id {
			return g, true
		}
	}
	return model.Group{}, false
}
