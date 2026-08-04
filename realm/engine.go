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
	circuitrelay "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	quictransport "github.com/libp2p/go-libp2p/p2p/transport/quic"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	libp2pwebrtc "github.com/libp2p/go-libp2p/p2p/transport/webrtc"
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

	// reconnectDelay: how long onDisconnected waits before retrying a dropped ring-neighbor peer.
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

	// lastDHTPeers remembers the public DHT swarm peers last connected before
	// disconnectDHTSwarmLocked dropped them, so DhtModeClient's next lookup
	// can redial them directly (reconnectRememberedDHTPeers) instead of only
	// starting from the public bootstrap list.
	lastDHTPeers []peer.AddrInfo

	// relayMu guards relayReservations separately from mu, since maintaining
	// reservations does network I/O (maintainManualRelayReservation).
	relayMu           sync.Mutex
	relayReservations map[peer.ID]*relayReservation
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

// Register adds f as an active feature: its actions join AvailableActions,
// its stream handlers install on (re)start, and any hook interfaces it
// implements get wired in. Must be called before the engine first starts.
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

// ConnectedAddresses returns the remote multiaddrs of every open connection
// to the given peer ID, or nil if not running or not connected.
func (e *Engine) ConnectedAddresses(id string) []string {
	e.mu.Lock()
	h := e.host
	e.mu.Unlock()
	if h == nil {
		return nil
	}

	pid, err := peer.Decode(id)
	if err != nil {
		return nil
	}

	conns := h.Network().ConnsToPeer(pid)
	addrs := make([]multiaddr.Multiaddr, 0, len(conns))
	for _, c := range conns {
		addrs = append(addrs, c.RemoteMultiaddr())
	}
	return addrsToStrings(addrs)
}

// ConnectedHosts returns the bare IP hosts (no port, deduplicated) of every
// open connection to the given peer ID. For a relayed connection, this is
// the relay's IP, not the peer's — that's the address actually dialed over
// the underlying network, which is what callers need to route around a VPN.
func (e *Engine) ConnectedHosts(id string) []string {
	e.mu.Lock()
	h := e.host
	e.mu.Unlock()
	if h == nil {
		return nil
	}

	pid, err := peer.Decode(id)
	if err != nil {
		return nil
	}

	seen := make(map[string]struct{})
	var hosts []string
	for _, c := range h.Network().ConnsToPeer(pid) {
		host, err := firstIPHost(c.RemoteMultiaddr())
		if err != nil {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}
	return hosts
}

// firstIPHost returns the first /ip4 or /ip6 component's value in a
// multiaddr, e.g. "/ip4/1.2.3.4/tcp/4001" -> "1.2.3.4".
func firstIPHost(a multiaddr.Multiaddr) (string, error) {
	var host string
	multiaddr.ForEach(a, func(c multiaddr.Component) bool {
		if c.Protocol().Code == multiaddr.P_IP4 || c.Protocol().Code == multiaddr.P_IP6 {
			host = c.Value()
			return false
		}
		return true
	})
	if host == "" {
		return "", fmt.Errorf("no ip component in %s", a)
	}
	return host, nil
}

// Restart stops the engine if running and starts it again with cfg. This
// tears down and rebuilds the libp2p host (new connections, fresh DHT
// routing table), so prefer Reconcile for ordinary config changes; Restart
// is only needed when the peer identity itself changes.
func (e *Engine) Restart(cfg model.Config) error {
	e.Stop()
	return e.Start(cfg)
}

// Reconcile applies cfg with minimal disruption: starts/stops the engine as
// needed, or if already running, adjusts mDNS/DHT discovery and per-group
// loops in place without touching the host or existing connections. Only a
// peer identity, listen-port, relay-service, or web-listener change forces a
// full Restart.
func (e *Engine) Reconcile(cfg model.Config) error {
	e.mu.Lock()
	running := e.running
	prevPeerID := e.cfg.PeerID.ID
	prevGroups := e.cfg.Groups
	prevListenPort := e.cfg.RealmListenPort
	prevListenPortMode := e.cfg.RealmListenPortMode
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
	if cfg.RealmListenPort != prevListenPort || cfg.RealmListenPortMode != prevListenPortMode {
		log.Printf("realm engine: listen port setting changed, restarting")
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
		// stopDHTLocked only closes the DHT protocol/datastore; its bootstrap
		// swarm connections stay open otherwise.
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

	// Previous host's Connected flags don't apply to this not-yet-connected one.
	e.peers.ResetAllConnected()

	opts := []libp2p.Option{
		libp2p.Identity(priv),
		// UPnP/NAT-PMP: forward the listen port on the local router.
		libp2p.NATPortMap(),
		// DCUtR: upgrade a relayed connection to direct via hole punching.
		libp2p.EnableHolePunching(),
	}
	// Customizing the websocket transport below opts the host out of
	// go-libp2p's DefaultListenAddrs/DefaultTransports fallback, so both are
	// re-added explicitly to keep prior behavior.
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

	// Web listener (see model.Config.ExposeWebEnabled): a Transport for the
	// realm-http(s) multiaddr scheme (web_transport.go). Always registered
	// so this host can dial any peer advertising one (see
	// exposeWebAnnounceAddr below), but only told to Listen when enabled.
	opts = append(opts, libp2p.Transport(newWebTransport))
	webListenAddr, err := exposeWebListenAddr(cfg)
	if err != nil {
		log.Printf("realm engine: %v", err)
	}
	if webListenAddr != nil {
		opts = append(opts, libp2p.ListenAddrs(webListenAddr))
	}

	webAnnounceAddr, err := exposeWebAnnounceAddr(cfg)
	if err != nil {
		log.Printf("realm engine: %v", err)
	}
	// Append the web-announce addr and standing relay reservation addrs
	// (maintainManualRelayReservation) unconditionally.
	opts = append(opts, libp2p.AddrsFactory(func(addrs []multiaddr.Multiaddr) []multiaddr.Multiaddr {
		if webAnnounceAddr != nil {
			addrs = append(addrs, webAnnounceAddr)
		}
		return append(addrs, e.currentRelayReservationAddrs()...)
	}))

	if cfg.EnableRelayService {
		// Circuit-relay-v2 server: lets other group peers relay through this
		// host (e.g. a publicly-reachable box relaying for NAT-stuck peers).
		// Gated by groupACL so only peers sharing a group with us can use it.
		// Default resource limits (2min/128KB) would reset a relayed
		// connection before hole-punching or real transfer completes; safe to
		// lift since every client is already groupACL-authenticated.
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

	// h.Close() blocks until every Disconnected handler returns, and those
	// handlers acquire e.mu themselves; closing after releasing e.mu (with
	// running/host/ctx already cleared, so handlers skip reconnecting) avoids
	// deadlocking against our own lock.
	if h != nil {
		if err := h.Close(); err != nil {
			log.Printf("realm engine: failed to close host: %v", err)
		}
	}

	e.relayMu.Lock()
	e.relayReservations = nil
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
