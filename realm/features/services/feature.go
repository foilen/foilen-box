// Package services is the "common/services" Realm feature: a peer can
// advertise local services (name, hostname, type, port) and let peers/groups
// it has granted the connect action to open a local TCP proxy tunnel to one,
// riding the same permissioned libp2p connection as every other feature.
package services

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	realm "foilen-realm"
	"foilen-realm/model"
)

const (
	// TunnelProtocolID carries one forwarded TCP connection per stream: a
	// small JSON header naming the service, a one-byte ack, then raw bytes
	// copied both ways until either side closes. Unlike every other
	// feature's protocols this stream is long-lived, so ioTimeout is only
	// ever applied to the initial header/ack handshake, never to the data
	// phase.
	TunnelProtocolID = protocol.ID("/foilen-box/services-tunnel/1.0.0")

	ioTimeout = 10 * time.Second
	maxBytes  = 16 * 1024

	// localPortRangeStart/End is the deterministic local-proxy-port range:
	// hash(peerId+serviceName) picks a starting point in this range, and a
	// collision linear-probes forward through it.
	localPortRangeStart = 49152
	localPortRangeEnd   = 65535

	// FeatureName is this feature's namespace, used to prefix its actions.
	FeatureName = "common/services"

	// ActionConnect gates listing this peer's own services and opening a
	// tunnel to one of them.
	ActionConnect model.PermissionAction = FeatureName + "/connect"
)

// tunnelHeader is the small JSON message sent once at the start of a
// TunnelProtocolID stream, naming the service to forward this connection to.
type tunnelHeader struct {
	ServiceName string `json:"serviceName"`
}

// tunnelAck is the one-message reply to a tunnelHeader: whether the local
// dial to the service succeeded.
type tunnelAck struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// ActiveProxy describes a currently-running local proxy, for the UI to
// restore Start/Stop button state after a page reload.
type ActiveProxy struct {
	PeerID      string `json:"peerId"`
	ServiceName string `json:"serviceName"`
	LocalPort   int    `json:"localPort"`
}

// ScanResult is one entry of ScanLocalPorts' report.
type ScanResult struct {
	Port         int    `json:"port"`
	Open         bool   `json:"open"`
	GuessedName  string `json:"guessedName"`
	GuessedType  string `json:"guessedType"`
	Unverifiable bool   `json:"unverifiable"`
}

// proxy is one running local TCP listener forwarding to a single peer
// service.
type proxy struct {
	listener  net.Listener
	localPort int

	connsMu sync.Mutex
	conns   map[net.Conn]struct{}
}

// Feature implements realm.Feature.
type Feature struct {
	mu  sync.Mutex
	reg *realm.Registrar

	store *Store

	proxiesMu sync.Mutex
	proxies   map[string]*proxy // by peerID+"|"+serviceName
}

// New builds the services Feature backed by store (see NewStore), which
// remembers which proxies the user explicitly started so RestoreAll can
// start them again on the next app start. The services this peer itself
// offers come from the currently-applied Config.Services (via the
// Registrar), so there's nothing else feature-specific to configure at
// construction time.
func New(store *Store) *Feature {
	return &Feature{proxies: make(map[string]*proxy), store: store}
}

func (f *Feature) registrar() *realm.Registrar {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reg
}

func (f *Feature) Name() string { return FeatureName }

func (f *Feature) Actions() []model.PermissionAction {
	return []model.PermissionAction{ActionConnect}
}

func (f *Feature) RegisterHandlers(reg *realm.Registrar) {
	f.mu.Lock()
	f.reg = reg
	f.mu.Unlock()
	reg.SetStreamHandler(TunnelProtocolID, f.handleTunnelStream(reg))
}

// handleTunnelStream is the libp2p stream handler for TunnelProtocolID: one
// forwarded TCP connection per stream.
func (f *Feature) handleTunnelStream(reg *realm.Registrar) network.StreamHandler {
	return func(s network.Stream) {
		remote := s.Conn().RemotePeer()
		_ = s.SetDeadline(time.Now().Add(ioTimeout))

		var header tunnelHeader
		if err := json.NewDecoder(io.LimitReader(s, maxBytes)).Decode(&header); err != nil {
			log.Printf("realm services: failed to decode tunnel header from %s: %v", remote, err)
			s.Close()
			return
		}

		if !reg.IsAllowed(remote, ActionConnect) {
			log.Printf("realm services: tunnel request from %s rejected: no permission", remote)
			_ = json.NewEncoder(s).Encode(tunnelAck{OK: false, Error: "not allowed"})
			s.Close()
			return
		}

		cfg := reg.Config()
		var svc *model.Service
		for i := range cfg.Services {
			if cfg.Services[i].Name == header.ServiceName {
				svc = &cfg.Services[i]
				break
			}
		}
		if svc == nil {
			_ = json.NewEncoder(s).Encode(tunnelAck{OK: false, Error: "no such service"})
			s.Close()
			return
		}
		if svc.Type == model.ServiceTypeUDP || svc.Type == model.ServiceTypeVPN {
			_ = json.NewEncoder(s).Encode(tunnelAck{OK: false, Error: "raw UDP forwarding isn't supported yet"})
			s.Close()
			return
		}

		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", svc.Hostname, svc.Port), ioTimeout)
		if err != nil {
			_ = json.NewEncoder(s).Encode(tunnelAck{OK: false, Error: err.Error()})
			s.Close()
			return
		}

		if err := json.NewEncoder(s).Encode(tunnelAck{OK: true}); err != nil {
			log.Printf("realm services: failed to ack tunnel to %s: %v", remote, err)
			s.Close()
			conn.Close()
			return
		}

		// The data phase is long-lived: clear the handshake deadline so an
		// idle-but-open connection doesn't get killed.
		_ = s.SetDeadline(time.Time{})
		splice(s, conn)
	}
}

// splice copies bytes both ways between a and b until either side closes,
// then closes both.
func splice(a io.ReadWriteCloser, b io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(a, b)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(b, a)
		done <- struct{}{}
	}()
	<-done
	a.Close()
	b.Close()
	<-done
}

// StartProxy binds a local TCP listener on a deterministic port derived from
// peerID+serviceName (linear-probed forward through the port range on
// collision) and starts forwarding accepted connections to serviceName on
// peer peerID over TunnelProtocolID. It returns the bound local port
// immediately, without waiting for any connection.
func (f *Feature) StartProxy(peerID, serviceName string) (int, error) {
	reg := f.registrar()
	if reg == nil {
		return 0, fmt.Errorf("realm services: not registered on an engine")
	}
	h := reg.Host()
	ctx := reg.Context()
	if h == nil || ctx == nil {
		return 0, fmt.Errorf("realm services: not running")
	}
	if _, err := peer.Decode(peerID); err != nil {
		return 0, fmt.Errorf("realm services: invalid peer id %q: %w", peerID, err)
	}

	key := proxyKey(peerID, serviceName)

	f.proxiesMu.Lock()
	if existing, ok := f.proxies[key]; ok {
		f.proxiesMu.Unlock()
		return existing.localPort, nil
	}
	f.proxiesMu.Unlock()

	listener, port, err := listenDeterministic(peerID, serviceName)
	if err != nil {
		return 0, fmt.Errorf("realm services: failed to bind a local proxy port: %w", err)
	}

	p := &proxy{listener: listener, localPort: port, conns: make(map[net.Conn]struct{})}

	f.proxiesMu.Lock()
	if existing, ok := f.proxies[key]; ok {
		f.proxiesMu.Unlock()
		listener.Close()
		return existing.localPort, nil
	}
	f.proxies[key] = p
	f.proxiesMu.Unlock()

	if f.store != nil {
		f.store.Add(peerID, serviceName)
	}

	go f.acceptLoop(reg, peerID, serviceName, key, p)

	return port, nil
}

// acceptLoop accepts local connections on p.listener and forwards each one to
// serviceName on peerID until the listener is closed by StopProxy/StopAll.
func (f *Feature) acceptLoop(reg *realm.Registrar, peerID, serviceName, key string, p *proxy) {
	pid, err := peer.Decode(peerID)
	if err != nil {
		return
	}
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			return
		}
		p.connsMu.Lock()
		p.conns[conn] = struct{}{}
		p.connsMu.Unlock()

		go func() {
			defer func() {
				p.connsMu.Lock()
				delete(p.conns, conn)
				p.connsMu.Unlock()
			}()
			f.forward(reg, pid, serviceName, conn)
		}()
	}
}

// forward proxies one accepted local connection to serviceName on pid.
func (f *Feature) forward(reg *realm.Registrar, pid peer.ID, serviceName string, conn net.Conn) {
	h := reg.Host()
	ctx := reg.Context()
	if h == nil || ctx == nil {
		conn.Close()
		return
	}

	if err := reg.EnsureConnected(ctx, pid); err != nil {
		log.Printf("realm services: peer %s unreachable to open tunnel: %v", pid, err)
		conn.Close()
		return
	}

	streamCtx, cancel := context.WithTimeout(ctx, ioTimeout)
	s, err := h.NewStream(streamCtx, pid, TunnelProtocolID)
	cancel()
	if err != nil {
		log.Printf("realm services: peer %s unreachable to open tunnel: %v", pid, err)
		conn.Close()
		return
	}
	_ = s.SetDeadline(time.Now().Add(ioTimeout))

	if err := json.NewEncoder(s).Encode(tunnelHeader{ServiceName: serviceName}); err != nil {
		s.Close()
		conn.Close()
		return
	}
	var ack tunnelAck
	if err := json.NewDecoder(io.LimitReader(s, maxBytes)).Decode(&ack); err != nil {
		s.Close()
		conn.Close()
		return
	}
	if !ack.OK {
		log.Printf("realm services: %s refused tunnel to %q: %s", pid, serviceName, ack.Error)
		s.Close()
		conn.Close()
		return
	}

	_ = s.SetDeadline(time.Time{})
	splice(s, conn)
}

// StopProxy closes the local listener and every in-flight connection for
// peerID+serviceName, if one is running, and forgets it so it won't be
// restarted by RestoreAll on the next app start. For an app-shutdown stop
// that should still be restored next time, use StopAll instead.
func (f *Feature) StopProxy(peerID, serviceName string) error {
	err := f.stopProxyLocal(peerID, serviceName)
	if f.store != nil {
		f.store.Remove(peerID, serviceName)
	}
	return err
}

// stopProxyLocal closes the local listener and every in-flight connection
// for peerID+serviceName, if one is running, without touching the
// persisted "should be running" record.
func (f *Feature) stopProxyLocal(peerID, serviceName string) error {
	key := proxyKey(peerID, serviceName)
	f.proxiesMu.Lock()
	p, ok := f.proxies[key]
	if ok {
		delete(f.proxies, key)
	}
	f.proxiesMu.Unlock()
	if !ok {
		return nil
	}
	p.listener.Close()
	p.connsMu.Lock()
	for conn := range p.conns {
		conn.Close()
	}
	p.connsMu.Unlock()
	return nil
}

// StopAll stops every running proxy without forgetting any of them, so
// RestoreAll starts them all again on the next app start. Called on
// shutdown/disable, since Engine.Stop only tears down the libp2p host and
// has no hook back into features for their own off-host state (local
// listeners/goroutines).
func (f *Feature) StopAll() {
	f.proxiesMu.Lock()
	keys := make([]string, 0, len(f.proxies))
	for key := range f.proxies {
		keys = append(keys, key)
	}
	f.proxiesMu.Unlock()
	for _, key := range keys {
		peerID, serviceName := splitProxyKey(key)
		_ = f.stopProxyLocal(peerID, serviceName)
	}
}

// RestoreAll starts a proxy for every service the user had previously,
// explicitly started (persisted via Store), so they come back up again on
// the next app start rather than staying stopped until the user notices
// and restarts them by hand. Called once after the realm engine is up.
// Failures (e.g. the peer isn't reachable yet) are logged and skipped —
// the user can retry from the UI.
func (f *Feature) RestoreAll() {
	if f.store == nil {
		return
	}
	for _, p := range f.store.List() {
		if _, err := f.StartProxy(p.PeerID, p.ServiceName); err != nil {
			log.Printf("realm services: failed to restore proxy for peer %s service %q: %v", p.PeerID, p.ServiceName, err)
		}
	}
}

// ListActive returns every currently-running proxy.
func (f *Feature) ListActive() []ActiveProxy {
	f.proxiesMu.Lock()
	defer f.proxiesMu.Unlock()
	result := make([]ActiveProxy, 0, len(f.proxies))
	for key, p := range f.proxies {
		peerID, serviceName := splitProxyKey(key)
		result = append(result, ActiveProxy{PeerID: peerID, ServiceName: serviceName, LocalPort: p.localPort})
	}
	return result
}

// IsPeerInUse reports whether id has a running proxy currently forwarding at
// least one live connection, per realm.PeerInUseHook: an idle proxy (bound
// but with no open connection) doesn't need id to stay connected, since a
// new local connection would just reconnect on demand via EnsureConnected.
func (f *Feature) IsPeerInUse(id peer.ID) bool {
	peerID := id.String()
	f.proxiesMu.Lock()
	defer f.proxiesMu.Unlock()
	for key, p := range f.proxies {
		pid, _ := splitProxyKey(key)
		if pid != peerID {
			continue
		}
		p.connsMu.Lock()
		active := len(p.conns) > 0
		p.connsMu.Unlock()
		if active {
			return true
		}
	}
	return false
}

// OnPeerRemoved stops every proxy tunneling to id, per realm.PeerRemovedHook.
func (f *Feature) OnPeerRemoved(id string) {
	f.proxiesMu.Lock()
	var keys []string
	for key := range f.proxies {
		peerID, _ := splitProxyKey(key)
		if peerID == id {
			keys = append(keys, key)
		}
	}
	f.proxiesMu.Unlock()
	for _, key := range keys {
		peerID, serviceName := splitProxyKey(key)
		_ = f.StopProxy(peerID, serviceName)
	}
}

func proxyKey(peerID, serviceName string) string { return peerID + "|" + serviceName }

func splitProxyKey(key string) (peerID, serviceName string) {
	for i := 0; i < len(key); i++ {
		if key[i] == '|' {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}

// listenDeterministic binds a local TCP listener on the port
// hash(peerID+serviceName) picks within [localPortRangeStart,
// localPortRangeEnd], linear-probing forward through the range if that port
// is already taken by something else.
func listenDeterministic(peerID, serviceName string) (net.Listener, int, error) {
	rangeSize := localPortRangeEnd - localPortRangeStart + 1
	h := fnv.New32a()
	_, _ = h.Write([]byte(peerID + "|" + serviceName))
	start := int(h.Sum32() % uint32(rangeSize))

	var lastErr error
	for i := 0; i < rangeSize; i++ {
		port := localPortRangeStart + (start+i)%rangeSize
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			return listener, port, nil
		}
		lastErr = err
	}
	return nil, 0, fmt.Errorf("no free port in range %d-%d: %w", localPortRangeStart, localPortRangeEnd, lastErr)
}
