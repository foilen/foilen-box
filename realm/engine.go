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
	"github.com/libp2p/go-libp2p/core/transport"
	routingdisc "github.com/libp2p/go-libp2p/p2p/discovery/routing"
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

	reconnectDelay = 10 * time.Second
)

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
	cfg              model.Config
	ctx              context.Context
	cancel           context.CancelFunc
	host             host.Host
	priv             crypto.PrivKey
	kadDHT           *dht.IpfsDHT
	dhtDatastore     *leveldb.Datastore
	routingDiscovery *routingdisc.RoutingDiscovery
	udpBroadcastConn *net.UDPConn
	udpBroadcastSeen map[string]struct{}
	dhtLoopCancels   map[string]context.CancelFunc

	lastDHTPeers []peer.AddrInfo

	relayTransport *relayTransport
}

// Setup

func New(dataDir string, peerStore *peers.Store) *Engine {
	return &Engine{dataDir: dataDir, peers: peerStore}
}

func (e *Engine) SetHostnameOverride(hostname string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.hostnameOverride = hostname
}

func (e *Engine) SetAppVersion(version string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.appVersion = version
}

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

func (e *Engine) AvailableActions() []model.PermissionAction {
	var all []model.PermissionAction
	for _, f := range e.features {
		all = append(all, f.Actions()...)
	}
	return all
}

// State

func (e *Engine) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

func (e *Engine) Host() host.Host {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.host
}

func (e *Engine) Context() context.Context {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ctx
}

func (e *Engine) HostID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.host == nil {
		return ""
	}
	return e.host.ID().String()
}

func (e *Engine) Addrs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.host == nil {
		return nil
	}
	return addrsToStrings(e.host.Addrs())
}

type SwarmPeer struct {
	ID        string
	Addresses []string
}

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

// Lifecycle

func (e *Engine) Restart(cfg model.Config) error {
	e.Stop()
	return e.Start(cfg)
}

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

	switch {
	case cfg.EnableUdpBroadcast && e.udpBroadcastConn == nil:
		log.Printf("realm engine: enabling UDP broadcast discovery")
		e.startUdpBroadcastLocked(ctx, h)
	case !cfg.EnableUdpBroadcast && e.udpBroadcastConn != nil:
		log.Printf("realm engine: disabling UDP broadcast discovery")
		e.stopUdpBroadcastLocked()
	}

	added := addedGroupKeys(prev.Groups, cfg.Groups)

	e.cfg = cfg

	if len(added) > 0 {
		e.notifyConnectedPeersOfGroups()
	}
	return nil
}

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

	e.peers.ResetAllConnected()

	opts := []libp2p.Option{
		libp2p.Identity(priv),

		libp2p.NATPortMap(),

		libp2p.EnableHolePunching(),
	}

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

	opts = append(opts, libp2p.AddrsFactory(func(addrs []multiaddr.Multiaddr) []multiaddr.Multiaddr {
		filtered := addrs[:0]
		for _, a := range addrs {
			if !a.Equal(relayListenAddr) {
				filtered = append(filtered, a)
			}
		}
		addrs = filtered
		if webAnnounceAddr != nil {
			addrs = append(addrs, webAnnounceAddr)
		}
		return addrs
	}))

	rt := &relayTransport{incoming: make(chan relayAcceptedConn), closed: make(chan struct{})}
	opts = append(opts,
		libp2p.Transport(func(u transport.Upgrader, rcmgr network.ResourceManager) (*relayTransport, error) {
			rt.upgrader, rt.rcmgr = u, rcmgr
			return rt, nil
		}),
		libp2p.ListenAddrs(relayListenAddr),
	)

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
	e.dhtLoopCancels = make(map[string]context.CancelFunc)

	rt.host = h
	rt.engine = e
	e.relayTransport = rt

	h.Network().Notify(&network.NotifyBundle{
		ConnectedF:    e.onConnected,
		DisconnectedF: e.onDisconnected,
	})
	h.SetStreamHandler(identifyProtocolID, e.handleIdentifyStream)
	h.SetStreamHandler(groupChallengeProtocolID, e.handleGroupChallengeStream)
	h.SetStreamHandler(relayHopProtocolID, rt.handleHopStream)
	h.SetStreamHandler(relayStopProtocolID, rt.handleStopStream)

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

	if cfg.EnableUdpBroadcast {
		e.startUdpBroadcastLocked(ctx, h)
	}

	go e.keepAliveLoop(ctx)

	log.Printf("realm engine: started, host id %s, listening on %v", model.ShortID(h.ID().String()), addrsToStrings(h.Addrs()))
	return nil
}

func (e *Engine) Stop() {
	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		return
	}

	e.cancel()
	e.stopUdpBroadcastLocked()
	e.stopDHTLocked()
	h := e.host
	e.host = nil
	e.priv = nil
	e.ctx = nil
	e.running = false
	e.mu.Unlock()

	if h != nil {
		if err := h.Close(); err != nil {
			log.Printf("realm engine: failed to close host: %v", err)
		}
	}

	e.relayTransport = nil

	log.Printf("realm engine: stopped")
}

// Helpers

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

func parseMultiaddrs(addrs []string) []multiaddr.Multiaddr {
	result := make([]multiaddr.Multiaddr, 0, len(addrs))
	for _, a := range addrs {
		if ma, err := multiaddr.NewMultiaddr(a); err == nil {
			result = append(result, ma)
		}
	}
	return result
}
