package realm

import (
	"testing"

	"foilen-realm/model"
)

func TestExposeWebListenAddr(t *testing.T) {
	if a, err := exposeWebListenAddr(model.Config{ExposeWebEnabled: false}); err != nil || a != nil {
		t.Fatalf("expected nil, nil when disabled, got %v, %v", a, err)
	}

	a, err := exposeWebListenAddr(model.Config{ExposeWebEnabled: true, ExposeWebListenPort: 443})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := a.String(), "/ip4/0.0.0.0/tcp/443/wss"; got != want {
		t.Fatalf("default protocol: got %q, want %q", got, want)
	}

	a, err = exposeWebListenAddr(model.Config{ExposeWebEnabled: true, ExposeWebListenPort: 8080, ExposeWebListenProtocol: "ws"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := a.String(), "/ip4/0.0.0.0/tcp/8080/ws"; got != want {
		t.Fatalf("explicit ws protocol: got %q, want %q", got, want)
	}
}

func TestExposeWebAnnounceAddr(t *testing.T) {
	if a, err := exposeWebAnnounceAddr(model.Config{ExposeWebEnabled: true, ExposeWebListenPort: 443}); err != nil || a != nil {
		t.Fatalf("expected nil, nil without AnnounceHost, got %v, %v", a, err)
	}

	a, err := exposeWebAnnounceAddr(model.Config{
		ExposeWebEnabled:      true,
		ExposeWebListenPort:   443,
		ExposeWebAnnounceHost: "example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := a.String(), "/dns4/example.com/tcp/443/wss"; got != want {
		t.Fatalf("default announce: got %q, want %q", got, want)
	}

	// Reverse-proxy setup: listen on plain ws on an internal port, but
	// announce wss on the proxy's public host/port.
	a, err = exposeWebAnnounceAddr(model.Config{
		ExposeWebEnabled:          true,
		ExposeWebListenProtocol:   "ws",
		ExposeWebListenPort:       8080,
		ExposeWebAnnounceHost:     "example.com",
		ExposeWebAnnouncePort:     443,
		ExposeWebAnnounceProtocol: "wss",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := a.String(), "/dns4/example.com/tcp/443/wss"; got != want {
		t.Fatalf("reverse-proxy announce: got %q, want %q", got, want)
	}
}
