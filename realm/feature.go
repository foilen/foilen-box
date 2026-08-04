package realm

import (
	"context"
	"log"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"

	"foilen-realm/model"
	"foilen-realm/peers"
)

// Feature is a self-contained Realm capability an application opts into via
// Engine.Register. It owns its own libp2p protocol(s), permission actions,
// and state; Engine only knows this interface, not the concrete type.
type Feature interface {
	// Name namespaces this feature's actions, e.g. "common/scripts".
	Name() string

	// Actions lists fully-qualified permission actions (Name()+"/"+verb,
	// e.g. "common/scripts/run"), aggregated by Engine.AvailableActions.
	Actions() []model.PermissionAction

	// RegisterHandlers is called on every (re)creation of the engine's host,
	// to register stream handler(s) via reg.SetStreamHandler.
	RegisterHandlers(reg *Registrar)
}

// PeerConnectedHook: Engine calls OnPeerConnected (own goroutine) whenever a peer connects.
type PeerConnectedHook interface {
	OnPeerConnected(reg *Registrar, id peer.ID)
}

// PeriodicHook: Engine calls RunPeriodic on the keep-alive loop's cadence.
type PeriodicHook interface {
	RunPeriodic(reg *Registrar)
}

// PeerRemovedHook: Engine calls OnPeerRemoved when a known peer is pruned
// from the peer store (unseen past the retention window), so the feature can
// discard its per-peer state.
type PeerRemovedHook interface {
	OnPeerRemoved(id string)
}

// GroupConfirmedHook: Engine calls OnGroupConfirmed (own goroutine) once a
// peer passes the group-challenge (group_challenge.go) — membership going
// from claimed to cryptographically proven.
type GroupConfirmedHook interface {
	OnGroupConfirmed(reg *Registrar, id peer.ID, group model.Group)
}

// PeerInUseHook: Engine consults IsPeerInUse (keep-alive tick,
// connection_ring.go) before disconnecting a peer outside every ring, so an
// actively-used connection isn't torn down.
type PeerInUseHook interface {
	IsPeerInUse(id peer.ID) bool
}

// PeerDisconnectedHook: Engine calls OnPeerDisconnected once a peer's last
// live Conn closes (not per-Conn, not on a stale-peer prune — see
// onDisconnected), so a feature can discard in-memory per-connection state.
type PeerDisconnectedHook interface {
	OnPeerDisconnected(id peer.ID)
}

// Registrar is the narrow facade a Feature is given instead of reaching into
// Engine's internals directly.
type Registrar struct{ e *Engine }

// SetStreamHandler registers h for protocol id on the running host.
//
// Reads r.e.host directly instead of via Host(): the only caller runs inside
// Engine.Start while already holding e.mu, so Host() would deadlock.
func (r *Registrar) SetStreamHandler(id protocol.ID, h network.StreamHandler) {
	if r.e.host != nil {
		r.e.host.SetStreamHandler(id, h)
	}
}

// Host returns the running libp2p host, or nil if the engine isn't running.
func (r *Registrar) Host() host.Host {
	r.e.mu.Lock()
	defer r.e.mu.Unlock()
	return r.e.host
}

// PrivKey returns the running host's private key, or nil if not running.
func (r *Registrar) PrivKey() crypto.PrivKey {
	r.e.mu.Lock()
	defer r.e.mu.Unlock()
	return r.e.priv
}

// Context returns the running engine's lifetime context, cancelled on Stop,
// or nil if not running.
func (r *Registrar) Context() context.Context {
	r.e.mu.Lock()
	defer r.e.mu.Unlock()
	return r.e.ctx
}

// DataDir returns the directory the engine was constructed with, for
// features that need a place to keep their own on-disk state.
func (r *Registrar) DataDir() string {
	return r.e.dataDir
}

// Config returns the currently-applied Config (as of the last Start/Reconcile).
func (r *Registrar) Config() model.Config {
	r.e.mu.Lock()
	defer r.e.mu.Unlock()
	return r.e.cfg
}

// IsAllowed reports whether id is granted action per the currently-applied
// Config.Permissions.
func (r *Registrar) IsAllowed(id peer.ID, action model.PermissionAction) bool {
	return r.e.isAllowed(id, action)
}

// IsCommonGroupPeer reports whether id is a known peer sharing at least one
// currently-configured group with us.
func (r *Registrar) IsCommonGroupPeer(id peer.ID) bool {
	return r.e.peerInCommonGroup(id)
}

// Peers returns the shared known/connected-peers store.
func (r *Registrar) Peers() *peers.Store {
	return r.e.peers
}

// EnsureConnected dials id (using its last-recorded peer-store addresses) if
// not already connected, blocking until connected, dialTimeout elapses, or
// ctx is done. Call before opening an outbound stream so an on-demand action
// doesn't fail just because the periodic reconnect hasn't reached this peer yet.
//
// info.Addresses may include relay addresses (realm/relay_transport.go,
// appended by realm/features/announce for every common-group peer that
// reports RelayServiceEnabled), tried alongside direct addresses in the same
// Connect call.
func (r *Registrar) EnsureConnected(ctx context.Context, id peer.ID) error {
	h := r.Host()
	if h == nil {
		return context.Canceled
	}
	if h.Network().Connectedness(id) == network.Connected {
		return nil
	}

	info, ok := r.e.peers.Get(id.String())
	var addrs []multiaddr.Multiaddr
	if ok {
		addrs = parseMultiaddrs(info.Addresses)
	}

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	log.Printf("realm engine: connecting to peer %s", r.e.peers.Label(id.String()))
	return h.Connect(dialCtx, peer.AddrInfo{ID: id, Addrs: addrs})
}
