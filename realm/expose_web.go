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

		NextProtos: []string{"http/1.1"},
	}, nil
}

type exposeWebSettingsSnapshot struct {
	enabled          bool
	listenProtocol   string
	listenPort       int
	announceHost     string
	announcePort     int
	announceProtocol string
}

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

func outboundIPv4() (string, error) {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return "", fmt.Errorf("failed to determine outbound IP: %w", err)
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String(), nil
}
