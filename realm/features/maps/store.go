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

func mapID(groupID, storeName string) string {
	return groupID + "__" + unsafeChars.ReplaceAllString(storeName, "_")
}

type listenerEntry struct {
	id int
	fn func(model.ChangeEvent)
}

type Store struct {
	dir string

	mu     sync.Mutex
	states map[string]model.RealmMap
	events map[string][]model.MapEvent

	peerCursors map[string]map[string]map[string]int64

	listeners      []listenerEntry
	nextListenerID int
}

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
				continue
			}
			s.peerCursors[groupID] = cursors
		}
	}
	return s, nil
}

// Maps

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

// Events

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

func (s *Store) ApplyEvent(groupID, storeName, key string, entry model.MapEntry) (bool, error) {
	id := mapID(groupID, storeName)

	s.mu.Lock()
	rm, ok := s.states[id]
	if !ok {
		rm = model.RealmMap{GroupID: groupID, StoreName: storeName, Entries: map[string]model.MapEntry{}}
	}

	existing, hadKey := rm.Entries[key]
	if hadKey && existing.UpdatedAtUnixMillis > entry.UpdatedAtUnixMillis {
		s.mu.Unlock()
		return false, nil
	}

	contentChanged := !hadKey || existing.Value != entry.Value || existing.Deleted != entry.Deleted
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

	wasLive := hadKey && !existing.Deleted
	isLive := !entry.Deleted
	var change *model.ChangeEvent
	if contentChanged && (wasLive || isLive) {
		ce := model.ChangeEvent{GroupID: groupID, StoreName: storeName, Key: key}
		switch {
		case wasLive && !isLive:
			ce.Type = model.EntryDeleted
			ce.Old = &existing
		case !wasLive && isLive:
			ce.Type = model.EntryAdded
			ce.New = &entry
		default:
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
		return contentChanged, err
	}

	if change != nil {
		for _, fn := range listenersSnapshot {
			fn(*change)
		}
	}

	return contentChanged, nil
}

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

// Peer cursors

func (s *Store) LastFromPeerForStore(groupID, storeName, peerID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peerCursors[groupID][storeName][peerID]
}

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

// Persistence

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
