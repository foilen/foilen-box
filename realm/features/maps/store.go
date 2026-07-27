// Package maps is the "common/maps" Realm feature: small shared
// Map<String,String> key-value stores scoped to a group (see model.Group),
// replicated peer-to-peer as a signed, last-write-wins event log.
package maps

import (
	"encoding/json"
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
// <mapID>.state.json and <mapID>.events.json. scopeId is already a
// filesystem-safe libp2p id; storeName is arbitrary user input, so it's
// sanitized.
func mapID(scopeID, storeName string) string {
	return scopeID + "__" + unsafeChars.ReplaceAllString(storeName, "_")
}

// Store caches every known RealmMap's current state and event log in
// memory, backed by one file pair per map on disk
// ($dir/realm-maps/<mapID>.state.json + <mapID>.events.json), following
// the same one-file-per-entity convention as realm/features/spec.Store.
type Store struct {
	dir string

	mu     sync.Mutex
	states map[string]model.RealmMap   // by mapID
	events map[string][]model.MapEvent // by mapID, one entry per key

	// peerCursors tracks, per scopeID and then per remote peer, the newest
	// UpdatedAtUnixMillis we've ever received from that specific peer while
	// pulling (see Feature.pullFrom). It is deliberately not a single
	// scope-wide watermark: if peer B has an old event we never got (e.g. a
	// push to us failed while we were offline), and we've since received
	// something newer from peer C, a shared watermark would sit above B's
	// event forever and we'd never ask B for it again. A per-peer cursor
	// means every peer is asked "since the last thing I got from *you*."
	peerCursors map[string]map[string]int64 // scopeID -> peerID -> unixMillis
}

// New creates the realm-maps directory if needed, loads any existing map
// files into memory, and returns a Store.
func NewStore(dir string) (*Store, error) {
	mapsDir := filepath.Join(dir, subDirName)
	if err := os.MkdirAll(mapsDir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: mapsDir, states: map[string]model.RealmMap{}, events: map[string][]model.MapEvent{}, peerCursors: map[string]map[string]int64{}}

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
			scopeID := name[:len(name)-len(".peercursors.json")]
			data, err := os.ReadFile(filepath.Join(mapsDir, name))
			if err != nil {
				continue
			}
			var cursors map[string]int64
			if err := json.Unmarshal(data, &cursors); err != nil {
				continue
			}
			s.peerCursors[scopeID] = cursors
		}
	}
	return s, nil
}

// ListSummaries returns one summary per known RealmMap that has at least
// one (non-tombstoned) entry, restricted to scopes present in cfgGroups
// (groups we're no longer configured with are hidden, though their files
// are left on disk), sorted by GroupName then StoreName.
func (s *Store) ListSummaries(cfgGroups []model.Group) []model.RealmMapSummary {
	s.mu.Lock()
	defer s.mu.Unlock()

	groupNames := make(map[string]string, len(cfgGroups))
	for _, g := range cfgGroups {
		groupNames[g.KeyPair.ID] = g.Name
	}

	result := make([]model.RealmMapSummary, 0, len(s.states))
	for _, rm := range s.states {
		groupName, ok := groupNames[rm.ScopeID]
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
			ScopeID:             rm.ScopeID,
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

// GetMap returns the current (non-tombstoned) entries of scopeId/storeName,
// or a zero-entry RealmMap if it doesn't exist locally yet.
func (s *Store) GetMap(scopeID, storeName string) model.RealmMap {
	s.mu.Lock()
	defer s.mu.Unlock()

	rm, ok := s.states[mapID(scopeID, storeName)]
	result := model.RealmMap{ScopeID: scopeID, StoreName: storeName, Entries: map[string]model.MapEntry{}}
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
// scopeId/storeName. Local-only convenience for the UI: an empty map has
// no events to sync, so it isn't visible to other members until its first
// key is set (see model.RealmMap doc).
func (s *Store) CreateMap(scopeID, storeName string) error {
	id := mapID(scopeID, storeName)

	s.mu.Lock()
	if _, ok := s.states[id]; ok {
		s.mu.Unlock()
		return nil
	}
	s.states[id] = model.RealmMap{ScopeID: scopeID, StoreName: storeName, Entries: map[string]model.MapEntry{}}
	s.events[id] = nil
	s.mu.Unlock()

	return s.persist(id)
}

// ApplyEvent merges one mutation (last-write-wins by UpdatedAtUnixMillis) into
// scopeId/storeName's key, creating the map locally if it didn't already
// exist. Returns whether it actually changed anything, so callers (the
// feature's push/sync handlers) can skip re-broadcasting stale/duplicate
// events.
func (s *Store) ApplyEvent(scopeID, storeName, key string, entry model.MapEntry) (bool, error) {
	id := mapID(scopeID, storeName)

	s.mu.Lock()
	rm, ok := s.states[id]
	if !ok {
		rm = model.RealmMap{ScopeID: scopeID, StoreName: storeName, Entries: map[string]model.MapEntry{}}
	}
	// Reject only strictly-older events: two same-millisecond mutations
	// (e.g. a local "set" immediately followed by a "delete") must still
	// apply in call order rather than the second one being silently
	// dropped as a "duplicate."
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
			evs[i] = model.MapEvent{ScopeID: scopeID, StoreName: storeName, Key: key, Value: entry.Value, Deleted: entry.Deleted, UpdatedAtUnixMillis: entry.UpdatedAtUnixMillis, OriginPeerID: entry.OriginPeerID}
			replaced = true
			break
		}
	}
	if !replaced {
		evs = append(evs, model.MapEvent{ScopeID: scopeID, StoreName: storeName, Key: key, Value: entry.Value, Deleted: entry.Deleted, UpdatedAtUnixMillis: entry.UpdatedAtUnixMillis, OriginPeerID: entry.OriginPeerID})
	}
	s.events[id] = evs
	s.mu.Unlock()

	if err := s.persist(id); err != nil {
		return true, err
	}
	return true, nil
}

// MaxUpdatedAt returns the max UpdatedAtUnixMillis across every event under any
// storeName for scopeId, or 0 if we have nothing for that scope yet. Used
// as the "since" cursor when pulling from a peer: "give me whatever
// changed after the newest thing I already know about."
func (s *Store) MaxUpdatedAt(scopeID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	var max int64
	for _, evs := range s.events {
		for _, e := range evs {
			if e.ScopeID == scopeID && e.UpdatedAtUnixMillis > max {
				max = e.UpdatedAtUnixMillis
			}
		}
	}
	return max
}

// EventsSince returns every event (any storeName) under scopeId with
// UpdatedAtUnixMillis > sinceUnix, unsigned — the feature layer signs each one
// before sending, since only it holds the scope group's private key.
func (s *Store) EventsSince(scopeID string, sinceUnix int64) []model.MapEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	var result []model.MapEvent
	for _, evs := range s.events {
		for _, e := range evs {
			if e.ScopeID == scopeID && e.UpdatedAtUnixMillis > sinceUnix {
				result = append(result, e)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UpdatedAtUnixMillis < result[j].UpdatedAtUnixMillis })
	return result
}

// LastFromPeer returns the newest UpdatedAtUnixMillis we've ever received
// from peerID while syncing scopeID, or 0 if we've never pulled anything
// from them yet. Used as the per-peer "since" cursor in Feature.pullFrom.
func (s *Store) LastFromPeer(scopeID, peerID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peerCursors[scopeID][peerID]
}

// RecordFromPeer advances scopeID's cursor for peerID to ts, if ts is newer
// than what's already recorded, and persists it.
func (s *Store) RecordFromPeer(scopeID, peerID string, ts int64) error {
	s.mu.Lock()
	cursors := s.peerCursors[scopeID]
	if cursors == nil {
		cursors = map[string]int64{}
		s.peerCursors[scopeID] = cursors
	}
	if ts <= cursors[peerID] {
		s.mu.Unlock()
		return nil
	}
	cursors[peerID] = ts
	snapshot := make(map[string]int64, len(cursors))
	for k, v := range cursors {
		snapshot[k] = v
	}
	s.mu.Unlock()

	return s.writeJSON(scopeID+".peercursors.json", snapshot)
}

// persist rewrites both files for id from the in-memory cache.
func (s *Store) persist(id string) error {
	s.mu.Lock()
	rm := s.states[id]
	evs := s.events[id]
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
