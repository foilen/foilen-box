package maps

import (
	"testing"

	"foilen-realm/model"
)

func TestApplyEventLastWriteWins(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	applied, err := s.ApplyEvent("scope1", "store1", "key1", model.MapEntry{Value: "a", UpdatedAtUnixMillis: 10})
	if err != nil || !applied {
		t.Fatalf("first ApplyEvent: applied=%v err=%v", applied, err)
	}

	// Older event for the same key must be rejected.
	applied, err = s.ApplyEvent("scope1", "store1", "key1", model.MapEntry{Value: "stale", UpdatedAtUnixMillis: 5})
	if err != nil || applied {
		t.Fatalf("stale ApplyEvent should not apply: applied=%v err=%v", applied, err)
	}

	// Newer event for the same key must win.
	applied, err = s.ApplyEvent("scope1", "store1", "key1", model.MapEntry{Value: "b", UpdatedAtUnixMillis: 20})
	if err != nil || !applied {
		t.Fatalf("newer ApplyEvent: applied=%v err=%v", applied, err)
	}

	rm := s.GetMap("scope1", "store1")
	if got := rm.Entries["key1"].Value; got != "b" {
		t.Fatalf("GetMap key1 = %q, want %q", got, "b")
	}

	// The event log is compacted per key: still exactly one event for key1.
	events := s.EventsSince("scope1", 0)
	if len(events) != 1 {
		t.Fatalf("EventsSince returned %d events, want 1 (compacted)", len(events))
	}
	if events[0].Value != "b" || events[0].UpdatedAtUnixMillis != 20 {
		t.Fatalf("unexpected compacted event: %+v", events[0])
	}
}

func TestEventsSinceFiltersByTimestampAndScope(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if _, err := s.ApplyEvent("scope1", "store1", "k1", model.MapEntry{Value: "v1", UpdatedAtUnixMillis: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyEvent("scope1", "store2", "k2", model.MapEntry{Value: "v2", UpdatedAtUnixMillis: 20}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyEvent("scope2", "store1", "k3", model.MapEntry{Value: "v3", UpdatedAtUnixMillis: 30}); err != nil {
		t.Fatal(err)
	}

	events := s.EventsSince("scope1", 10)
	if len(events) != 1 || events[0].Key != "k2" {
		t.Fatalf("EventsSince(scope1, 10) = %+v, want just k2", events)
	}

	if max := s.MaxUpdatedAt("scope1"); max != 20 {
		t.Fatalf("MaxUpdatedAt(scope1) = %d, want 20", max)
	}
	if max := s.MaxUpdatedAt("scope-unknown"); max != 0 {
		t.Fatalf("MaxUpdatedAt(unknown) = %d, want 0", max)
	}
}

func TestPeerCursorIsPerPeerNotAScopeWideWatermark(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if max := s.LastFromPeer("scope1", "peerB"); max != 0 {
		t.Fatalf("LastFromPeer before any sync = %d, want 0", max)
	}

	// We last synced with peerB a while ago.
	if err := s.RecordFromPeer("scope1", "peerB", 10); err != nil {
		t.Fatal(err)
	}
	// Since then we've received something much newer from peerC — a
	// scope-wide watermark would now sit above any old event peerB might
	// still have that we never got (e.g. a missed push).
	if err := s.RecordFromPeer("scope1", "peerC", 100); err != nil {
		t.Fatal(err)
	}

	if got := s.LastFromPeer("scope1", "peerB"); got != 10 {
		t.Fatalf("LastFromPeer(scope1, peerB) = %d, want 10 (unaffected by peerC's cursor)", got)
	}
	if got := s.LastFromPeer("scope1", "peerC"); got != 100 {
		t.Fatalf("LastFromPeer(scope1, peerC) = %d, want 100", got)
	}

	// An older/equal timestamp must not move the cursor backwards.
	if err := s.RecordFromPeer("scope1", "peerB", 5); err != nil {
		t.Fatal(err)
	}
	if got := s.LastFromPeer("scope1", "peerB"); got != 10 {
		t.Fatalf("LastFromPeer(scope1, peerB) = %d after stale update, want unchanged 10", got)
	}

	reloaded, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reload NewStore: %v", err)
	}
	if got := reloaded.LastFromPeer("scope1", "peerB"); got != 10 {
		t.Fatalf("after reload, LastFromPeer(scope1, peerB) = %d, want 10", got)
	}
	if got := reloaded.LastFromPeer("scope1", "peerC"); got != 100 {
		t.Fatalf("after reload, LastFromPeer(scope1, peerC) = %d, want 100", got)
	}
}

func TestDeletedEntriesAreHiddenFromGetMapButKeptAsTombstones(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if _, err := s.ApplyEvent("scope1", "store1", "k1", model.MapEntry{Value: "v1", UpdatedAtUnixMillis: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyEvent("scope1", "store1", "k1", model.MapEntry{Deleted: true, UpdatedAtUnixMillis: 20}); err != nil {
		t.Fatal(err)
	}

	rm := s.GetMap("scope1", "store1")
	if _, ok := rm.Entries["k1"]; ok {
		t.Fatalf("GetMap should hide deleted entries, got %+v", rm.Entries)
	}

	summaries := s.ListSummaries([]model.Group{{Name: "g", KeyPair: model.KeyPair{ID: "scope1"}}})
	if len(summaries) != 1 || summaries[0].EntryCount != 0 {
		t.Fatalf("ListSummaries = %+v, want one map with EntryCount 0", summaries)
	}
}

func TestStorePersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := s.ApplyEvent("scope1", "store1", "k1", model.MapEntry{Value: "v1", UpdatedAtUnixMillis: 10}); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reload NewStore: %v", err)
	}
	rm := reloaded.GetMap("scope1", "store1")
	if got := rm.Entries["k1"].Value; got != "v1" {
		t.Fatalf("after reload, key1 = %q, want %q", got, "v1")
	}
	if max := reloaded.MaxUpdatedAt("scope1"); max != 10 {
		t.Fatalf("after reload, MaxUpdatedAt = %d, want 10", max)
	}
}
