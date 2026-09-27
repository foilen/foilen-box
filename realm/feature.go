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

type Feature interface {
	Name() string

	Actions() []model.PermissionAction

	RegisterHandlers(reg *Registrar)
}

type PeerConnectedHook interface {
	OnPeerConnected(reg *Registrar, id peer.ID)
}

type PeriodicHook interface {
	RunPeriodic(reg *Registrar)
}

type PeerRemovedHook interface {
	OnPeerRemoved(id string)
}

type GroupConfirmedHook interface {
	OnGroupConfirmed(reg *Registrar, id peer.ID, group model.Group)
}

type PeerInUseHook interface {
	IsPeerInUse(id peer.ID) bool
}

type PeerDisconnectedHook interface {
	OnPeerDisconnected(id peer.ID)
}

type Registrar struct{ e *Engine }

func (r *Registrar) SetStreamHandler(id protocol.ID, h network.StreamHandler) {
	if r.e.host != nil {
		r.e.host.SetStreamHandler(id, h)
	}
}

func (r *Registrar) Host() host.Host {
	r.e.mu.Lock()
	defer r.e.mu.Unlock()
	return r.e.host
}

func (r *Registrar) PrivKey() crypto.PrivKey {
	r.e.mu.Lock()
	defer r.e.mu.Unlock()
	return r.e.priv
}

func (r *Registrar) Context() context.Context {
	r.e.mu.Lock()
	defer r.e.mu.Unlock()
	return r.e.ctx
}

func (r *Registrar) DataDir() string {
	return r.e.dataDir
}

func (r *Registrar) Config() model.Config {
	r.e.mu.Lock()
	defer r.e.mu.Unlock()
	return r.e.cfg
}

func (r *Registrar) IsAllowed(id peer.ID, action model.PermissionAction) bool {
	return r.e.isAllowed(id, action)
}

func (r *Registrar) IsCommonGroupPeer(id peer.ID) bool {
	return r.e.peerInCommonGroup(id)
}

func (r *Registrar) Peers() *peers.Store {
	return r.e.peers
}

func (r *Registrar) PeerLabel(id string) string {
	if r == nil {
		return model.ShortID(id)
	}
	return r.e.peers.Label(id)
}

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
