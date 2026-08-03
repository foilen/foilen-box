package realm

import (
	"strings"
	"testing"

	"foilen-realm/model"
)

func TestExposeWebAnnounceAddr(t *testing.T) {
	if a, err := exposeWebAnnounceAddr(model.Config{ExposeWebEnabled: false, ExposeWebListenPort: 443}); err != nil || a != nil {
		t.Fatalf("expected nil, nil when disabled, got %v, %v", a, err)
	}

	a, err := exposeWebAnnounceAddr(model.Config{
		ExposeWebEnabled:      true,
		ExposeWebListenPort:   443,
		ExposeWebAnnounceHost: "example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := a.String(), "/dns4/example.com/realm-https/443"; got != want {
		t.Fatalf("default protocol: got %q, want %q", got, want)
	}

	a, err = exposeWebAnnounceAddr(model.Config{
		ExposeWebEnabled:        true,
		ExposeWebListenPort:     8080,
		ExposeWebListenProtocol: "http",
		ExposeWebAnnounceHost:   "example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := a.String(), "/dns4/example.com/realm-http/8080"; got != want {
		t.Fatalf("explicit http protocol: got %q, want %q", got, want)
	}

	// Reverse-proxy setup: listen on plain http on an internal port, but
	// announce https on the proxy's public host/port.
	a, err = exposeWebAnnounceAddr(model.Config{
		ExposeWebEnabled:          true,
		ExposeWebListenProtocol:   "http",
		ExposeWebListenPort:       8080,
		ExposeWebAnnounceHost:     "example.com",
		ExposeWebAnnouncePort:     443,
		ExposeWebAnnounceProtocol: "https",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := a.String(), "/dns4/example.com/realm-https/443"; got != want {
		t.Fatalf("reverse-proxy announce: got %q, want %q", got, want)
	}

	// No AnnounceHost: falls back to this host's outbound IP.
	a, err = exposeWebAnnounceAddr(model.Config{ExposeWebEnabled: true, ExposeWebListenPort: 443})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := a.String(); !strings.HasPrefix(got, "/ip4/") || !strings.HasSuffix(got, "/realm-https/443") {
		t.Fatalf("outbound-IP fallback: got %q", got)
	}
}
