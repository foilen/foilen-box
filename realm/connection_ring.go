package realm

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"foilen-realm/model"
)

const ringNeighborCount = 2

func (e *Engine) maintainGroupRings(ctx context.Context) {
	e.mu.Lock()
	h := e.host
	cfg := e.cfg
	e.mu.Unlock()
	if h == nil {
		return
	}
	selfID := h.ID().String()
	known := e.peers.List()

	var mu sync.Mutex
	required := map[string]bool{}
	var wg sync.WaitGroup

	for _, group := range cfg.Groups {
		members := ringMemberIDs(known, group.Name, selfID)
		selfIdx := indexOfString(members, selfID)
		if selfIdx < 0 || len(members) < 2 {
			continue
		}
		for _, dir := range [2]int{-1, 1} {
			candidates := ringCandidateOrder(members, selfIdx, dir)
			wg.Add(1)
			go func(candidates []string) {
				defer wg.Done()
				connected := e.connectRingCandidates(ctx, h, candidates, ringNeighborCount)
				mu.Lock()
				for _, id := range connected {
					required[id] = true
				}
				mu.Unlock()
			}(candidates)
		}
	}
	wg.Wait()

	e.disconnectExtraPeers(h, required)
}

func ringMemberIDs(known []model.PeerInfo, groupName, selfID string) []string {
	ids := []string{selfID}
	for _, info := range known {
		for _, g := range info.GroupNames {
			if g == groupName {
				ids = append(ids, info.ID)
				break
			}
		}
	}
	sort.Strings(ids)
	return ids
}

func ringCandidateOrder(members []string, selfIdx, dir int) []string {
	n := len(members)
	order := make([]string, 0, n-1)
	idx := selfIdx
	for i := 0; i < n-1; i++ {
		idx = (idx + dir + n) % n
		order = append(order, members[idx])
	}
	return order
}

func indexOfString(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return -1
}

func (e *Engine) connectRingCandidates(ctx context.Context, h host.Host, candidates []string, want int) []string {
	found := make([]string, 0, want)
	for _, candidate := range candidates {
		if len(found) >= want {
			break
		}
		pid, err := peer.Decode(candidate)
		if err != nil {
			continue
		}
		if h.Network().Connectedness(pid) == network.Connected {
			found = append(found, candidate)
			continue
		}
		info, ok := e.peers.Get(candidate)
		if !ok {
			continue
		}
		addrs := parseMultiaddrs(info.Addresses)
		if len(addrs) == 0 {
			continue
		}
		log.Printf("realm engine: connecting to ring peer %s", e.peers.Label(candidate))
		dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		err = h.Connect(dialCtx, peer.AddrInfo{ID: pid, Addrs: addrs})
		cancel()
		if err != nil {
			log.Printf("realm engine: ring peer %s unreachable, trying next: %v", e.peers.Label(candidate), err)
			continue
		}
		found = append(found, candidate)
	}
	return found
}

func (e *Engine) disconnectExtraPeers(h host.Host, required map[string]bool) {
	for _, pid := range h.Network().Peers() {
		idStr := pid.String()
		if required[idStr] {
			continue
		}
		info, ok := e.peers.Get(idStr)
		if !ok || len(info.GroupNames) == 0 {
			continue
		}
		if e.isPeerInUse(pid) {
			continue
		}
		if err := h.Network().ClosePeer(pid); err != nil {
			log.Printf("realm engine: failed to disconnect extra peer %s: %v", info.Label(), err)
		} else {
			log.Printf("realm engine: disconnected extra peer %s (outside connection ring, not in use)", info.Label())
		}
	}
}

func (e *Engine) isPeerInUse(id peer.ID) bool {
	for _, h := range e.peerInUseHooks {
		if h.IsPeerInUse(id) {
			return true
		}
	}
	return false
}

func (e *Engine) IsRingNeighbor(id string) bool {
	return e.isRingNeighbor(id)
}

func (e *Engine) isRingNeighbor(id string) bool {
	e.mu.Lock()
	cfg := e.cfg
	e.mu.Unlock()

	selfID := ""
	if h := e.getHost(); h != nil {
		selfID = h.ID().String()
	}
	if selfID == "" {
		return false
	}
	known := e.peers.List()

	for _, group := range cfg.Groups {
		members := ringMemberIDs(known, group.Name, selfID)
		selfIdx := indexOfString(members, selfID)
		if selfIdx < 0 || len(members) < 2 {
			continue
		}
		for _, dir := range [2]int{-1, 1} {
			candidates := ringCandidateOrder(members, selfIdx, dir)
			if len(candidates) > ringNeighborCount {
				candidates = candidates[:ringNeighborCount]
			}
			if indexOfString(candidates, id) >= 0 {
				return true
			}
		}
	}
	return false
}

func (e *Engine) getHost() host.Host {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.host
}

func (e *Engine) reconnectRingPeerOnce(ctx context.Context, id string) {
	timer := time.NewTimer(reconnectDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return
	}

	h := e.getHost()
	if h == nil {
		return
	}
	pid, err := peer.Decode(id)
	if err != nil {
		return
	}
	if h.Network().Connectedness(pid) == network.Connected {
		return
	}
	info, ok := e.peers.Get(id)
	if !ok {
		return
	}
	addrs := parseMultiaddrs(info.Addresses)
	if len(addrs) == 0 {
		return
	}

	log.Printf("realm engine: reconnecting to main peer %s after disconnect", info.Label())
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	if err := h.Connect(dialCtx, peer.AddrInfo{ID: pid, Addrs: addrs}); err != nil {
		log.Printf("realm engine: reconnect to main peer %s failed: %v", info.Label(), err)
	}
}
