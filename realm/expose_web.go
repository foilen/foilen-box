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
// "wss" listener, just to look like ordinary HTTPS/WSS to firewalls/proxies;
// real peer auth is libp2p's Noise handshake on top (websocketDialerTLSConfig).
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
	}, nil
}

// websocketDialerTLSConfig is used for every outgoing wss dial, since any
// peer may have a self-signed cert (generateSelfSignedTLSConfig) with no
// shared CA to validate against. Safe to skip: libp2p's Noise handshake on
// top actually authenticates the peer.
func websocketDialerTLSConfig() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true} //nolint:gosec // see doc comment
}

// exposeWebSettingsSnapshot is the subset of model.Config that requires a
// full host Restart (not just a Reconcile) when changed, since these are all
// only applied via libp2p.New's options.
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

// exposeWebListenAddr builds the extra listen multiaddr for cfg's websocket
// listener (see model.Config.ExposeWebEnabled), or nil if it's not enabled.
func exposeWebListenAddr(cfg model.Config) (multiaddr.Multiaddr, error) {
	if !cfg.ExposeWebEnabled {
		return nil, nil
	}
	proto := cfg.ExposeWebListenProtocol
	if proto == "" {
		proto = "wss"
	}
	spec := fmt.Sprintf("/ip4/0.0.0.0/tcp/%d/%s", cfg.ExposeWebListenPort, proto)
	a, err := multiaddr.NewMultiaddr(spec)
	if err != nil {
		return nil, fmt.Errorf("expose web: invalid listen addr %q: %w", spec, err)
	}
	return a, nil
}

// exposeWebAnnounceAddr builds the multiaddr this host should advertise to
// other peers for its websocket listener (see model.Config.ExposeWebEnabled),
// or nil if it's not enabled. Falls back to this host's outbound IP when
// ExposeWebAnnounceHost isn't set.
func exposeWebAnnounceAddr(cfg model.Config) (multiaddr.Multiaddr, error) {
	if !cfg.ExposeWebEnabled {
		return nil, nil
	}
	proto := cfg.ExposeWebAnnounceProtocol
	if proto == "" {
		proto = cfg.ExposeWebListenProtocol
	}
	if proto == "" {
		proto = "wss"
	}
	port := cfg.ExposeWebAnnouncePort
	if port == 0 {
		port = cfg.ExposeWebListenPort
	}

	var spec string
	if cfg.ExposeWebAnnounceHost != "" {
		spec = fmt.Sprintf("/dns4/%s/tcp/%d/%s", cfg.ExposeWebAnnounceHost, port, proto)
	} else {
		ip, err := outboundIPv4()
		if err != nil {
			return nil, fmt.Errorf("expose web: failed to determine local IP for announce addr: %w", err)
		}
		spec = fmt.Sprintf("/ip4/%s/tcp/%d/%s", ip, port, proto)
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
