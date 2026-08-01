package maps

import (
	"os"
	"path/filepath"
	"testing"

	"foilen-realm/model"
)

func TestApplyEventLastWriteWins(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	applied, err := s.ApplyEvent("group1", "store1", "key1", model.MapEntry{Value: "a", UpdatedAtUnixMillis: 10})
	if err != nil || !applied {
		t.Fatalf("first ApplyEvent: applied=%v err=%v", applied, err)
	}

	// Older event for the same key must be rejected.
	applied, err = s.ApplyEvent("group1", "store1", "key1", model.MapEntry{Value: "stale", UpdatedAtUnixMillis: 5})
	if err != nil || applied {
		t.Fatalf("stale ApplyEvent should not apply: applied=%v err=%v", applied, err)
	}

	// Newer event for the same key must win.
	applied, err = s.ApplyEvent("group1", "store1", "key1", model.MapEntry{Value: "b", UpdatedAtUnixMillis: 20})
	if err != nil || !applied {
		t.Fatalf("newer ApplyEvent: applied=%v err=%v", applied, err)
	}

	rm := s.GetMap("group1", "store1")
	if got := rm.Entries["key1"].Value; got != "b" {
		t.Fatalf("GetMap key1 = %q, want %q", got, "b")
	}

	// The event log is compacted per key: still exactly one event for key1.
	events := s.EventsSinceForStore("group1", "store1", 0)
	if len(events) != 1 {
		t.Fatalf("EventsSinceForStore returned %d events, want 1 (compacted)", len(events))
	}
	if events[0].Value != "b" || events[0].UpdatedAtUnixMillis != 20 {
		t.Fatalf("unexpected compacted event: %+v", events[0])
	}
}

func TestEventsSinceForStoreFiltersByTimestampGroupAndStore(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if _, err := s.ApplyEvent("group1", "store1", "k1", model.MapEntry{Value: "v1", UpdatedAtUnixMillis: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyEvent("group1", "store1", "k2", model.MapEntry{Value: "v2", UpdatedAtUnixMillis: 20}); err != nil {
		t.Fatal(err)
	}
	// Different store under the same group: must not leak into store1's events.
	if _, err := s.ApplyEvent("group1", "store2", "k3", model.MapEntry{Value: "v3", UpdatedAtUnixMillis: 30}); err != nil {
		t.Fatal(err)
	}
	// Different group, same store name: must not leak either.
	if _, err := s.ApplyEvent("group2", "store1", "k4", model.MapEntry{Value: "v4", UpdatedAtUnixMillis: 40}); err != nil {
		t.Fatal(err)
	}

	events := s.EventsSinceForStore("group1", "store1", 10)
	if len(events) != 1 || events[0].Key != "k2" {
		t.Fatalf("EventsSinceForStore(group1, store1, 10) = %+v, want just k2", events)
	}

	all := s.EventsSinceForStore("group1", "store1", 0)
	if len(all) != 2 {
		t.Fatalf("EventsSinceForStore(group1, store1, 0) = %+v, want k1 and k2", all)
	}
}

func TestPeerCursorIsPerPeerAndPerStore(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if max := s.LastFromPeerForStore("group1", "store1", "peerB"); max != 0 {
		t.Fatalf("LastFromPeerForStore before any subscribe = %d, want 0", max)
	}

	// We last caught up with peerB a while ago.
	if err := s.RecordFromPeerForStore("group1", "store1", "peerB", 10); err != nil {
		t.Fatal(err)
	}
	// Since then we've received something much newer from peerC — a
	// store-wide watermark would now sit above any old event peerB might
	// still have that we never got (e.g. a missed push).
	if err := s.RecordFromPeerForStore("group1", "store1", "peerC", 100); err != nil {
		t.Fatal(err)
	}
	// A cursor for a different store under the same group/peer must be
	// independent too.
	if err := s.RecordFromPeerForStore("group1", "store2", "peerB", 999); err != nil {
		t.Fatal(err)
	}

	if got := s.LastFromPeerForStore("group1", "store1", "peerB"); got != 10 {
		t.Fatalf("LastFromPeerForStore(group1, store1, peerB) = %d, want 10 (unaffected by peerC's cursor)", got)
	}
	if got := s.LastFromPeerForStore("group1", "store1", "peerC"); got != 100 {
		t.Fatalf("LastFromPeerForStore(group1, store1, peerC) = %d, want 100", got)
	}
	if got := s.LastFromPeerForStore("group1", "store2", "peerB"); got != 999 {
		t.Fatalf("LastFromPeerForStore(group1, store2, peerB) = %d, want 999 (unaffected by store1's cursor)", got)
	}

	// An older/equal timestamp must not move the cursor backwards.
	if err := s.RecordFromPeerForStore("group1", "store1", "peerB", 5); err != nil {
		t.Fatal(err)
	}
	if got := s.LastFromPeerForStore("group1", "store1", "peerB"); got != 10 {
		t.Fatalf("LastFromPeerForStore(group1, store1, peerB) = %d after stale update, want unchanged 10", got)
	}

	reloaded, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reload NewStore: %v", err)
	}
	if got := reloaded.LastFromPeerForStore("group1", "store1", "peerB"); got != 10 {
		t.Fatalf("after reload, LastFromPeerForStore(group1, store1, peerB) = %d, want 10", got)
	}
	if got := reloaded.LastFromPeerForStore("group1", "store1", "peerC"); got != 100 {
		t.Fatalf("after reload, LastFromPeerForStore(group1, store1, peerC) = %d, want 100", got)
	}
	if got := reloaded.LastFromPeerForStore("group1", "store2", "peerB"); got != 999 {
		t.Fatalf("after reload, LastFromPeerForStore(group1, store2, peerB) = %d, want 999", got)
	}
}

// TestDeleteValueHidesEntryButKeepsTombstoneAndListSummariesRow pins the
// per-entry delete behavior (ApplyEvent with Deleted: true against a single
// key): the map itself still exists, still shows up in ListSummaries, just
// with EntryCount 0. Unlike DeleteMap (see below), this does not remove the
// map.
func TestDeleteValueHidesEntryButKeepsTombstoneAndListSummariesRow(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if _, err := s.ApplyEvent("group1", "store1", "k1", model.MapEntry{Value: "v1", UpdatedAtUnixMillis: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyEvent("group1", "store1", "k1", model.MapEntry{Deleted: true, UpdatedAtUnixMillis: 20}); err != nil {
		t.Fatal(err)
	}

	rm := s.GetMap("group1", "store1")
	if _, ok := rm.Entries["k1"]; ok {
		t.Fatalf("GetMap should hide deleted entries, got %+v", rm.Entries)
	}

	summaries := s.ListSummaries([]model.Group{{Name: "g", KeyPair: model.KeyPair{ID: "group1"}}})
	if len(summaries) != 1 || summaries[0].EntryCount != 0 {
		t.Fatalf("ListSummaries = %+v, want one map with EntryCount 0", summaries)
	}
}

// TestDeleteMapRemovesMapFromListSummariesAndDisk verifies whole-map
// deletion (as opposed to per-entry DeleteValue above) actually purges the
// map: it disappears from ListSummaries entirely, and both on-disk files
// are removed.
func TestDeleteMapRemovesMapFromListSummariesAndDisk(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if _, err := s.ApplyEvent("group1", "store1", "k1", model.MapEntry{Value: "v1", UpdatedAtUnixMillis: 10}); err != nil {
		t.Fatal(err)
	}

	id := mapID("group1", "store1")
	statePath := filepath.Join(dir, subDirName, id+".state.json")
	eventsPath := filepath.Join(dir, subDirName, id+".events.json")
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("expected %s to exist before delete: %v", statePath, err)
	}
	if _, err := os.Stat(eventsPath); err != nil {
		t.Fatalf("expected %s to exist before delete: %v", eventsPath, err)
	}

	if err := s.DeleteMap("group1", "store1"); err != nil {
		t.Fatalf("DeleteMap: %v", err)
	}

	// Deleting an already-deleted (or never-existing) map must not error.
	if err := s.DeleteMap("group1", "store1"); err != nil {
		t.Fatalf("DeleteMap on already-deleted map: %v", err)
	}

	summaries := s.ListSummaries([]model.Group{{Name: "g", KeyPair: model.KeyPair{ID: "group1"}}})
	if len(summaries) != 0 {
		t.Fatalf("ListSummaries after DeleteMap = %+v, want none", summaries)
	}

	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed, stat err = %v", statePath, err)
	}
	if _, err := os.Stat(eventsPath); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed, stat err = %v", eventsPath, err)
	}
}

func TestStorePersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := s.ApplyEvent("group1", "store1", "k1", model.MapEntry{Value: "v1", UpdatedAtUnixMillis: 10}); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reload NewStore: %v", err)
	}
	rm := reloaded.GetMap("group1", "store1")
	if got := rm.Entries["k1"].Value; got != "v1" {
		t.Fatalf("after reload, key1 = %q, want %q", got, "v1")
	}
	if events := reloaded.EventsSinceForStore("group1", "store1", 0); len(events) != 1 || events[0].UpdatedAtUnixMillis != 10 {
		t.Fatalf("after reload, EventsSinceForStore = %+v, want one event at ts 10", events)
	}
}

func TestSubscribeFiresEntryAddedUpdatedDeleted(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	var got []model.ChangeEvent
	unsubscribe := s.Subscribe(func(ev model.ChangeEvent) {
		got = append(got, ev)
	})

	if _, err := s.ApplyEvent("group1", "store1", "k1", model.MapEntry{Value: "v1", UpdatedAtUnixMillis: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyEvent("group1", "store1", "k1", model.MapEntry{Value: "v2", UpdatedAtUnixMillis: 20}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyEvent("group1", "store1", "k1", model.MapEntry{Deleted: true, UpdatedAtUnixMillis: 30}); err != nil {
		t.Fatal(err)
	}
	// A delete applied against an already-tombstoned key changes nothing
	// visible, so no event should fire for it.
	if _, err := s.ApplyEvent("group1", "store1", "k1", model.MapEntry{Deleted: true, UpdatedAtUnixMillis: 40}); err != nil {
		t.Fatal(err)
	}

	if len(got) != 3 {
		t.Fatalf("got %d change events, want 3 (add, update, delete): %+v", len(got), got)
	}
	if got[0].Type != model.EntryAdded || got[0].Old != nil || got[0].New == nil || got[0].New.Value != "v1" {
		t.Fatalf("event[0] = %+v, want EntryAdded with New.Value=v1", got[0])
	}
	if got[1].Type != model.EntryUpdated || got[1].Old == nil || got[1].Old.Value != "v1" || got[1].New == nil || got[1].New.Value != "v2" {
		t.Fatalf("event[1] = %+v, want EntryUpdated from v1 to v2", got[1])
	}
	if got[2].Type != model.EntryDeleted || got[2].New != nil || got[2].Old == nil || got[2].Old.Value != "v2" {
		t.Fatalf("event[2] = %+v, want EntryDeleted with Old.Value=v2", got[2])
	}

	unsubscribe()
	if _, err := s.ApplyEvent("group1", "store1", "k2", model.MapEntry{Value: "v3", UpdatedAtUnixMillis: 50}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d change events after unsubscribe, want still 3", len(got))
	}
}

func TestSubscribeRecreatingAfterDeleteIsAnAddNotAnUpdate(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if _, err := s.ApplyEvent("group1", "store1", "k1", model.MapEntry{Value: "v1", UpdatedAtUnixMillis: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyEvent("group1", "store1", "k1", model.MapEntry{Deleted: true, UpdatedAtUnixMillis: 20}); err != nil {
		t.Fatal(err)
	}

	var got []model.ChangeEvent
	s.Subscribe(func(ev model.ChangeEvent) {
		got = append(got, ev)
	})

	// Recreating a key whose only prior state was a tombstone must look
	// like a brand-new key to a listener, not an update.
	if _, err := s.ApplyEvent("group1", "store1", "k1", model.MapEntry{Value: "v3", UpdatedAtUnixMillis: 30}); err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0].Type != model.EntryAdded || got[0].Old != nil {
		t.Fatalf("event = %+v, want a single EntryAdded with Old=nil", got)
	}
}
