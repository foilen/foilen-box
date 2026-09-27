package maps

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"maps"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	ds "github.com/ipfs/go-datastore"
	dsq "github.com/ipfs/go-datastore/query"
	leveldb "github.com/ipfs/go-ds-leveldb"

	"foilen-realm/model"
)

const dbDirName = "realm-maps-db"

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func mapID(groupID, storeName string) string {
	return groupID + "__" + unsafeChars.ReplaceAllString(storeName, "_")
}

type listenerEntry struct {
	id int
	fn func(model.ChangeEvent)
}

type mapInfo struct {
	GroupID   string `json:"groupId"`
	StoreName string `json:"storeName"`
}

type Store struct {
	db *leveldb.Datastore

	mu          sync.Mutex
	mapMeta     map[string]mapInfo
	peerCursors map[string]map[string]map[string]int64

	listeners      []listenerEntry
	nextListenerID int
}

func NewStore(dir string) (*Store, error) {
	db, err := leveldb.NewDatastore(filepath.Join(dir, dbDirName), nil)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, mapMeta: map[string]mapInfo{}, peerCursors: map[string]map[string]map[string]int64{}}

	ctx := context.Background()

	idxEntries, err := queryRest(ctx, db, dsq.Query{Prefix: "/mapindex", KeysOnly: true})
	if err != nil {
		db.Close()
		return nil, err
	}
	for _, e := range idxEntries {
		id := strings.TrimPrefix(e.Key, "/mapindex/")
		data, err := db.Get(ctx, ds.NewKey("/"+id+"/mapmeta"))
		if err != nil {
			continue
		}
		var info mapInfo
		if err := json.Unmarshal(data, &info); err != nil {
			continue
		}
		s.mapMeta[id] = info
	}

	cursorEntries, err := queryRest(ctx, db, dsq.Query{Prefix: "/peercursor"})
	if err != nil {
		db.Close()
		return nil, err
	}
	for _, e := range cursorEntries {
		groupID := strings.TrimPrefix(e.Key, "/peercursor/")
		var cursors map[string]map[string]int64
		if err := json.Unmarshal(e.Value, &cursors); err != nil {
			continue
		}
		s.peerCursors[groupID] = cursors
	}

	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func queryRest(ctx context.Context, db *leveldb.Datastore, q dsq.Query) ([]dsq.Entry, error) {
	results, err := db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	return results.Rest()
}

func (s *Store) stateEntries(ctx context.Context, id string) map[string]model.MapEntry {
	prefix := "/" + id + "/state"
	entries, err := queryRest(ctx, s.db, dsq.Query{Prefix: prefix})
	if err != nil {
		log.Printf("realm maps: query %s failed: %v", prefix, err)
		return nil
	}
	out := make(map[string]model.MapEntry, len(entries))
	for _, e := range entries {
		key := strings.TrimPrefix(e.Key, prefix+"/")
		var me model.MapEntry
		if err := json.Unmarshal(e.Value, &me); err != nil {
			continue
		}
		out[key] = me
	}
	return out
}

// Maps

func (s *Store) ListSummaries(cfgGroups []model.Group) []model.RealmMapSummary {
	s.mu.Lock()
	metaSnapshot := make(map[string]mapInfo, len(s.mapMeta))
	maps.Copy(metaSnapshot, s.mapMeta)
	s.mu.Unlock()

	groupNames := make(map[string]string, len(cfgGroups))
	for _, g := range cfgGroups {
		groupNames[g.KeyPair.ID] = g.Name
	}

	ctx := context.Background()
	result := make([]model.RealmMapSummary, 0, len(metaSnapshot))
	for id, info := range metaSnapshot {
		groupName, ok := groupNames[info.GroupID]
		if !ok {
			continue
		}
		count := 0
		var maxUpdated int64
		for _, e := range s.stateEntries(ctx, id) {
			if e.Deleted {
				continue
			}
			count++
			if e.UpdatedAtUnixMillis > maxUpdated {
				maxUpdated = e.UpdatedAtUnixMillis
			}
		}
		result = append(result, model.RealmMapSummary{
			GroupID:             info.GroupID,
			GroupName:           groupName,
			StoreName:           info.StoreName,
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
	id := mapID(groupID, storeName)
	result := model.RealmMap{GroupID: groupID, StoreName: storeName, Entries: map[string]model.MapEntry{}}
	for k, e := range s.stateEntries(context.Background(), id) {
		if !e.Deleted {
			result.Entries[k] = e
		}
	}
	return result
}

func (s *Store) ensureMapMetaLocked(ctx context.Context, id, groupID, storeName string) error {
	if _, ok := s.mapMeta[id]; ok {
		return nil
	}
	info := mapInfo{GroupID: groupID, StoreName: storeName}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	if err := s.db.Put(ctx, ds.NewKey("/"+id+"/mapmeta"), data); err != nil {
		return err
	}
	if err := s.db.Put(ctx, ds.NewKey("/mapindex/"+id), []byte{}); err != nil {
		return err
	}
	s.mapMeta[id] = info
	return nil
}

func (s *Store) CreateMap(groupID, storeName string) error {
	id := mapID(groupID, storeName)

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureMapMetaLocked(context.Background(), id, groupID, storeName)
}

func (s *Store) DeleteMap(groupID, storeName string) error {
	id := mapID(groupID, storeName)
	ctx := context.Background()

	s.mu.Lock()
	delete(s.mapMeta, id)
	s.mu.Unlock()

	if err := s.db.Delete(ctx, ds.NewKey("/mapindex/"+id)); err != nil {
		return err
	}

	entries, err := queryRest(ctx, s.db, dsq.Query{Prefix: "/" + id, KeysOnly: true})
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	batch, err := s.db.Batch(ctx)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := batch.Delete(ctx, ds.NewKey(e.Key)); err != nil {
			return err
		}
	}
	return batch.Commit(ctx)
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
	ctx := context.Background()
	stateKey := ds.NewKey("/" + id + "/state/" + key)
	eventKey := ds.NewKey("/" + id + "/event/" + key)

	s.mu.Lock()

	if err := s.ensureMapMetaLocked(ctx, id, groupID, storeName); err != nil {
		s.mu.Unlock()
		return false, err
	}

	var existing model.MapEntry
	hadKey := false
	switch data, err := s.db.Get(ctx, stateKey); {
	case err == nil:
		if err := json.Unmarshal(data, &existing); err != nil {
			s.mu.Unlock()
			return false, err
		}
		hadKey = true
	case errors.Is(err, ds.ErrNotFound):
	default:
		s.mu.Unlock()
		return false, err
	}

	if hadKey && existing.UpdatedAtUnixMillis > entry.UpdatedAtUnixMillis {
		s.mu.Unlock()
		return false, nil
	}

	contentChanged := !hadKey || existing.Value != entry.Value || existing.Deleted != entry.Deleted

	entryData, err := json.Marshal(entry)
	if err != nil {
		s.mu.Unlock()
		return false, err
	}
	ev := model.MapEvent{GroupID: groupID, StoreName: storeName, Key: key, Value: entry.Value, Deleted: entry.Deleted, UpdatedAtUnixMillis: entry.UpdatedAtUnixMillis, OriginPeerID: entry.OriginPeerID, Nonce: entry.Nonce, IdentitySignature: entry.IdentitySignature}
	eventData, err := json.Marshal(ev)
	if err != nil {
		s.mu.Unlock()
		return false, err
	}

	batch, err := s.db.Batch(ctx)
	if err != nil {
		s.mu.Unlock()
		return false, err
	}
	if err := batch.Put(ctx, stateKey, entryData); err != nil {
		s.mu.Unlock()
		return false, err
	}
	if err := batch.Put(ctx, eventKey, eventData); err != nil {
		s.mu.Unlock()
		return false, err
	}
	if err := batch.Commit(ctx); err != nil {
		s.mu.Unlock()
		return false, err
	}

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

	if change != nil {
		for _, fn := range listenersSnapshot {
			fn(*change)
		}
	}

	return contentChanged, nil
}

func (s *Store) EventsSinceForStore(groupID, storeName string, sinceUnix int64) []model.MapEvent {
	id := mapID(groupID, storeName)
	prefix := "/" + id + "/event"

	entries, err := queryRest(context.Background(), s.db, dsq.Query{Prefix: prefix})
	if err != nil {
		log.Printf("realm maps: query %s failed: %v", prefix, err)
		return nil
	}

	var result []model.MapEvent
	for _, e := range entries {
		var ev model.MapEvent
		if err := json.Unmarshal(e.Value, &ev); err != nil {
			continue
		}
		if ev.UpdatedAtUnixMillis > sinceUnix {
			result = append(result, ev)
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
		maps.Copy(inner, m)
		snapshot[st] = inner
	}
	s.mu.Unlock()

	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if err := s.db.Put(context.Background(), ds.NewKey("/peercursor/"+groupID), data); err != nil {
		log.Printf("realm maps: failed to write peer cursors for group %s: %v", groupID, err)
		return err
	}
	return nil
}
