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

// Feature is a self-contained Realm capability an application opts into by
// constructing it and passing it to Engine.Register. Each feature owns its
// own libp2p protocol(s), permission actions, and any state/store it needs;
// the application calls whatever public methods the concrete feature type
// exposes directly (e.g. a notifications.Feature's SendNotification) —
// Engine itself only knows about the Feature interface below.
type Feature interface {
	// Name namespaces this feature's actions, e.g. "common/notifications".
	Name() string

	// Actions lists the fully-qualified permission actions this feature's
	// incoming handlers check, of the form Name()+"/"+verb (e.g.
	// "common/notifications/receive"). Engine.AvailableActions aggregates
	// these across every registered feature to build the dynamic
	// permission catalog.
	Actions() []model.PermissionAction

	// RegisterHandlers is called once whenever the engine's host is
	// (re)created, so the feature can register its own libp2p stream
	// handler(s) via reg.SetStreamHandler.
	RegisterHandlers(reg *Registrar)
}

// PeerConnectedHook is an optional Feature interface: if implemented, Engine
// calls OnPeerConnected (in its own goroutine) every time a peer connects —
// e.g. to flush queued outgoing sends to it, or refresh cached state.
type PeerConnectedHook interface {
	OnPeerConnected(reg *Registrar, id peer.ID)
}

// PeriodicHook is an optional Feature interface: if implemented, Engine
// calls RunPeriodic on the same cadence as its known-peer keep-alive loop.
type PeriodicHook interface {
	RunPeriodic(reg *Registrar)
}

// PeerRemovedHook is an optional Feature interface: if implemented, Engine
// calls OnPeerRemoved whenever a known peer is dropped from the peer store
// (currently: pruned for being unseen past the configured retention
// window), so the feature can discard whatever per-peer state it keeps
// (cached specs, notifications, run history, ...).
type PeerRemovedHook interface {
	OnPeerRemoved(id string)
}

// GroupConfirmedHook is an optional Feature interface: if implemented,
// Engine calls OnGroupConfirmed (in its own goroutine) whenever a peer
// passes the group-challenge for group (see group_challenge.go), i.e. the
// moment its membership goes from merely claimed to cryptographically
// proven — e.g. to push/pull whatever group-scoped state the feature keeps
// as soon as the peer is known to actually hold the group's key.
type GroupConfirmedHook interface {
	OnGroupConfirmed(reg *Registrar, id peer.ID, group model.Group)
}

// PeerInUseHook is an optional Feature interface: if implemented, Engine
// consults IsPeerInUse (on its keep-alive tick, see connection_ring.go)
// before disconnecting a peer that's outside every group's current
// connection ring, so an actively-used connection (e.g. a service proxy
// with data in flight) isn't torn down underneath it.
type PeerInUseHook interface {
	IsPeerInUse(id peer.ID) bool
}

// Registrar is the narrow facade a Feature is given instead of reaching into
// Engine's internals directly.
type Registrar struct{ e *Engine }

// SetStreamHandler registers h for protocol id on the running host.
//
// Unlike Host/PrivKey/Context/Config below, this deliberately reads r.e.host
// directly rather than through the locked Host() accessor: RegisterHandlers
// (the only caller) runs synchronously inside Engine.Start, on the same
// goroutine that is holding e.mu for the whole call — going through Host()
// here would try to re-lock the (non-reentrant) mutex and deadlock. Reading
// the field directly is safe because, as the current holder of e.mu, no
// other goroutine can be writing it concurrently.
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

// Peers returns the shared known/connected-peers store.
func (r *Registrar) Peers() *peers.Store {
	return r.e.peers
}

// EnsureConnected dials id if it isn't already connected, using the
// addresses last recorded for it in the peer store (the same source
// Engine's keep-alive loop dials from). It blocks until connected, dialTimeout
// elapses, or ctx is done. Features call this before opening an outbound
// stream so an on-demand action (e.g. a user pressing a button) doesn't fail
// just because the periodic reconnect hasn't gotten to this peer yet.
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
	log.Printf("realm engine: connecting to peer %s", id)
	return h.Connect(dialCtx, peer.AddrInfo{ID: id, Addrs: addrs})
}
