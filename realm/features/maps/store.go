// Package maps is the "common/maps" Realm feature: small shared
// Map<String,String> key-value stores scoped to a group (see model.Group),
// replicated peer-to-peer as a signed, last-write-wins event log.
package maps

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"

	"foilen-realm/model"
)

const subDirName = "realm-maps"

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// mapID identifies one RealmMap on disk: the pair's file names are
// <mapID>.state.json and <mapID>.events.json. groupID is already a
// filesystem-safe libp2p id; storeName is arbitrary user input, so it's
// sanitized.
func mapID(groupID, storeName string) string {
	return groupID + "__" + unsafeChars.ReplaceAllString(storeName, "_")
}

// listenerEntry pairs a change-hook callback with a stable id so Subscribe's
// returned unsubscribe func can remove exactly the right one regardless of
// what else has (un)subscribed since.
type listenerEntry struct {
	id int
	fn func(model.ChangeEvent)
}

// Store caches every known RealmMap's current state and event log in
// memory, backed by one file pair per map on disk
// ($dir/realm-maps/<mapID>.state.json + <mapID>.events.json).
type Store struct {
	dir string

	mu     sync.Mutex
	states map[string]model.RealmMap   // by mapID
	events map[string][]model.MapEvent // by mapID, one entry per key

	// peerCursors: newest UpdatedAtUnixMillis received per (group, store,
	// peer) — see Feature.subscribeToPeer. Deliberately per-peer rather than
	// a shared watermark: otherwise a peer whose push we missed while
	// offline could get permanently shadowed by a later, newer peer.
	peerCursors map[string]map[string]map[string]int64 // groupID -> storeName -> peerID -> unixMillis

	listeners      []listenerEntry
	nextListenerID int
}

// NewStore creates the realm-maps directory if needed, loads any existing
// map files into memory, and returns a Store.
func NewStore(dir string) (*Store, error) {
	mapsDir := filepath.Join(dir, subDirName)
	if err := os.MkdirAll(mapsDir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: mapsDir, states: map[string]model.RealmMap{}, events: map[string][]model.MapEvent{}, peerCursors: map[string]map[string]map[string]int64{}}

	entries, err := os.ReadDir(mapsDir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		switch {
		case len(name) > len(".state.json") && name[len(name)-len(".state.json"):] == ".state.json":
			id := name[:len(name)-len(".state.json")]
			data, err := os.ReadFile(filepath.Join(mapsDir, name))
			if err != nil {
				continue
			}
			var rm model.RealmMap
			if err := json.Unmarshal(data, &rm); err != nil {
				continue
			}
			s.states[id] = rm
		case len(name) > len(".events.json") && name[len(name)-len(".events.json"):] == ".events.json":
			id := name[:len(name)-len(".events.json")]
			data, err := os.ReadFile(filepath.Join(mapsDir, name))
			if err != nil {
				continue
			}
			var evs []model.MapEvent
			if err := json.Unmarshal(data, &evs); err != nil {
				continue
			}
			s.events[id] = evs
		case len(name) > len(".peercursors.json") && name[len(name)-len(".peercursors.json"):] == ".peercursors.json":
			groupID := name[:len(name)-len(".peercursors.json")]
			data, err := os.ReadFile(filepath.Join(mapsDir, name))
			if err != nil {
				continue
			}
			var cursors map[string]map[string]int64
			if err := json.Unmarshal(data, &cursors); err != nil {
				// Old flat map[string]int64 format won't unmarshal here; skip it —
				// worst case is a one-time full resync per store.
				continue
			}
			s.peerCursors[groupID] = cursors
		}
	}
	return s, nil
}

// ListSummaries returns one summary per known RealmMap restricted to
// cfgGroups (unconfigured groups' maps stay on disk but are hidden), sorted
// by GroupName then StoreName. A fully-tombstoned map still appears with
// EntryCount 0; only Store.DeleteMap removes it from this list.
func (s *Store) ListSummaries(cfgGroups []model.Group) []model.RealmMapSummary {
	s.mu.Lock()
	defer s.mu.Unlock()

	groupNames := make(map[string]string, len(cfgGroups))
	for _, g := range cfgGroups {
		groupNames[g.KeyPair.ID] = g.Name
	}

	result := make([]model.RealmMapSummary, 0, len(s.states))
	for _, rm := range s.states {
		groupName, ok := groupNames[rm.GroupID]
		if !ok {
			continue
		}
		count := 0
		var maxUpdated int64
		for _, e := range rm.Entries {
			if e.Deleted {
				continue
			}
			count++
			if e.UpdatedAtUnixMillis > maxUpdated {
				maxUpdated = e.UpdatedAtUnixMillis
			}
		}
		result = append(result, model.RealmMapSummary{
			GroupID:             rm.GroupID,
			GroupName:           groupName,
			StoreName:           rm.StoreName,
			EntryCount:          count,
			UpdatedAtUnixMillis: maxUpdated,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].GroupName != result[j].GroupName {
			return result[i].GroupName < result[j].GroupName
		}
		return result[i].StoreName < result[j].StoreName
	})
	return result
}

// GetMap returns the current (non-tombstoned) entries of groupID/storeName,
// or a zero-entry RealmMap if it doesn't exist locally yet.
func (s *Store) GetMap(groupID, storeName string) model.RealmMap {
	s.mu.Lock()
	defer s.mu.Unlock()

	rm, ok := s.states[mapID(groupID, storeName)]
	result := model.RealmMap{GroupID: groupID, StoreName: storeName, Entries: map[string]model.MapEntry{}}
	if !ok {
		return result
	}
	for k, e := range rm.Entries {
		if !e.Deleted {
			result.Entries[k] = e
		}
	}
	return result
}

// CreateMap ensures an (empty, if new) RealmMap exists locally for
// groupID/storeName. Local-only convenience for the UI: an empty map has
// no events to sync, so it isn't visible to other members until its first
// key is set (see model.RealmMap doc).
func (s *Store) CreateMap(groupID, storeName string) error {
	id := mapID(groupID, storeName)

	s.mu.Lock()
	if _, ok := s.states[id]; ok {
		s.mu.Unlock()
		return nil
	}
	s.states[id] = model.RealmMap{GroupID: groupID, StoreName: storeName, Entries: map[string]model.MapEntry{}}
	s.events[id] = nil
	s.mu.Unlock()

	return s.persist(id)
}

// DeleteMap actually removes groupID/storeName: its in-memory state and
// event log, and both on-disk files. Unlike tombstoning individual entries
// (DeleteValue/ApplyEvent), this makes the map disappear from ListSummaries
// entirely rather than lingering with EntryCount 0.
func (s *Store) DeleteMap(groupID, storeName string) error {
	id := mapID(groupID, storeName)

	s.mu.Lock()
	delete(s.states, id)
	delete(s.events, id)
	s.mu.Unlock()

	var firstErr error
	for _, suffix := range []string{".state.json", ".events.json"} {
		if err := os.Remove(filepath.Join(s.dir, id+suffix)); err != nil && !errors.Is(err, os.ErrNotExist) {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// Subscribe registers fn to be called synchronously, after each ApplyEvent
// that produces an observable change, with the corresponding ChangeEvent.
// Call the returned unsubscribe func to stop receiving them.
func (s *Store) Subscribe(fn func(model.ChangeEvent)) (unsubscribe func()) {
	s.mu.Lock()
	id := s.nextListenerID
	s.nextListenerID++
	s.listeners = append(s.listeners, listenerEntry{id: id, fn: fn})
	s.mu.Unlock()

	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, l := range s.listeners {
			if l.id == id {
				s.listeners = append(s.listeners[:i], s.listeners[i+1:]...)
				break
			}
		}
	}
}

// ApplyEvent merges one mutation (last-write-wins by UpdatedAtUnixMillis)
// into groupID/storeName's key, creating the map locally if it didn't
// already exist. Returns whether it actually changed anything, so callers
// (the feature's push/subscribe handlers) can skip re-broadcasting
// stale/duplicate events.
func (s *Store) ApplyEvent(groupID, storeName, key string, entry model.MapEntry) (bool, error) {
	id := mapID(groupID, storeName)

	s.mu.Lock()
	rm, ok := s.states[id]
	if !ok {
		rm = model.RealmMap{GroupID: groupID, StoreName: storeName, Entries: map[string]model.MapEntry{}}
	}
	// Reject only strictly-older events: same-millisecond mutations must
	// still apply in call order rather than being dropped as "duplicates."
	existing, hadKey := rm.Entries[key]
	if hadKey && existing.UpdatedAtUnixMillis > entry.UpdatedAtUnixMillis {
		s.mu.Unlock()
		return false, nil
	}
	rm.Entries[key] = entry
	s.states[id] = rm

	evs := s.events[id]
	replaced := false
	for i := range evs {
		if evs[i].Key == key {
			evs[i] = model.MapEvent{GroupID: groupID, StoreName: storeName, Key: key, Value: entry.Value, Deleted: entry.Deleted, UpdatedAtUnixMillis: entry.UpdatedAtUnixMillis, OriginPeerID: entry.OriginPeerID, Nonce: entry.Nonce, IdentitySignature: entry.IdentitySignature}
			replaced = true
			break
		}
	}
	if !replaced {
		evs = append(evs, model.MapEvent{GroupID: groupID, StoreName: storeName, Key: key, Value: entry.Value, Deleted: entry.Deleted, UpdatedAtUnixMillis: entry.UpdatedAtUnixMillis, OriginPeerID: entry.OriginPeerID, Nonce: entry.Nonce, IdentitySignature: entry.IdentitySignature})
	}
	s.events[id] = evs

	// wasLive/isLive: a delete against an already-tombstoned or nonexistent
	// key changes nothing visible, so no event fires for it.
	wasLive := hadKey && !existing.Deleted
	isLive := !entry.Deleted
	var change *model.ChangeEvent
	if wasLive || isLive {
		ce := model.ChangeEvent{GroupID: groupID, StoreName: storeName, Key: key}
		switch {
		case wasLive && !isLive:
			ce.Type = model.EntryDeleted
			ce.Old = &existing
		case !wasLive && isLive:
			ce.Type = model.EntryAdded
			ce.New = &entry
		default: // wasLive && isLive
			ce.Type = model.EntryUpdated
			ce.Old = &existing
			ce.New = &entry
		}
		change = &ce
	}

	listenersSnapshot := make([]func(model.ChangeEvent), len(s.listeners))
	for i, l := range s.listeners {
		listenersSnapshot[i] = l.fn
	}
	s.mu.Unlock()

	if err := s.persist(id); err != nil {
		return true, err
	}

	if change != nil {
		for _, fn := range listenersSnapshot {
			fn(*change)
		}
	}

	return true, nil
}

// EventsSinceForStore returns every event for groupID/storeName with
// UpdatedAtUnixMillis > sinceUnix, unsigned — the feature layer signs each
// one before sending, since only it holds the group's private key.
func (s *Store) EventsSinceForStore(groupID, storeName string, sinceUnix int64) []model.MapEvent {
	id := mapID(groupID, storeName)

	s.mu.Lock()
	defer s.mu.Unlock()

	var result []model.MapEvent
	for _, e := range s.events[id] {
		if e.UpdatedAtUnixMillis > sinceUnix {
			result = append(result, e)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UpdatedAtUnixMillis < result[j].UpdatedAtUnixMillis })
	return result
}

// LastFromPeerForStore returns the newest UpdatedAtUnixMillis we've ever
// received from peerID for groupID/storeName, or 0 if we've never pulled
// anything from them for that store yet. Used as the per-peer "since"
// cursor in Feature.subscribeToPeer.
func (s *Store) LastFromPeerForStore(groupID, storeName, peerID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peerCursors[groupID][storeName][peerID]
}

// RecordFromPeerForStore advances groupID/storeName's cursor for peerID to
// ts, if ts is newer than what's already recorded, and persists it.
func (s *Store) RecordFromPeerForStore(groupID, storeName, peerID string, ts int64) error {
	s.mu.Lock()
	byStore := s.peerCursors[groupID]
	if byStore == nil {
		byStore = map[string]map[string]int64{}
		s.peerCursors[groupID] = byStore
	}
	cursors := byStore[storeName]
	if cursors == nil {
		cursors = map[string]int64{}
		byStore[storeName] = cursors
	}
	if ts <= cursors[peerID] {
		s.mu.Unlock()
		return nil
	}
	cursors[peerID] = ts

	snapshot := make(map[string]map[string]int64, len(byStore))
	for st, m := range byStore {
		inner := make(map[string]int64, len(m))
		for k, v := range m {
			inner[k] = v
		}
		snapshot[st] = inner
	}
	s.mu.Unlock()

	return s.writeJSON(groupID+".peercursors.json", snapshot)
}

// persist rewrites both files for id from the in-memory cache. It snapshots
// rm.Entries and evs before unlocking: both are reference types, so
// writeJSON's later reflection-based marshal would otherwise race with
// concurrent ApplyEvent calls mutating the same underlying map/slice.
func (s *Store) persist(id string) error {
	s.mu.Lock()
	rm := s.states[id]
	entries := make(map[string]model.MapEntry, len(rm.Entries))
	for k, e := range rm.Entries {
		entries[k] = e
	}
	rm.Entries = entries
	evs := make([]model.MapEvent, len(s.events[id]))
	copy(evs, s.events[id])
	s.mu.Unlock()

	if err := s.writeJSON(id+".state.json", rm); err != nil {
		return err
	}
	return s.writeJSON(id+".events.json", evs)
}

func (s *Store) writeJSON(name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.dir, name), data, 0o644); err != nil {
		log.Printf("realm maps: failed to write %s: %v", name, err)
		return err
	}
	return nil
}
