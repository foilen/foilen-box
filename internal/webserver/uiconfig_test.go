package webserver

import (
	"net"
	"reflect"
	"testing"
)

func TestUIConfigSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	svc, err := newUIConfigService(dir)
	if err != nil {
		t.Fatalf("newUIConfigService() error = %v", err)
	}

	want := uiConfig{RandomPort: false, Port: 12345, TabLoadCounts: map[string]int{"realm": 3}, SubtabLoadCounts: map[string]int{"realm-main": 2}}
	if err := svc.Save(want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got := svc.Load()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestUIConfigLoadMissingFileDefaultsToRandomPort(t *testing.T) {
	dir := t.TempDir()
	svc, err := newUIConfigService(dir)
	if err != nil {
		t.Fatalf("newUIConfigService() error = %v", err)
	}

	got := svc.Load()
	if want := (uiConfig{RandomPort: true}); !reflect.DeepEqual(got, want) {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestListenForUIRandomByDefault(t *testing.T) {
	listener, err := listenForUI(uiConfig{RandomPort: true})
	if err != nil {
		t.Fatalf("listenForUI() error = %v", err)
	}
	defer listener.Close()
	if listener.Addr().(*net.TCPAddr).Port == 0 {
		t.Errorf("expected a concrete bound port, got 0")
	}
}

func TestListenForUIPinnedPort(t *testing.T) {
	// Bind a random port first, then ask listenForUI to pin exactly that
	// port to confirm the fixed-port path is actually taken.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	listener, err := listenForUI(uiConfig{RandomPort: false, Port: port})
	if err != nil {
		t.Fatalf("listenForUI() error = %v", err)
	}
	defer listener.Close()
	if got := listener.Addr().(*net.TCPAddr).Port; got != port {
		t.Errorf("listenForUI() bound port = %d, want %d", got, port)
	}
}

func TestListenForUIPinnedPortFallsBackWhenBusy(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port

	listener, err := listenForUI(uiConfig{RandomPort: false, Port: port})
	if err != nil {
		t.Fatalf("listenForUI() error = %v", err)
	}
	defer listener.Close()
	if got := listener.Addr().(*net.TCPAddr).Port; got == port {
		t.Errorf("expected fallback to a different port, still got the busy port %d", port)
	}
}
