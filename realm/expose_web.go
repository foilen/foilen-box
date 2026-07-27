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
	"time"

	"github.com/multiformats/go-multiaddr"

	"foilen-realm/model"
)

// generateSelfSignedTLSConfig creates a throwaway, in-memory self-signed TLS
// certificate for the websocket listener's "wss" transport. It's never
// persisted and never verified by dialing peers (see
// websocketDialerTLSConfig): the outer TLS handshake only exists so the
// connection looks like ordinary HTTPS/WSS traffic to firewalls/proxies; the
// actual peer authentication happens afterwards, via libp2p's own Noise
// security handshake.
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

// websocketDialerTLSConfig is used for every outgoing websocket-secure dial,
// not just when this peer's own ExposeWebEnabled is set: any peer might have
// it enabled with a self-signed certificate (see
// generateSelfSignedTLSConfig), and there's no shared CA to validate it
// against. Skipping verification here is safe because libp2p's own Noise
// handshake, layered on top of this transport, is what actually
// authenticates the remote peer's identity.
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
// or nil if it's not enabled or ExposeWebAnnounceHost isn't set.
func exposeWebAnnounceAddr(cfg model.Config) (multiaddr.Multiaddr, error) {
	if !cfg.ExposeWebEnabled || cfg.ExposeWebAnnounceHost == "" {
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
	spec := fmt.Sprintf("/dns4/%s/tcp/%d/%s", cfg.ExposeWebAnnounceHost, port, proto)
	a, err := multiaddr.NewMultiaddr(spec)
	if err != nil {
		return nil, fmt.Errorf("expose web: invalid announce addr %q: %w", spec, err)
	}
	return a, nil
}
