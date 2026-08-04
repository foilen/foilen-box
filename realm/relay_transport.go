package realm

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/core/transport"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// Custom multiaddr protocol for application-level relaying (relayTransport,
// below): distinct from the standard "p2p-circuit" code so nothing in
// go-libp2p mistakes one of these addresses for a circuit-relay-v2 one.
// Code picked from multicodec's private-use range, next free after
// web_transport.go's protoCodeRealmHTTP/protoCodeRealmHTTPS.
const (
	protoCodeRealmRelay = 0x300703
	realmRelayProtoName = "realm-relay"

	relayStatusOK      byte = 0x01
	relayStatusRefused byte = 0x00

	// relayLineMaxLen bounds a hop/stop handshake line (a peer id string),
	// as a guard against a misbehaving/garbage peer on the other end.
	relayLineMaxLen = 128
)

// relayListenAddr is the bare marker address relayTransport listens on (see
// engine.go's Start): it doesn't correspond to any real socket, and is never
// advertised to other peers — it just makes libp2p invoke Listen so the hop/
// stop stream handlers are wired up before an inbound relayed connection can
// arrive. Built in init() below, after the protocol it names is registered:
// package-level var initializers all run before any init() func regardless
// of source order, so building it via a var initializer here would run
// before ma.AddProtocol and fail to parse.
var relayListenAddr ma.Multiaddr

func init() {
	p := ma.Protocol{Name: realmRelayProtoName, Code: protoCodeRealmRelay, VCode: ma.CodeToVarint(protoCodeRealmRelay), Size: 0}
	if err := ma.AddProtocol(p); err != nil {
		panic(fmt.Sprintf("realm: failed to register multiaddr protocol %q: %v", realmRelayProtoName, err))
	}

	a, err := ma.NewMultiaddr("/" + realmRelayProtoName)
	if err != nil {
		panic(fmt.Sprintf("realm: failed to build relay listen addr: %v", err))
	}
	relayListenAddr = a
}

// RelayDialAddr is the fully-qualified address this host dials to reach
// targetID through relayID: /p2p/<relayID>/realm-relay/p2p/<targetID>.
func RelayDialAddr(relayID, targetID peer.ID) (ma.Multiaddr, error) {
	return ma.NewMultiaddr("/p2p/" + relayID.String() + "/" + realmRelayProtoName + "/p2p/" + targetID.String())
}

// parseRelayMultiaddr extracts the relay peer id from a RelayDialAddr-shaped
// address. It deliberately doesn't parse (or require) the trailing
// /p2p/<target> component: go-libp2p's swarm unconditionally strips a
// multiaddr's trailing /p2p/<id> component before it ever reaches CanDial or
// Dial (stripP2PComponent in swarm_dial.go's resolveAddrs, on the assumption
// that component is always redundant with the peer id already passed to
// Transport.Dial separately) — which holds here too, since that trailing id
// is always the dial target. So Dial must use its separately-passed target
// peer.ID instead of re-parsing it from addr; see dialWithScope.
func parseRelayMultiaddr(addr ma.Multiaddr) (relayID peer.ID, err error) {
	var comps []ma.Component
	ma.ForEach(addr, func(c ma.Component) bool {
		comps = append(comps, c)
		return true
	})
	if len(comps) < 2 ||
		comps[0].Protocol().Code != ma.P_P2P ||
		comps[1].Protocol().Code != protoCodeRealmRelay {
		return "", fmt.Errorf("realm relay transport: unexpected address shape %s", addr)
	}
	relayID, err = peer.Decode(comps[0].Value())
	if err != nil {
		return "", fmt.Errorf("realm relay transport: invalid relay id in %s: %w", addr, err)
	}
	return relayID, nil
}

// relayHopProtocolID/relayStopProtocolID are the application-level relay
// wire protocols (realm/relay_transport.go), replacing circuit-relay-v2:
// the source peer (A) speaks relayHopProtocolID to the relay (R), which in
// turn speaks relayStopProtocolID to the target (B), then just pumps bytes
// between the two streams. A and B then perform their normal libp2p
// security/mux handshake straight through that pipe, so the resulting
// connection has B's real, verified peer id, exactly as if directly
// connected.
const (
	relayHopProtocolID  protocol.ID = "/foilen-box/relay-hop/1.0.0"
	relayStopProtocolID protocol.ID = "/foilen-box/relay-stop/1.0.0"
)

// relayTransport is a libp2p Transport for the realm-relay multiaddr scheme.
// Dial never dials a new network connection itself: it opens a stream over
// an existing connection to the relay named in the address (see Dial), and
// the relay in turn opens a stream over its own existing connection to the
// target (see handleHopStream) — never a fresh dial on either hop:
// handleHopStream refuses a hop request unless it's already connected to the
// requested target.
type relayTransport struct {
	upgrader transport.Upgrader
	rcmgr    network.ResourceManager
	host     host.Host
	engine   *Engine

	incoming  chan relayAcceptedConn
	closeOnce sync.Once
	closed    chan struct{}
}

var _ transport.Transport = (*relayTransport)(nil)

func (t *relayTransport) Protocols() []int { return []int{protoCodeRealmRelay} }

// Proxy reports that a connection dialed through this transport doesn't
// represent this host's own directly-reachable network address.
func (t *relayTransport) Proxy() bool { return true }

// CanDial accepts both the full RelayDialAddr shape (/p2p/<relay>/realm-relay/p2p/<target>)
// and its trailing-/p2p/<target>-stripped form: in practice the swarm always
// dials the stripped form (see parseRelayMultiaddr), so Dial receives the
// stripped form too, never the full one.
func (t *relayTransport) CanDial(addr ma.Multiaddr) bool {
	var comps []ma.Component
	ma.ForEach(addr, func(c ma.Component) bool {
		comps = append(comps, c)
		return true
	})
	return len(comps) >= 2 &&
		comps[0].Protocol().Code == ma.P_P2P &&
		comps[1].Protocol().Code == protoCodeRealmRelay
}

// Dial opens a hop stream to raddr's relay and, once the relay confirms it
// has bridged a stream to raddr's target, upgrades the resulting pipe into a
// full libp2p connection.
func (t *relayTransport) Dial(ctx context.Context, raddr ma.Multiaddr, p peer.ID) (transport.CapableConn, error) {
	connScope, err := t.rcmgr.OpenConnection(network.DirOutbound, true, raddr)
	if err != nil {
		return nil, err
	}
	conn, err := t.dialWithScope(ctx, raddr, p, connScope)
	if err != nil {
		connScope.Done()
		return nil, err
	}
	return conn, nil
}

func (t *relayTransport) dialWithScope(ctx context.Context, raddr ma.Multiaddr, target peer.ID, connScope network.ConnManagementScope) (transport.CapableConn, error) {
	relayID, err := parseRelayMultiaddr(raddr)
	if err != nil {
		return nil, err
	}

	h := t.host
	if h == nil {
		return nil, fmt.Errorf("realm relay transport: host not ready")
	}
	if h.Network().Connectedness(relayID) != network.Connected {
		return nil, fmt.Errorf("realm relay transport: not connected to relay %s", relayID)
	}

	hopStream, err := h.NewStream(ctx, relayID, relayHopProtocolID)
	if err != nil {
		return nil, fmt.Errorf("realm relay transport: open hop stream to %s: %w", relayID, err)
	}

	if err := writeRelayLine(hopStream, target.String()); err != nil {
		hopStream.Reset()
		return nil, fmt.Errorf("realm relay transport: write hop request: %w", err)
	}
	status, err := readRelayStatus(hopStream)
	if err != nil {
		hopStream.Reset()
		return nil, fmt.Errorf("realm relay transport: read hop status: %w", err)
	}
	if status != relayStatusOK {
		hopStream.Reset()
		return nil, fmt.Errorf("realm relay transport: relay %s refused to relay to %s", relayID, target)
	}

	localAddr, err := ma.NewMultiaddr("/p2p/" + h.ID().String() + "/" + realmRelayProtoName)
	if err != nil {
		hopStream.Reset()
		return nil, err
	}

	macon := &relayStreamConn{Stream: hopStream, local: localAddr, remote: raddr}
	return t.upgrader.Upgrade(ctx, t, macon, network.DirOutbound, target, connScope)
}

// Listen returns a listener that never binds a real socket: its Accept()
// yields each inbound relayed connection handed to it by handleStopStream.
func (t *relayTransport) Listen(laddr ma.Multiaddr) (transport.Listener, error) {
	l := &relayListener{t: t, laddr: laddr}
	return t.upgrader.UpgradeGatedMaListener(t, l), nil
}

// handleHopStream runs on the relay (R): src (A) asks to be bridged to a
// target peer id (B). Refuses unless R is already connected to B (never
// dials) and both A and B share a group with R; on success, opens a stream
// to B over R's existing connection and pumps bytes between the two streams
// until either side closes.
func (t *relayTransport) handleHopStream(s network.Stream) {
	src := s.Conn().RemotePeer()
	targetLine, err := readRelayLine(s)
	if err != nil {
		s.Reset()
		return
	}
	target, err := peer.Decode(targetLine)
	if err != nil {
		log.Printf("realm relay transport: hop request from %s: invalid target %q: %v", t.engine.peers.Label(src.String()), targetLine, err)
		writeRelayStatus(s, relayStatusRefused)
		s.Close()
		return
	}

	h := t.host
	e := t.engine
	if h == nil || e == nil || !e.relayServiceEnabled() {
		writeRelayStatus(s, relayStatusRefused)
		s.Close()
		return
	}
	if h.Network().Connectedness(target) != network.Connected {
		writeRelayStatus(s, relayStatusRefused)
		s.Close()
		return
	}
	if !e.peerInCommonGroup(src) || !e.peerInCommonGroup(target) {
		writeRelayStatus(s, relayStatusRefused)
		s.Close()
		return
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	stopStream, err := h.NewStream(stopCtx, target, relayStopProtocolID)
	cancel()
	if err != nil {
		writeRelayStatus(s, relayStatusRefused)
		s.Close()
		return
	}
	if err := writeRelayLine(stopStream, src.String()); err != nil {
		stopStream.Reset()
		writeRelayStatus(s, relayStatusRefused)
		s.Close()
		return
	}
	status, err := readRelayStatus(stopStream)
	if err != nil || status != relayStatusOK {
		stopStream.Reset()
		writeRelayStatus(s, relayStatusRefused)
		s.Close()
		return
	}

	if err := writeRelayStatus(s, relayStatusOK); err != nil {
		stopStream.Reset()
		s.Reset()
		return
	}

	log.Printf("realm relay transport: relaying %s -> %s", e.peers.Label(src.String()), e.peers.Label(target.String()))
	pumpRelayStreams(s, stopStream)
}

// handleStopStream runs on the target (B): accepts the relay's bridged
// stream as a normal inbound connection attempt, wrapping it the same way
// Dial wraps the outbound hop stream, so the swarm's usual security/mux
// upgrade runs straight through the pipe and B ends up with A's real,
// verified peer id.
func (t *relayTransport) handleStopStream(s network.Stream) {
	relayPeer := s.Conn().RemotePeer()
	srcLine, err := readRelayLine(s)
	if err != nil {
		s.Reset()
		return
	}

	if err := writeRelayStatus(s, relayStatusOK); err != nil {
		s.Reset()
		return
	}
	log.Printf("realm relay transport: accepting relayed connection from %s via %s", srcLine, t.engine.peers.Label(relayPeer.String()))

	localAddr, err := ma.NewMultiaddr("/p2p/" + t.host.ID().String() + "/" + realmRelayProtoName)
	if err != nil {
		s.Reset()
		return
	}
	remoteAddr, err := ma.NewMultiaddr("/p2p/" + relayPeer.String() + "/" + realmRelayProtoName)
	if err != nil {
		s.Reset()
		return
	}

	connScope, err := t.rcmgr.OpenConnection(network.DirInbound, false, remoteAddr)
	if err != nil {
		s.Reset()
		return
	}

	macon := &relayStreamConn{Stream: s, local: localAddr, remote: remoteAddr}

	select {
	case t.incoming <- relayAcceptedConn{conn: macon, scope: connScope}:
	case <-t.closed:
		connScope.Done()
		s.Reset()
	}
}

// pumpRelayStreams copies bytes bidirectionally between a and b until both
// directions finish (EOF or error), then closes both.
func pumpRelayStreams(a, b network.Stream) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(a, b) //nolint:errcheck // best-effort pump; either side closing ends it
		a.CloseWrite()
	}()
	go func() {
		defer wg.Done()
		io.Copy(b, a) //nolint:errcheck
		b.CloseWrite()
	}()
	wg.Wait()
	a.Close()
	b.Close()
}

func writeRelayLine(w io.Writer, s string) error {
	_, err := w.Write([]byte(s + "\n"))
	return err
}

// readRelayLine reads a single newline-terminated line byte-by-byte
// (instead of via bufio) so the stream isn't left with buffered-but-unread
// bytes belonging to whatever comes after the line (a status byte, or the
// relayed payload itself).
func readRelayLine(r io.Reader) (string, error) {
	var buf []byte
	var b [1]byte
	for {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", err
		}
		if b[0] == '\n' {
			return string(buf), nil
		}
		buf = append(buf, b[0])
		if len(buf) > relayLineMaxLen {
			return "", fmt.Errorf("realm relay transport: line too long")
		}
	}
}

func writeRelayStatus(w io.Writer, status byte) error {
	_, err := w.Write([]byte{status})
	return err
}

func readRelayStatus(r io.Reader) (byte, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}

// relayStreamConn adapts a network.Stream (either the hop stream held by the
// dialing side, or the stop stream handed to the accepting side) to
// manet.Conn, so it can carry libp2p's usual security/mux handshake the same
// way a raw TCP connection would.
type relayStreamConn struct {
	network.Stream
	local  ma.Multiaddr
	remote ma.Multiaddr
}

var _ manet.Conn = (*relayStreamConn)(nil)

func (c *relayStreamConn) LocalAddr() net.Addr           { return &relayNetAddr{c.local} }
func (c *relayStreamConn) RemoteAddr() net.Addr          { return &relayNetAddr{c.remote} }
func (c *relayStreamConn) LocalMultiaddr() ma.Multiaddr  { return c.local }
func (c *relayStreamConn) RemoteMultiaddr() ma.Multiaddr { return c.remote }

type relayNetAddr struct{ addr ma.Multiaddr }

func (a *relayNetAddr) Network() string { return realmRelayProtoName }
func (a *relayNetAddr) String() string  { return a.addr.String() }

// relayAcceptedConn pairs an accepted connection with the resource-management
// scope opened for it, matching transport.GatedMaListener.Accept's contract.
type relayAcceptedConn struct {
	conn  *relayStreamConn
	scope network.ConnManagementScope
}

// relayListener implements transport.GatedMaListener: relayTransport.Listen
// returns one, and handleStopStream feeds each accepted relayed connection
// to it via t.incoming.
type relayListener struct {
	t     *relayTransport
	laddr ma.Multiaddr
}

var _ transport.GatedMaListener = (*relayListener)(nil)

func (l *relayListener) Accept() (manet.Conn, network.ConnManagementScope, error) {
	select {
	case ac := <-l.t.incoming:
		return ac.conn, ac.scope, nil
	case <-l.t.closed:
		return nil, nil, transport.ErrListenerClosed
	}
}

func (l *relayListener) Close() error {
	l.t.closeOnce.Do(func() { close(l.t.closed) })
	return nil
}

func (l *relayListener) Multiaddr() ma.Multiaddr { return l.laddr }
func (l *relayListener) Addr() net.Addr          { return &relayNetAddr{l.laddr} }
