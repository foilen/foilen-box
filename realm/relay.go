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

// relayReservationCandidateCount bounds how many candidates
// maintainManualRelayReservation tries per round; the first with
// cfg.EnableRelayService granted by groupACL wins.
const relayReservationCandidateCount = 5

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

// relayPeerSource implements autorelay.PeerSource: offers common-group peers
// that last reported cfg.EnableRelayService as relay candidates (groupACL
// may still refuse if that's since changed). No dedicated relay infra
// exists, so known group peers are the only pool. Already-connected
// candidates go first to avoid an extra dial.
func (e *Engine) relayPeerSource(ctx context.Context, num int) <-chan peer.AddrInfo {
	e.mu.Lock()
	groups := e.cfg.Groups
	h := e.host
	e.mu.Unlock()

	out := make(chan peer.AddrInfo)
	go func() {
		defer close(out)

		candidates := make([]peer.AddrInfo, 0, num)
		var connected, disconnected []peer.AddrInfo
		for _, info := range e.peers.List() {
			if !info.RelayServiceEnabled {
				continue
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
			ai := peer.AddrInfo{ID: pid, Addrs: addrs}
			if h != nil && h.Network().Connectedness(pid) == network.Connected {
				connected = append(connected, ai)
			} else {
				disconnected = append(disconnected, ai)
			}
		}
		candidates = append(candidates, connected...)
		candidates = append(candidates, disconnected...)

		for _, ai := range candidates {
			if num <= 0 {
				return
			}
			select {
			case out <- ai:
				num--
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// currentRelayReservationAddrs returns the dialable circuit-relay addrs from
// this host's current standing reservation (see maintainManualRelayReservation),
// or nil if it doesn't hold one right now.
func (e *Engine) currentRelayReservationAddrs() []multiaddr.Multiaddr {
	e.relayMu.Lock()
	defer e.relayMu.Unlock()
	if e.relayReservation == nil {
		return nil
	}
	return e.relayReservation.addrs
}

// maintainManualRelayReservation keeps a standing reservation on some
// relay-capable group peer, independent of AutoRelay (which only acts once
// the swarm-wide AutoNAT verdict is Private — see EnableAutoRelayWithPeerSource
// in engine.go — so it stays dormant for a host unreachable from just one
// peer, e.g. behind an asymmetric firewall). The reservation's addrs are
// announced unconditionally via engine.go's AddrsFactory.
//
// No-op if the current reservation has more than relayReservationRenewBefore
// left. Otherwise tries up to relayReservationCandidateCount candidates and
// keeps the first that grants one (per groupACL).
func (e *Engine) maintainManualRelayReservation(ctx context.Context) {
	e.mu.Lock()
	h := e.host
	e.mu.Unlock()
	if h == nil {
		return
	}

	e.relayMu.Lock()
	current := e.relayReservation
	e.relayMu.Unlock()
	if current != nil && time.Until(current.expiration) > relayReservationRenewBefore {
		return
	}

	candidatesCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	for info := range e.relayPeerSource(candidatesCtx, relayReservationCandidateCount) {
		if info.ID == h.ID() {
			continue
		}

		reserveCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		resv, err := circuitv2client.Reserve(reserveCtx, h, info)
		cancel()
		if err != nil {
			continue
		}

		e.relayMu.Lock()
		e.relayReservation = &relayReservation{peerID: info.ID, addrs: resv.Addrs, expiration: resv.Expiration}
		e.relayMu.Unlock()
		log.Printf("realm engine: holding standing relay reservation via %s (expires %s)", info.ID, resv.Expiration.Format(time.RFC3339))
		return
	}

	// Drop a previously held reservation only once it's actually lapsed, so a
	// transient renew failure doesn't stop advertising a still-valid addr.
	e.relayMu.Lock()
	if e.relayReservation != nil && !time.Now().Before(e.relayReservation.expiration) {
		e.relayReservation = nil
	}
	e.relayMu.Unlock()
}
