package realm

import (
	"context"
	"log"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	circuitv2client "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	"github.com/multiformats/go-multiaddr"

	"foilen-realm/model"
)

// relayReservationRenewBefore: how far ahead of expiration
// maintainManualRelayReservation renews a held reservation (server's default
// TTL is 1h), leaving several keepAliveInterval ticks to retry.
const relayReservationRenewBefore = 20 * time.Minute

// relayReservation is a standing circuit-relay-v2 reservation this host
// holds on another peer, and the addresses (see circuitv2client.Reserve) it
// can be dialed at through that relay.
type relayReservation struct {
	peerID     peer.ID
	addrs      []multiaddr.Multiaddr
	expiration time.Time
}

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

// connectedRelayPeers returns every currently-connected common-group peer
// that last reported cfg.EnableRelayService (groupACL may still refuse if
// that's since changed). Only connected peers are candidates: reserving
// through a disconnected one would just force an extra dial.
func (e *Engine) connectedRelayPeers() []peer.AddrInfo {
	e.mu.Lock()
	groups := e.cfg.Groups
	h := e.host
	e.mu.Unlock()
	if h == nil {
		return nil
	}

	var candidates []peer.AddrInfo
	for _, info := range e.peers.List() {
		if !info.RelayServiceEnabled {
			continue
		}
		if !hasCommonGroup(info.GroupNames, groups) {
			continue
		}
		pid, err := peer.Decode(info.ID)
		if err != nil || pid == h.ID() {
			continue
		}
		if h.Network().Connectedness(pid) != network.Connected {
			continue
		}
		addrs := parseMultiaddrs(info.Addresses)
		if len(addrs) == 0 {
			continue
		}
		candidates = append(candidates, peer.AddrInfo{ID: pid, Addrs: addrs})
	}
	return candidates
}

// currentRelayReservationAddrs returns the dialable circuit-relay addrs from
// every standing reservation this host holds (see
// maintainManualRelayReservation).
func (e *Engine) currentRelayReservationAddrs() []multiaddr.Multiaddr {
	e.relayMu.Lock()
	defer e.relayMu.Unlock()
	var addrs []multiaddr.Multiaddr
	for _, resv := range e.relayReservations {
		addrs = append(addrs, resv.addrs...)
	}
	return addrs
}

// maintainManualRelayReservation keeps a standing reservation on every
// currently-connected relay-capable group peer, so this host stays reachable
// through any of them rather than betting on a single relay. Reservations
// are announced unconditionally via engine.go's AddrsFactory.
//
// Peers no longer connected (or no longer relay/group candidates) have their
// reservation dropped; peers whose reservation still has more than
// relayReservationRenewBefore left are left alone; everyone else gets a
// fresh Reserve call.
func (e *Engine) maintainManualRelayReservation(ctx context.Context) {
	e.mu.Lock()
	h := e.host
	e.mu.Unlock()
	if h == nil {
		return
	}

	candidates := e.connectedRelayPeers()
	candidateSet := make(map[peer.ID]struct{}, len(candidates))
	for _, info := range candidates {
		candidateSet[info.ID] = struct{}{}
	}

	e.relayMu.Lock()
	for pid, resv := range e.relayReservations {
		if _, stillCandidate := candidateSet[pid]; !stillCandidate {
			delete(e.relayReservations, pid)
			continue
		}
		if time.Until(resv.expiration) <= relayReservationRenewBefore {
			delete(e.relayReservations, pid)
		}
	}
	e.relayMu.Unlock()

	for _, info := range candidates {
		e.relayMu.Lock()
		_, held := e.relayReservations[info.ID]
		e.relayMu.Unlock()
		if held {
			continue
		}

		reserveCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		resv, err := circuitv2client.Reserve(reserveCtx, h, info)
		cancel()
		if err != nil {
			continue
		}

		e.relayMu.Lock()
		if e.relayReservations == nil {
			e.relayReservations = make(map[peer.ID]*relayReservation)
		}
		e.relayReservations[info.ID] = &relayReservation{peerID: info.ID, addrs: resv.Addrs, expiration: resv.Expiration}
		e.relayMu.Unlock()
		log.Printf("realm engine: holding standing relay reservation via %s (expires %s)", info.ID, resv.Expiration.Format(time.RFC3339))
	}
}
