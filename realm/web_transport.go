package realm

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/transport"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// Custom multiaddr protocols for the web listener (see
// exposeWebListenAddr/exposeWebAnnounceAddr): distinct from the standard
// multiaddr /http and /ws codes so nothing else in go-libp2p mistakes one of
// these addresses for a semantic libp2phttp or websocket-transport endpoint.
// Codes are picked from multicodec's private-use range.
const (
	protoCodeRealmHTTP  = 0x300701
	protoCodeRealmHTTPS = 0x300702
)

func init() {
	for _, p := range []ma.Protocol{
		{Name: "realm-http", Code: protoCodeRealmHTTP, VCode: ma.CodeToVarint(protoCodeRealmHTTP), Size: 16, Transcoder: ma.TranscoderPort},
		{Name: "realm-https", Code: protoCodeRealmHTTPS, VCode: ma.CodeToVarint(protoCodeRealmHTTPS), Size: 16, Transcoder: ma.TranscoderPort},
	} {
		if err := ma.AddProtocol(p); err != nil {
			panic(fmt.Sprintf("realm: failed to register multiaddr protocol %q: %v", p.Name, err))
		}
	}
}

// webTransport is a libp2p Transport for the realm-http(s) multiaddr
// scheme: Dial opens a WebSocket connection to the remote's /p2p endpoint;
// Listen runs a standalone HTTP(S) server (self-signed cert when secure)
// that serves an informational index page plus a /p2p WebSocket endpoint,
// accepting each upgraded connection directly into libp2p the same way the
// tcp transport would accept a raw TCP connection. Either side hands its
// raw byte stream straight to libp2p's usual security/muxer upgrade — there
// is no separate local TCP redial or bridging step.
type webTransport struct {
	upgrader      transport.Upgrader
	rcmgr         network.ResourceManager
	tlsClientConf *tls.Config
}

var _ transport.Transport = (*webTransport)(nil)

func newWebTransport(u transport.Upgrader, rcmgr network.ResourceManager) (*webTransport, error) {
	if rcmgr == nil {
		rcmgr = &network.NullResourceManager{}
	}
	return &webTransport{
		upgrader: u,
		rcmgr:    rcmgr,
		// Any peer with ExposeWebEnabled uses a self-signed cert
		// (generateSelfSignedTLSConfig); there's no shared CA to validate
		// against. Safe to skip: libp2p's Noise handshake on top actually
		// authenticates the peer.
		tlsClientConf: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // see doc comment
	}, nil
}

func (t *webTransport) Protocols() []int { return []int{protoCodeRealmHTTP, protoCodeRealmHTTPS} }

func (t *webTransport) Proxy() bool { return false }

func (t *webTransport) CanDial(addr ma.Multiaddr) bool {
	_, _, _, err := parseRealmWebMultiaddr(addr)
	return err == nil
}

// Dial opens a WebSocket connection to raddr's /p2p endpoint and upgrades
// it into a full libp2p connection.
func (t *webTransport) Dial(ctx context.Context, raddr ma.Multiaddr, p peer.ID) (transport.CapableConn, error) {
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

func (t *webTransport) dialWithScope(ctx context.Context, raddr ma.Multiaddr, p peer.ID, connScope network.ConnManagementScope) (transport.CapableConn, error) {
	host, port, secure, err := parseRealmWebMultiaddr(raddr)
	if err != nil {
		return nil, err
	}
	scheme := "ws"
	if secure {
		scheme = "wss"
	}
	url := fmt.Sprintf("%s://%s:%s%s", scheme, host, port, webBridgePath)

	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		Proxy:            websocket.DefaultDialer.Proxy,
	}
	if secure {
		dialer.TLSClientConfig = t.tlsClientConf
	}
	wsConn, _, err := dialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("realm web transport: dial %s: %w", url, err)
	}

	macon, err := newWebConn(wsConn, raddr)
	if err != nil {
		wsConn.Close()
		return nil, err
	}

	return t.upgrader.Upgrade(ctx, t, macon, network.DirOutbound, p, connScope)
}

// Listen starts a standalone HTTP(S) server on laddr's port (self-signed
// cert when the realm-https protocol is used) and returns a Listener whose
// Accept() yields fully upgraded libp2p connections, one per /p2p
// WebSocket connection made to it.
func (t *webTransport) Listen(laddr ma.Multiaddr) (transport.Listener, error) {
	host, port, secure, err := parseRealmWebMultiaddr(laddr)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, fmt.Errorf("realm web transport: listen: %w", err)
	}

	wl := &webListener{
		rcmgr:    t.rcmgr,
		ln:       ln,
		secure:   secure,
		incoming: make(chan webAcceptedConn),
		closed:   make(chan struct{}),
	}

	maProto := "realm-https"
	if !secure {
		maProto = "realm-http"
	}
	// ln.Addr() has the actually-bound port, in case laddr's port was 0.
	actualPort := ln.Addr().(*net.TCPAddr).Port
	wl.laddr, err = ma.NewMultiaddr(fmt.Sprintf("/ip4/%s/%s/%d", host, maProto, actualPort))
	if err != nil {
		ln.Close()
		return nil, fmt.Errorf("realm web transport: listen addr: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleWebBridgeIndex)
	mux.HandleFunc(webBridgePath, wl.handleP2P)
	wl.server = &http.Server{Handler: mux}

	if secure {
		tlsConf, err := generateSelfSignedTLSConfig()
		if err != nil {
			ln.Close()
			return nil, fmt.Errorf("realm web transport: %w", err)
		}
		wl.server.TLSConfig = tlsConf
		go func() {
			if err := wl.server.ServeTLS(ln, "", ""); err != nil && err != http.ErrServerClosed {
				log.Printf("realm web transport: https listener error: %v", err)
			}
		}()
	} else {
		go func() {
			if err := wl.server.Serve(ln); err != nil && err != http.ErrServerClosed {
				log.Printf("realm web transport: http listener error: %v", err)
			}
		}()
	}

	return t.upgrader.UpgradeGatedMaListener(t, wl), nil
}

// parseRealmWebMultiaddr extracts the dial/listen target from a
// realm-http(s) multiaddr, e.g. "/dns4/example.com/realm-https/8443" or
// "/ip4/0.0.0.0/realm-http/8080".
func parseRealmWebMultiaddr(addr ma.Multiaddr) (host, port string, secure bool, err error) {
	for _, code := range []int{ma.P_IP4, ma.P_IP6, ma.P_DNS, ma.P_DNS4, ma.P_DNS6} {
		if v, verr := addr.ValueForProtocol(code); verr == nil {
			host = v
			break
		}
	}
	if host == "" {
		return "", "", false, fmt.Errorf("realm web transport: no host component in %s", addr)
	}
	if v, verr := addr.ValueForProtocol(protoCodeRealmHTTPS); verr == nil {
		return host, v, true, nil
	}
	if v, verr := addr.ValueForProtocol(protoCodeRealmHTTP); verr == nil {
		return host, v, false, nil
	}
	return "", "", false, fmt.Errorf("realm web transport: no realm-http(s) component in %s", addr)
}

// webAcceptedConn pairs an accepted connection with the resource-management
// scope opened for it, matching transport.GatedMaListener.Accept's contract.
type webAcceptedConn struct {
	conn  *webConn
	scope network.ConnManagementScope
}

// webListener implements transport.GatedMaListener: an HTTP(S) server
// (started by webTransport.Listen) hands each successfully upgraded /p2p
// WebSocket connection to Accept via incoming, instead of returning from a
// blocking net.Listener.Accept call directly.
type webListener struct {
	rcmgr  network.ResourceManager
	ln     net.Listener
	server *http.Server
	laddr  ma.Multiaddr
	secure bool

	incoming  chan webAcceptedConn
	closeOnce sync.Once
	closed    chan struct{}
}

var _ transport.GatedMaListener = (*webListener)(nil)

func (l *webListener) handleP2P(w http.ResponseWriter, r *http.Request) {
	wsConn, err := webListenUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("realm web transport: upgrade from %s: %v", r.RemoteAddr, err)
		return
	}

	raddr, err := manet.FromNetAddr(wsConn.RemoteAddr())
	if err != nil {
		log.Printf("realm web transport: remote addr: %v", err)
		wsConn.Close()
		return
	}
	macon, err := newWebConn(wsConn, raddr)
	if err != nil {
		log.Printf("realm web transport: %v", err)
		wsConn.Close()
		return
	}

	scope, err := l.rcmgr.OpenConnection(network.DirInbound, false, raddr)
	if err != nil {
		log.Printf("realm web transport: resource manager rejected inbound connection from %s: %v", raddr, err)
		wsConn.Close()
		return
	}

	select {
	case l.incoming <- webAcceptedConn{conn: macon, scope: scope}:
		// Connection has been handed to Accept(); safe to return, the
		// hijacked WebSocket stays open until macon.Close().
	case <-l.closed:
		scope.Done()
		wsConn.Close()
	}
}

func (l *webListener) Accept() (manet.Conn, network.ConnManagementScope, error) {
	select {
	case ac := <-l.incoming:
		return ac.conn, ac.scope, nil
	case <-l.closed:
		return nil, nil, transport.ErrListenerClosed
	}
}

func (l *webListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.closed)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := l.server.Shutdown(ctx); err != nil {
			l.server.Close()
		}
	})
	return nil
}

func (l *webListener) Multiaddr() ma.Multiaddr { return l.laddr }
func (l *webListener) Addr() net.Addr          { return l.ln.Addr() }

var webListenUpgrader = websocket.Upgrader{
	// Any client may connect; that's the same trust model as the rest of
	// this exposed listener — real peer auth happens afterwards, once the
	// bytes reach libp2p's own Noise handshake.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// webConn adapts a gorilla/websocket connection to manet.Conn, splitting its
// message framing back into a plain byte stream so it can carry libp2p's
// usual multistream-select/Noise/yamux negotiation.
type webConn struct {
	*websocket.Conn
	laddr ma.Multiaddr
	raddr ma.Multiaddr

	readMu sync.Mutex
	reader io.Reader

	writeMu sync.Mutex
}

var _ manet.Conn = (*webConn)(nil)

// newWebConn wraps raw, reporting raddr via RemoteMultiaddr.
func newWebConn(raw *websocket.Conn, raddr ma.Multiaddr) (*webConn, error) {
	laddr, err := manet.FromNetAddr(raw.LocalAddr())
	if err != nil {
		return nil, fmt.Errorf("realm web transport: local addr: %w", err)
	}
	return &webConn{Conn: raw, laddr: laddr, raddr: raddr}, nil
}

func (c *webConn) LocalMultiaddr() ma.Multiaddr  { return c.laddr }
func (c *webConn) RemoteMultiaddr() ma.Multiaddr { return c.raddr }

func (c *webConn) Read(b []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()

	for {
		if c.reader == nil {
			_, r, err := c.Conn.NextReader()
			if err != nil {
				return 0, mapWebConnCloseErr(err)
			}
			c.reader = r
		}
		n, err := c.reader.Read(b)
		if err == io.EOF {
			c.reader = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (c *webConn) Write(b []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if err := c.Conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (c *webConn) Close() error {
	c.writeMu.Lock()
	_ = c.Conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "closed"), time.Now().Add(100*time.Millisecond))
	c.writeMu.Unlock()
	return c.Conn.Close()
}

func (c *webConn) SetDeadline(t time.Time) error {
	if err := c.Conn.SetReadDeadline(t); err != nil {
		return err
	}
	return c.Conn.SetWriteDeadline(t)
}

func mapWebConnCloseErr(err error) error {
	if ce, ok := err.(*websocket.CloseError); ok && (ce.Code == websocket.CloseNormalClosure || ce.Code == websocket.CloseNoStatusReceived) {
		return io.EOF
	}
	return err
}
