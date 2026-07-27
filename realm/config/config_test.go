package config

import (
	"os"
	"path/filepath"
	"testing"

	"foilen-realm/model"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	svc, err := NewInDir(dir, model.DhtModeServer)
	if err != nil {
		t.Fatalf("NewInDir() error = %v", err)
	}

	want := model.Config{
		PeerID:     model.KeyPair{ID: "peer1", PrivateKeyBase64: "abc"},
		Groups:     []model.Group{{Name: "family", KeyPair: model.KeyPair{ID: "peer2", PrivateKeyBase64: "def"}}},
		DhtMode:    model.DhtModeClient,
		EnableMdns: false,
		EnableDht:  true,
	}
	if err := svc.Save(want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got := svc.Load()
	if got.PeerID != want.PeerID || got.DhtMode != want.DhtMode || got.EnableMdns != want.EnableMdns || got.EnableDht != want.EnableDht {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
	if len(got.Groups) != 1 || got.Groups[0] != want.Groups[0] {
		t.Errorf("Load() Groups = %+v, want %+v", got.Groups, want.Groups)
	}
}

func TestLoadMissingFileReturnsPlatformDefault(t *testing.T) {
	dir := t.TempDir()
	svc, err := NewInDir(dir, model.DhtModeClient)
	if err != nil {
		t.Fatalf("NewInDir() error = %v", err)
	}

	got := svc.Load()
	want := model.Config{DhtMode: model.DhtModeClient, EnableMdns: true, EnableDht: true}
	if got.DhtMode != want.DhtMode || got.EnableMdns != want.EnableMdns || got.EnableDht != want.EnableDht || len(got.Groups) != 0 {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadCorruptFileReturnsPlatformDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, configFileName), []byte("not json"), 0o644); err != nil {
		t.Fatalf("setup WriteFile error = %v", err)
	}
	svc, err := NewInDir(dir, model.DhtModeServer)
	if err != nil {
		t.Fatalf("NewInDir() error = %v", err)
	}

	got := svc.Load()
	want := model.Config{DhtMode: model.DhtModeServer, EnableMdns: true, EnableDht: true}
	if got.DhtMode != want.DhtMode || got.EnableMdns != want.EnableMdns || got.EnableDht != want.EnableDht || len(got.Groups) != 0 {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestPersistedDhtModeOverridesDefaultOnceSaved(t *testing.T) {
	dir := t.TempDir()
	svc, err := NewInDir(dir, model.DhtModeServer)
	if err != nil {
		t.Fatalf("NewInDir() error = %v", err)
	}
	if err := svc.Save(model.Config{DhtMode: model.DhtModeClient, EnableMdns: true, EnableDht: true}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// A new Service constructed with a different platform default must
	// still see the persisted value, not re-apply its own default.
	svc2, err := NewInDir(dir, model.DhtModeServer)
	if err != nil {
		t.Fatalf("NewInDir() error = %v", err)
	}
	if got := svc2.Load().DhtMode; got != model.DhtModeClient {
		t.Errorf("Load().DhtMode = %q, want %q (persisted value)", got, model.DhtModeClient)
	}
}
