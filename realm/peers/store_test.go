package peers

import (
	"testing"
	"time"

	"foilen-realm/model"
)

func TestUpsertAndList(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	s.Upsert(model.PeerInfo{ID: "peerB", LastSeen: time.Unix(2, 0), GroupNames: []string{"family"}}, "")
	s.Upsert(model.PeerInfo{ID: "peerA", LastSeen: time.Unix(1, 0), GroupNames: []string{"family"}, Connected: true}, "")

	got := s.List()
	if len(got) != 2 {
		t.Fatalf("List() len = %d, want 2", len(got))
	}
	if got[0].ID != "peerA" || got[1].ID != "peerB" {
		t.Errorf("List() not sorted by ID: %+v", got)
	}
	if !got[0].Connected {
		t.Errorf("List()[0].Connected = false, want true")
	}
}

func TestRemoveGroupName(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.Upsert(model.PeerInfo{ID: "peerA", GroupNames: []string{"family", "work"}}, "")
	s.Upsert(model.PeerInfo{ID: "peerB", GroupNames: []string{"work"}}, "")

	s.RemoveGroupName("work")

	got := s.List()
	if len(got) != 2 {
		t.Fatalf("List() len = %d, want 2", len(got))
	}
	peerA, _ := s.Get("peerA")
	if len(peerA.GroupNames) != 1 || peerA.GroupNames[0] != "family" {
		t.Errorf("peerA.GroupNames = %v, want [family]", peerA.GroupNames)
	}
	peerB, _ := s.Get("peerB")
	if len(peerB.GroupNames) != 0 {
		t.Errorf("peerB.GroupNames = %v, want empty", peerB.GroupNames)
	}

	s.RemoveGroupName("unknown")
	if len(s.List()) != 2 {
		t.Errorf("RemoveGroupName() for unused group should not remove peers")
	}
}

func TestPruneStale(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	now := time.Now()
	s.Upsert(model.PeerInfo{ID: "stale", LastSeen: now.Add(-48 * time.Hour)}, "")
	s.Upsert(model.PeerInfo{ID: "recent", LastSeen: now}, "")
	s.Upsert(model.PeerInfo{ID: "staleButConnected", LastSeen: now.Add(-48 * time.Hour), Connected: true}, "")

	removed := s.PruneStale(now.Add(-24 * time.Hour))

	if len(removed) != 1 || removed[0].ID != "stale" {
		t.Fatalf("PruneStale() removed = %v, want [stale]", removed)
	}
	got := s.List()
	if len(got) != 2 {
		t.Fatalf("List() len = %d, want 2", len(got))
	}
	if _, ok := s.Get("stale"); ok {
		t.Errorf("stale peer should have been removed")
	}
	if _, ok := s.Get("recent"); !ok {
		t.Errorf("recent peer should still be present")
	}
	if _, ok := s.Get("staleButConnected"); !ok {
		t.Errorf("connected peer should never be pruned")
	}
}

func TestUpsertMergesAddressesPerSourcePreferringLANFirst(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// dht sees only the public address.
	s.Upsert(model.PeerInfo{ID: "peerA", Addresses: []string{"/ip4/1.2.3.4/tcp/1"}}, "dht")
	// mdns then reports the LAN address; it must not be lost when dht
	// ticks again with only the public address.
	s.Upsert(model.PeerInfo{ID: "peerA", Addresses: []string{"/ip4/192.168.1.5/tcp/1"}}, "mdns")
	s.Upsert(model.PeerInfo{ID: "peerA", Addresses: []string{"/ip4/1.2.3.4/tcp/1"}}, "dht")

	got, ok := s.Get("peerA")
	if !ok {
		t.Fatalf("Get() peerA not found")
	}
	want := []string{"/ip4/192.168.1.5/tcp/1", "/ip4/1.2.3.4/tcp/1"}
	if len(got.Addresses) != len(want) || got.Addresses[0] != want[0] || got.Addresses[1] != want[1] {
		t.Errorf("Addresses = %v, want %v (LAN/mdns first)", got.Addresses, want)
	}

	// A source-less update (e.g. hostname/description only) must leave
	// the merged addresses untouched.
	s.Upsert(model.PeerInfo{ID: "peerA", Hostname: "host"}, "")
	got, _ = s.Get("peerA")
	if len(got.Addresses) != len(want) {
		t.Errorf("Addresses after source-less Upsert = %v, want unchanged %v", got.Addresses, want)
	}
	if got.Hostname != "host" {
		t.Errorf("Hostname = %q, want %q", got.Hostname, "host")
	}
}

func TestSetConnected(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.Upsert(model.PeerInfo{ID: "peerA"}, "")

	s.SetConnected("peerA", true)
	got := s.List()
	if len(got) != 1 || !got[0].Connected {
		t.Fatalf("List() = %+v, want peerA connected", got)
	}

	s.SetConnected("unknown", true)
	if len(s.List()) != 1 {
		t.Errorf("SetConnected() for unknown peer should not create an entry")
	}
}
