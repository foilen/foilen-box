package realm

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"time"

	"github.com/multiformats/go-multiaddr"

	"foilen-realm/model"
)

// generateSelfSignedTLSConfig creates a throwaway self-signed cert for the
// web listener's HTTPS server (webTransport.Listen, web_transport.go) and
// for the matching dial-side "realm-https" TLS config; real peer auth
// happens afterwards, via libp2p's Noise handshake on top.
func generateSelfSignedTLSConfig() (*tls.Config, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("expose web: failed to generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("expose web: failed to generate serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "foilen-box realm"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return nil, fmt.Errorf("expose web: failed to create certificate: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: priv}},
		// Force HTTP/1.1: the WebSocket upgrade in web_transport.go relies on
		// hijacking the connection, which HTTP/2 doesn't support. Without
		// this, any client offering "h2" in its ALPN (e.g. curl, browsers)
		// gets negotiated to HTTP/2 and the /p2p upgrade fails with 400.
		NextProtos: []string{"http/1.1"},
	}, nil
}

// exposeWebSettingsSnapshot is the subset of model.Config that requires a
// full host Restart (not just a Reconcile) when changed: the web listener
// (webTransport, web_transport.go) is only (de)registered and (un)listened
// via Engine.Start's libp2p.New options.
type exposeWebSettingsSnapshot struct {
	enabled          bool
	listenProtocol   string
	listenPort       int
	announceHost     string
	announcePort     int
	announceProtocol string
}

// exposeWebSettings extracts cfg's comparable snapshot of ExposeWeb settings,
// for diffing in Reconcile.
func exposeWebSettings(cfg model.Config) exposeWebSettingsSnapshot {
	return exposeWebSettingsSnapshot{
		enabled:          cfg.ExposeWebEnabled,
		listenProtocol:   cfg.ExposeWebListenProtocol,
		listenPort:       cfg.ExposeWebListenPort,
		announceHost:     cfg.ExposeWebAnnounceHost,
		announcePort:     cfg.ExposeWebAnnouncePort,
		announceProtocol: cfg.ExposeWebAnnounceProtocol,
	}
}

// exposeWebListenAddr builds the multiaddr webTransport.Listen should bind
// to for cfg's web listener (see model.Config.ExposeWebEnabled), or nil if
// it's not enabled.
func exposeWebListenAddr(cfg model.Config) (multiaddr.Multiaddr, error) {
	if !cfg.ExposeWebEnabled {
		return nil, nil
	}
	maProto := "realm-https"
	if cfg.ExposeWebListenProtocol == "http" {
		maProto = "realm-http"
	}
	spec := fmt.Sprintf("/ip4/0.0.0.0/%s/%d", maProto, cfg.ExposeWebListenPort)
	a, err := multiaddr.NewMultiaddr(spec)
	if err != nil {
		return nil, fmt.Errorf("expose web: invalid listen addr %q: %w", spec, err)
	}
	return a, nil
}

// exposeWebAnnounceAddr builds the multiaddr this host should advertise to
// other peers for its web listener (see model.Config.ExposeWebEnabled and
// web_transport.go), or nil if it's not enabled. Uses the custom
// realm-http/realm-https multiaddr protocols rather than libp2p's standard
// ws/wss, since this isn't a libp2p websocket transport listener. Falls
// back to this host's outbound IP when ExposeWebAnnounceHost isn't set.
func exposeWebAnnounceAddr(cfg model.Config) (multiaddr.Multiaddr, error) {
	if !cfg.ExposeWebEnabled {
		return nil, nil
	}
	proto := cfg.ExposeWebAnnounceProtocol
	if proto == "" {
		proto = cfg.ExposeWebListenProtocol
	}
	maProto := "realm-https"
	if proto == "http" {
		maProto = "realm-http"
	}
	port := cfg.ExposeWebAnnouncePort
	if port == 0 {
		port = cfg.ExposeWebListenPort
	}

	var spec string
	if cfg.ExposeWebAnnounceHost != "" {
		spec = fmt.Sprintf("/dns4/%s/%s/%d", cfg.ExposeWebAnnounceHost, maProto, port)
	} else {
		ip, err := outboundIPv4()
		if err != nil {
			return nil, fmt.Errorf("expose web: failed to determine local IP for announce addr: %w", err)
		}
		spec = fmt.Sprintf("/ip4/%s/%s/%d", ip, maProto, port)
	}
	a, err := multiaddr.NewMultiaddr(spec)
	if err != nil {
		return nil, fmt.Errorf("expose web: invalid announce addr %q: %w", spec, err)
	}
	return a, nil
}

// outboundIPv4 returns this host's preferred outbound IPv4 address, by
// asking the OS how it would route to a public IP. No packets are actually
// sent: UDP "connect" just resolves local routing.
func outboundIPv4() (string, error) {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return "", fmt.Errorf("failed to determine outbound IP: %w", err)
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String(), nil
}
