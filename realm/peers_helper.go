package realm

import (
	"context"
	"log"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"foilen-realm/model"
)

func (e *Engine) onConnected(_ network.Network, conn network.Conn) {
	remote := conn.RemotePeer()

	// Only known peers are worth surfacing (mirrors onDisconnected below);
	// a peer already marked connected means this is an extra simultaneous
	// Conn to the same peer, not a fresh connect worth logging again.
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

// onDisconnected fires once per closed network.Conn, not once per peer
// actually going unreachable: a peer can have more than one simultaneous
// Conn (e.g. both sides dialed each other, or more than one transport
// succeeded), so closing one of them isn't a real disconnect as long as net
// still reports another live Conn to the same peer. Only a disconnect that
// drops the peer's last Conn is logged/recorded here; the rest are ignored.
//
// Real disconnects are app-initiated (ring/DHT-swarm trimming, see
// connection_ring.go/discovery_dht.go, both of which already log why they
// closed the connection) or not; logging unconditionally here makes a
// spontaneous/network-level drop distinguishable from those: it's any
// disconnect log line that isn't immediately preceded by one of theirs.
// Unknown peers (e.g. DHT routing connections to strangers) are skipped;
// only known peers are worth surfacing.
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
}

// handleFoundPeer records a peer surfaced by mDNS/DHT discovery under
// groupName's rendezvous channel. That channel only narrows the search, it
// doesn't prove membership (the derived service name/topic can leak to a
// network observer without the group secret) — so groupName is not trusted
// here. GroupNames is left untouched (empty for a brand-new peer); it's only
// ever populated once the peer actually passes a signed group-challenge, see
// peer_identify.go/challengeGroup.
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

// keepAliveLoop runs the per-group connection ring maintenance (see
// maintainGroupRings) and DHT swarm trimming (see maintainDHTSwarm) every
// keepAliveInterval, and drives every registered feature's PeriodicHook on
// the same cadence. PeriodicHook runs first: the "common/announce" hook it
// drives (see realmAnnounce.RunPeriodic in internal/webserver) is what
// merges peers' self-reported reachability addresses from the RealmMap
// ("announce" source, see peers.addressSourcePriority) into the local known-
// peers store, and a peer's real (e.g. LAN) address learned that way —
// picked up via gossip through any group member, not only a direct
// connection to that peer — is often already available and more likely
// dialable than whatever mDNS/DHT discovery has found so far. Running it
// before maintainGroupRings means the ring reconnect attempt below uses
// addresses as fresh as this tick, instead of ones left over from the
// previous one.
func (e *Engine) keepAliveLoop(ctx context.Context) {
	e.runPeriodicHooks()
	e.maintainGroupRings(ctx)
	e.maintainDHTSwarm()
	e.maintainManualRelayReservation(ctx)
	e.pruneStalePeers()

	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			e.runPeriodicHooks()
			e.maintainGroupRings(ctx)
			e.maintainDHTSwarm()
			e.maintainManualRelayReservation(ctx)
			e.pruneStalePeers()
		case <-ctx.Done():
			return
		}
	}
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
// already-connected known peer, so a group just added to our config is
// announced to them immediately (as our claimed GroupIDs) instead of only
// surfacing next time each connection is re-established.
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
