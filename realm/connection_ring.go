package realm

import (
	"context"
	"log"
	"sort"
	"sync"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"foilen-realm/model"
)

// ringNeighborCount is how many peers on each side (alphabetically previous
// and next, by peer id, wrapping around the group's member list) this peer
// tries to keep connected per group.
const ringNeighborCount = 2

// maintainGroupRings is the periodic connection-shaping pass run on every
// keepAliveInterval tick: for each configured group, it ensures this peer is
// connected to its ringNeighborCount previous and next members (trying
// further-out members if the nearest ones are unreachable), then disconnects
// any other known group peer that ends up connected but isn't required by
// any group's ring and isn't reported in use by a feature (see
// PeerInUseHook). Peers overlapping multiple groups' rings, or actively used
// by a feature, are never disconnected here.
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

// ringMemberIDs returns the alphabetically-sorted peer ids of selfID plus
// every known peer whose confirmed GroupNames includes groupName (discovery
// alone doesn't count, see handleFoundPeer).
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

// ringCandidateOrder returns every other member of members, in the order
// they should be tried when looking for connections in direction dir (-1:
// alphabetically previous, wrapping; +1: alphabetically next, wrapping)
// starting from members[selfIdx], closest first. members must be sorted and
// contain selfID exactly once, at selfIdx.
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

// indexOfString returns the index of v in list, or -1 if absent.
func indexOfString(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return -1
}

// connectRingCandidates walks candidates in order, ensuring each is
// connected (dialing it if it isn't), until want of them succeed or the
// list is exhausted. An unreachable candidate is skipped in favor of the
// next one further out, per the ring's "try the next one in the list"
// fallback. Returns the peer ids that ended up connected, in the order
// found.
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
		dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		err = h.Connect(dialCtx, peer.AddrInfo{ID: pid, Addrs: addrs})
		cancel()
		if err != nil {
			log.Printf("realm engine: ring peer %s unreachable, trying next: %v", candidate, err)
			continue
		}
		found = append(found, candidate)
	}
	return found
}

// disconnectExtraPeers closes the connection to every currently-connected,
// known group peer (one with at least one confirmed GroupNames entry) that
// isn't in required and isn't reported in use by any registered
// PeerInUseHook. Peers not tracked in the store at all (e.g. DHT routing
// connections to strangers) are left untouched.
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
			log.Printf("realm engine: failed to disconnect extra peer %s: %v", idStr, err)
		} else {
			log.Printf("realm engine: disconnected extra peer %s (outside connection ring, not in use)", idStr)
		}
	}
}

// isPeerInUse reports whether any registered PeerInUseHook claims id is
// actively in use.
func (e *Engine) isPeerInUse(id peer.ID) bool {
	for _, h := range e.peerInUseHooks {
		if h.IsPeerInUse(id) {
			return true
		}
	}
	return false
}
