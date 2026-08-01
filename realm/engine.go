// Package realm owns the go-libp2p host lifecycle for Realm: mDNS/DHT
// discovery, the connect/keep-alive loop for known group peers, and a
// pluggable Feature model that lets an application opt into only the
// capabilities it needs (see Feature).
package realm

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	leveldb "github.com/ipfs/go-ds-leveldb"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	routingdisc "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	"github.com/libp2p/go-libp2p/p2p/host/autorelay"
	circuitrelay "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	quictransport "github.com/libp2p/go-libp2p/p2p/transport/quic"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	libp2pwebrtc "github.com/libp2p/go-libp2p/p2p/transport/webrtc"
	ws "github.com/libp2p/go-libp2p/p2p/transport/websocket"
	webtransport "github.com/libp2p/go-libp2p/p2p/transport/webtransport"
	"github.com/multiformats/go-multiaddr"

	"foilen-realm/keypair"
	"foilen-realm/model"
	"foilen-realm/peers"
)

const (
	dhtDatastoreDirName = "realm-dht-datastore"
	keepAliveInterval   = 10 * time.Minute
	dialTimeout         = 30 * time.Second

	// reconnectDelay is how long onDisconnected waits before making a single
	// reconnect attempt to a main (ring-neighbor) peer that just dropped.
	// See reconnectRingPeerOnce.
	reconnectDelay = 10 * time.Second
)

// Engine owns a running Realm host, if any. The zero value (via New) is
// idle; Start/Stop bring the underlying host up and down as configuration
// changes. Features must be registered (via Register) before the engine is
// first started.
type Engine struct {
	dataDir          string
	peers            *peers.Store
	hostnameOverride string
	appVersion       string

	features              []Feature
	peerConnectedHooks    []PeerConnectedHook
	periodicHooks         []PeriodicHook
	peerRemovedHooks      []PeerRemovedHook
	peerInUseHooks        []PeerInUseHook
	groupConfirmedHooks   []GroupConfirmedHook
	peerDisconnectedHooks []PeerDisconnectedHook

	mu               sync.Mutex
	running          bool
	cfg              model.Config // config last applied via Start/Reconcile; used to diff on Reconcile
	ctx              context.Context
	cancel           context.CancelFunc
	host             host.Host
	priv             crypto.PrivKey
	kadDHT           *dht.IpfsDHT
	dhtDatastore     *leveldb.Datastore
	routingDiscovery *routingdisc.RoutingDiscovery
	mdnsSvcs         map[string]mdns.Service       // by groupKey
	dhtLoopCancels   map[string]context.CancelFunc // by groupKey

	// lastDHTPeers remembers the public DHT swarm peers (not known Realm
	// group peers) this host was last connected to, right before they were
	// disconnected (see disconnectDHTSwarmLocked). In DhtModeClient, the
	// engine doesn't keep this swarm connected between lookups; keeping
	// their addresses here lets the next lookup redial them directly (see
	// reconnectRememberedDHTPeers) instead of only starting from the public
	// bootstrap list again.
	lastDHTPeers []peer.AddrInfo

	// relayMu guards relayReservation. Separate from mu so that maintaining
	// the reservation (which does network I/O, see
	// maintainManualRelayReservation) never blocks unrelated engine state
	// access.
	relayMu          sync.Mutex
	relayReservation *relayReservation
}

// New creates an idle Engine. dataDir is where the persistent DHT datastore
// is kept; peerStore is the shared known/connected-peers view. Register the
// application's chosen Features on the result before the first Start/Reconcile.
func New(dataDir string, peerStore *peers.Store) *Engine {
	return &Engine{dataDir: dataDir, peers: peerStore}
}

// SetHostnameOverride sets the hostname reported to other peers during the
// identify exchange (see selfIdentifyPayload), used in place of
// os.Hostname() — needed on Android, where the OS-level hostname is always
// "localhost".
func (e *Engine) SetHostnameOverride(hostname string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.hostnameOverride = hostname
}

// SetAppVersion sets the application name/version reported to other peers
// during the identify exchange (see selfIdentifyPayload), e.g.
// "FoilenBox - abc1234".
func (e *Engine) SetAppVersion(version string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.appVersion = version
}

// Register adds f to the engine's set of active features: its actions join
// the AvailableActions catalog, its stream handler(s) are installed whenever
// the host (re)starts, and any optional hook interfaces it implements
// (PeerConnectedHook, PeriodicHook) are wired in. Must be called before the
// engine is first started.
func (e *Engine) Register(f Feature) {
	e.features = append(e.features, f)
	if h, ok := f.(PeerConnectedHook); ok {
		e.peerConnectedHooks = append(e.peerConnectedHooks, h)
	}
	if h, ok := f.(PeriodicHook); ok {
		e.periodicHooks = append(e.periodicHooks, h)
	}
	if h, ok := f.(PeerRemovedHook); ok {
		e.peerRemovedHooks = append(e.peerRemovedHooks, h)
	}
	if h, ok := f.(PeerInUseHook); ok {
		e.peerInUseHooks = append(e.peerInUseHooks, h)
	}
	if h, ok := f.(GroupConfirmedHook); ok {
		e.groupConfirmedHooks = append(e.groupConfirmedHooks, h)
	}
	if h, ok := f.(PeerDisconnectedHook); ok {
		e.peerDisconnectedHooks = append(e.peerDisconnectedHooks, h)
	}
}

// AvailableActions is the dynamic permission catalog: every action every
// registered Feature declares, in registration order. Used both to validate
// incoming Permission rules and to expose the catalog to a UI.
func (e *Engine) AvailableActions() []model.PermissionAction {
	var all []model.PermissionAction
	for _, f := range e.features {
		all = append(all, f.Actions()...)
	}
	return all
}

// Running reports whether the host is currently up.
func (e *Engine) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

// Host returns the running libp2p host, or nil if not running. Exposed for
// applications that need to dial a peer directly (e.g. by known multiaddr,
// bypassing group discovery) rather than through a Feature's own methods.
func (e *Engine) Host() host.Host {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.host
}

// Context returns the engine's lifetime context, cancelled on Stop, or nil
// if not running.
func (e *Engine) Context() context.Context {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ctx
}

// HostID returns the running host's peer id, or "" if not running.
func (e *Engine) HostID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.host == nil {
		return ""
	}
	return e.host.ID().String()
}

// Addrs returns the running host's listen multiaddresses, or nil if not running.
func (e *Engine) Addrs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.host == nil {
		return nil
	}
	return addrsToStrings(e.host.Addrs())
}

// SwarmPeer describes a libp2p peer the host is currently connected to,
// straight from the network layer, independent of whether it's a known
// Realm group peer.
type SwarmPeer struct {
	ID        string
	Addresses []string
}

// SwarmPeers returns every peer the host currently has an open connection
// to, per go-libp2p's own network/peerstore state (e.g. DHT routing
// connections to strangers), not just peers tracked in the Realm peer
// store. Returns nil if not running.
func (e *Engine) SwarmPeers() []SwarmPeer {
	e.mu.Lock()
	h := e.host
	e.mu.Unlock()
	if h == nil {
		return nil
	}

	ids := h.Network().Peers()
	result := make([]SwarmPeer, 0, len(ids))
	for _, id := range ids {
		conns := h.Network().ConnsToPeer(id)
		addrs := make([]multiaddr.Multiaddr, 0, len(conns))
		for _, c := range conns {
			addrs = append(addrs, c.RemoteMultiaddr())
		}
		result = append(result, SwarmPeer{ID: id.String(), Addresses: addrsToStrings(addrs)})
	}
	return result
}

// Restart stops the engine if running and starts it again with cfg. This
// tears down and rebuilds the libp2p host (new connections, fresh DHT
// routing table), so prefer Reconcile for ordinary config changes; Restart
// is only needed when the peer identity itself changes.
func (e *Engine) Restart(cfg model.Config) error {
	e.Stop()
	return e.Start(cfg)
}

// Reconcile applies cfg to the engine with minimal disruption: if the
// engine isn't running it starts it (or stops it, if cfg has no peer id
// yet or is Disabled); if it's already running, it adjusts mDNS/DHT
// discovery and per-group loops in place, leaving the host and existing
// peer connections untouched. Only a peer identity change forces a full
// Restart, since that requires a new libp2p host. Toggling Disabled back
// off goes through the !running branch, so it always gets a fresh Start
// rather than a partial reconcile.
func (e *Engine) Reconcile(cfg model.Config) error {
	e.mu.Lock()
	running := e.running
	prevPeerID := e.cfg.PeerID.ID
	prevGroups := e.cfg.Groups
	prevEnableRelayService := e.cfg.EnableRelayService
	prevExposeWeb := exposeWebSettings(e.cfg)
	e.mu.Unlock()

	e.pruneRemovedGroups(prevGroups, cfg.Groups)

	if !running {
		if cfg.PeerID.ID == "" || cfg.Disabled {
			return nil
		}
		return e.Start(cfg)
	}
	if cfg.PeerID.ID == "" || cfg.Disabled {
		e.Stop()
		return nil
	}
	if cfg.PeerID.ID != prevPeerID {
		log.Printf("realm engine: peer identity changed, restarting")
		return e.Restart(cfg)
	}
	if cfg.EnableRelayService != prevEnableRelayService {
		log.Printf("realm engine: relay service setting changed, restarting")
		return e.Restart(cfg)
	}
	if exposeWebSettings(cfg) != prevExposeWeb {
		log.Printf("realm engine: web listener setting changed, restarting")
		return e.Restart(cfg)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reconcileLocked(cfg)
}

// reconcileLocked adjusts discovery state from e.cfg to cfg in place. Must
// be called with e.mu held and the engine running.
func (e *Engine) reconcileLocked(cfg model.Config) error {
	h := e.host
	ctx := e.ctx
	prev := e.cfg

	dhtModeChanged := cfg.EnableDht && prev.EnableDht && cfg.DhtMode != prev.DhtMode
	if dhtModeChanged {
		log.Printf("realm engine: DHT mode changed %q -> %q, restarting DHT", prev.DhtMode, cfg.DhtMode)
		e.stopDHTLocked()
		if cfg.DhtMode == model.DhtModeClient {
			e.disconnectDHTSwarmLocked(h)
		}
	}

	switch {
	case cfg.EnableDht && e.kadDHT == nil:
		log.Printf("realm engine: enabling DHT")
		if err := e.startDHT(ctx, h, cfg); err != nil {
			log.Printf("realm engine: failed to start DHT: %v", err)
		} else {
			e.routingDiscovery = routingdisc.NewRoutingDiscovery(e.kadDHT)
		}
	case !cfg.EnableDht && e.kadDHT != nil:
		log.Printf("realm engine: disabling DHT")
		e.stopDHTLocked()
		// stopDHTLocked only closes the DHT's own protocol/datastore; the
		// swarm connections it opened while bootstrapping/refreshing its
		// routing table (to public bootstrap nodes and other DHT peers, none
		// of them known Realm group peers) stay open otherwise.
		e.disconnectDHTSwarmLocked(h)
	}

	if e.kadDHT != nil {
		desired := groupsByKey(cfg.Groups)
		for key, cancel := range e.dhtLoopCancels {
			if _, ok := desired[key]; !ok {
				cancel()
				delete(e.dhtLoopCancels, key)
			}
		}
		for key, group := range desired {
			if _, ok := e.dhtLoopCancels[key]; !ok {
				e.startGroupDHTLoopLocked(ctx, group)
			}
		}
	}

	if cfg.EnableMdns && mdnsSupported {
		desired := groupsByKey(cfg.Groups)
		for key, svc := range e.mdnsSvcs {
			if _, ok := desired[key]; !ok {
				if err := svc.Close(); err != nil {
					log.Printf("realm engine: failed to close mDNS service: %v", err)
				}
				delete(e.mdnsSvcs, key)
			}
		}
		for key, group := range desired {
			if _, ok := e.mdnsSvcs[key]; !ok {
				e.startGroupMdnsLocked(h, group)
			}
		}
	} else if len(e.mdnsSvcs) > 0 {
		log.Printf("realm engine: disabling mDNS")
		e.stopAllMdnsLocked()
	}

	added := addedGroupKeys(prev.Groups, cfg.Groups)

	e.cfg = cfg

	if len(added) > 0 {
		e.notifyConnectedPeersOfGroups()
	}
	return nil
}

// Start brings the host up: identity from cfg.PeerID, mDNS/DHT discovery
// per cfg.EnableMdns/EnableDht and cfg.DhtMode, and the keep-alive loop for
// known peers. A no-op if cfg has no peer id yet, cfg.Disabled is set, or
// the engine is already running.
func (e *Engine) Start(cfg model.Config) error {
	if cfg.PeerID.ID == "" || cfg.Disabled {
		return nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return nil
	}

	priv, err := keypair.PrivateKey(cfg.PeerID)
	if err != nil {
		return fmt.Errorf("realm engine: invalid peer keypair: %w", err)
	}

	// A peer's persisted Connected flag reflects the previous host's
	// connections, not this (not-yet-connected) one; clear it before the new
	// host starts reporting live connections.
	e.peers.ResetAllConnected()

	opts := []libp2p.Option{
		libp2p.Identity(priv),
		// UPnP/NAT-PMP: ask the local router to forward the listen port so
		// this peer is directly dialable behind a home-router NAT.
		libp2p.NATPortMap(),
		// DCUtR: once a relayed connection to a peer exists, try to upgrade
		// it to a direct connection via NAT hole punching.
		libp2p.EnableHolePunching(),
		// AutoRelay (client side): if this peer turns out to be
		// unreachable, reserve a slot on a relay so others can still reach
		// it (and hole-punching above has a relayed connection to upgrade
		// from). There's no public relay infra for a private swarm, so
		// candidates come from e.relayPeerSource, i.e. this peer's own
		// known group peers; only ones that opted into
		// cfg.EnableRelayService actually grant a reservation. This only
		// engages once AutoNAT's swarm-wide verdict is Private, so it's a
		// backstop against everyone being unreachable, not the asymmetric
		// case (unreachable from just a few peers) — see
		// maintainManualRelayReservation in relay.go for that.
		libp2p.EnableAutoRelayWithPeerSource(e.relayPeerSource, autorelay.WithMinCandidates(1)),
		// AutoNAT (server side): answer other connected peers' dial-back
		// probes so their AutoNAT client can determine its own reachability.
		// Without this, nobody in the swarm can confirm anybody else's
		// reachability, AutoNAT status stays Unknown forever, and the
		// AutoRelay client above never gets a Private verdict to act on.
		libp2p.EnableNATService(),
	}
	// Customizing the websocket transport's options below (via
	// libp2p.Transport(ws.New, ...)) opts the whole host out of
	// go-libp2p's own DefaultListenAddrs/DefaultTransports fallback
	// (which only applies when no Transport option was given at all), so
	// both must be re-added explicitly below to keep prior behavior.
	if cfg.RealmListenPort != 0 {
		listenAddrs, err := listenAddrsForPort(cfg.RealmListenPort)
		if err != nil {
			log.Printf("realm engine: failed to build listen addrs for port %d, falling back to random port: %v", cfg.RealmListenPort, err)
			opts = append(opts, libp2p.DefaultListenAddrs)
		} else {
			opts = append(opts, libp2p.ListenAddrs(listenAddrs...))
		}
	} else {
		opts = append(opts, libp2p.DefaultListenAddrs)
	}

	opts = append(opts,
		libp2p.Transport(tcp.NewTCPTransport),
		libp2p.Transport(quictransport.NewTransport),
		libp2p.Transport(webtransport.New),
		libp2p.Transport(libp2pwebrtc.New),
	)

	// The websocket transport's dialer must accept self-signed certs
	// unconditionally: any peer we try to reach may have ExposeWebEnabled
	// with its own self-signed cert, regardless of whether we have it
	// enabled ourselves (see expose_web.go).
	wsOpts := []any{ws.WithTLSClientConfig(websocketDialerTLSConfig())}

	webListenAddr, err := exposeWebListenAddr(cfg)
	if err != nil {
		log.Printf("realm engine: %v", err)
	}
	if webListenAddr != nil {
		opts = append(opts, libp2p.ListenAddrs(webListenAddr))
		if cfg.ExposeWebListenProtocol == "" || cfg.ExposeWebListenProtocol == "wss" {
			tlsConf, err := generateSelfSignedTLSConfig()
			if err != nil {
				log.Printf("realm engine: failed to generate self-signed cert for web listener, disabling it: %v", err)
				webListenAddr = nil
			} else {
				wsOpts = append(wsOpts, ws.WithTLSConfig(tlsConf))
			}
		}
	}
	opts = append(opts, libp2p.Transport(ws.New, wsOpts...))

	webAnnounceAddr, err := exposeWebAnnounceAddr(cfg)
	if err != nil {
		log.Printf("realm engine: %v", err)
	}
	// Always append the web-announce addr (if any) and this host's current
	// standing relay reservation addrs (see maintainManualRelayReservation),
	// on top of whatever address set go-libp2p/AutoRelay otherwise produces
	// — so a relay path is advertised unconditionally, not only while
	// AutoRelay considers this host privately-reachable.
	opts = append(opts, libp2p.AddrsFactory(func(addrs []multiaddr.Multiaddr) []multiaddr.Multiaddr {
		if webAnnounceAddr != nil {
			addrs = append(addrs, webAnnounceAddr)
		}
		return append(addrs, e.currentRelayReservationAddrs()...)
	}))

	if cfg.EnableRelayService {
		// Circuit-relay-v2 server: let other group peers reserve a slot on
		// this host and relay through it, e.g. a publicly-reachable box
		// relaying for peers stuck behind a symmetric/restrictive NAT. Gated
		// by groupACL so this doesn't turn into an open relay for any
		// stranger who finds this peer on the public DHT — only peers that
		// share a group with us (per the known-peers store) may reserve a
		// slot or relay a connection through us.
		//
		// The library's own defaults (Limit.Duration: 2min, Limit.Data:
		// 128KB) reset a relayed connection long before hole-punching
		// usually has a chance to upgrade it to direct, or before a peer
		// with no other reachable address (e.g. one behind CGNAT/a
		// restrictive mobile carrier) can do any real work over it. Every
		// relay client here already authenticated via groupACL, so there's
		// no abuse concern in leaving connections unlimited (Limit: nil).
		relayResources := circuitrelay.DefaultResources()
		relayResources.Limit = nil
		opts = append(opts, libp2p.EnableRelayService(circuitrelay.WithACL(&groupACL{e: e}), circuitrelay.WithResources(relayResources)))
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		return fmt.Errorf("realm engine: failed to create host: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	e.host = h
	e.priv = priv
	e.ctx = ctx
	e.cancel = cancel
	e.running = true
	e.cfg = cfg
	e.mdnsSvcs = make(map[string]mdns.Service)
	e.dhtLoopCancels = make(map[string]context.CancelFunc)

	h.Network().Notify(&network.NotifyBundle{
		ConnectedF:    e.onConnected,
		DisconnectedF: e.onDisconnected,
	})
	h.SetStreamHandler(identifyProtocolID, e.handleIdentifyStream)
	h.SetStreamHandler(groupChallengeProtocolID, e.handleGroupChallengeStream)

	reg := &Registrar{e: e}
	for _, f := range e.features {
		f.RegisterHandlers(reg)
	}

	if cfg.EnableDht {
		if err := e.startDHT(ctx, h, cfg); err != nil {
			log.Printf("realm engine: failed to start DHT: %v", err)
		} else {
			e.routingDiscovery = routingdisc.NewRoutingDiscovery(e.kadDHT)
			for _, group := range cfg.Groups {
				e.startGroupDHTLoopLocked(ctx, group)
			}
		}
	}

	if cfg.EnableMdns && mdnsSupported {
		for _, group := range cfg.Groups {
			e.startGroupMdnsLocked(h, group)
		}
	}

	go e.keepAliveLoop(ctx)

	log.Printf("realm engine: started, host id %s, listening on %v", h.ID(), addrsToStrings(h.Addrs()))
	return nil
}

// Stop shuts the host (and any discovery services) down. A no-op if not
// running.
func (e *Engine) Stop() {
	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		return
	}

	e.cancel()
	e.stopAllMdnsLocked()
	e.stopDHTLocked()
	h := e.host
	e.host = nil
	e.priv = nil
	e.ctx = nil
	e.running = false
	e.mu.Unlock()

	// h.Close() synchronously drains every open connection and blocks
	// until each one's Disconnected notification handler returns (see
	// swarm.Swarm.close) — and onDisconnected/isRingNeighbor need to
	// acquire e.mu themselves. Closing after releasing e.mu (with
	// e.running/e.host/e.ctx already updated above, so those handlers see
	// a stopped engine and skip any reconnect attempt) avoids deadlocking
	// against our own lock.
	if h != nil {
		if err := h.Close(); err != nil {
			log.Printf("realm engine: failed to close host: %v", err)
		}
	}

	e.relayMu.Lock()
	e.relayReservation = nil
	e.relayMu.Unlock()

	log.Printf("realm engine: stopped")
}

// listenAddrsForPort mirrors go-libp2p's own DefaultListenAddrs (TCP and
// QUIC, v4 and v6), but pins the port to the given value instead of letting
// the OS assign a random one each time, so the host's advertised addresses
// only change if its IP changes, not on every restart.
func listenAddrsForPort(port int) ([]multiaddr.Multiaddr, error) {
	specs := []string{
		fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port),
		fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1", port),
		fmt.Sprintf("/ip6/::/tcp/%d", port),
		fmt.Sprintf("/ip6/::/udp/%d/quic-v1", port),
	}
	addrs := make([]multiaddr.Multiaddr, 0, len(specs))
	for _, s := range specs {
		a, err := multiaddr.NewMultiaddr(s)
		if err != nil {
			return nil, fmt.Errorf("invalid listen addr %q: %w", s, err)
		}
		addrs = append(addrs, a)
	}
	return addrs, nil
}

// PickFreeListenPort asks the OS for a currently unused TCP port, suitable
// as a new Config.ListenPort. Callers must persist the result (see
// realm/config.Service.Save) so the same port is reused on every future
// Start instead of being picked again.
func PickFreeListenPort() (int, error) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, fmt.Errorf("realm engine: failed to pick a free listen port: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// addrsToStrings renders each multiaddr as a string, in order.
func addrsToStrings(addrs []multiaddr.Multiaddr) []string {
	result := make([]string, 0, len(addrs))
	for _, a := range addrs {
		result = append(result, a.String())
	}
	return result
}

// parseMultiaddrs parses each address string, silently skipping ones that
// fail to parse.
func parseMultiaddrs(addrs []string) []multiaddr.Multiaddr {
	result := make([]multiaddr.Multiaddr, 0, len(addrs))
	for _, a := range addrs {
		if ma, err := multiaddr.NewMultiaddr(a); err == nil {
			result = append(result, ma)
		}
	}
	return result
}
