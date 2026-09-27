package grouptroubleshooting

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	realm "foilen-realm"
	realmmaps "foilen-realm/features/maps"
	realmmodel "foilen-realm/model"
	realmpeers "foilen-realm/peers"
)

const SessionDuration = 10 * time.Minute

const updateInterval = 15 * time.Second

type Manager struct {
	mapsFeature *realmmaps.Feature
	engine      *realm.Engine
	peers       *realmpeers.Store
	localPeerID func() string
	localGroups func() []realmmodel.Group

	mu          sync.Mutex
	lastWritten map[string]string
}

func NewManager(mapsFeature *realmmaps.Feature, engine *realm.Engine, peers *realmpeers.Store, localPeerID func() string, localGroups func() []realmmodel.Group) *Manager {
	return &Manager{
		mapsFeature: mapsFeature,
		engine:      engine,
		peers:       peers,
		localPeerID: localPeerID,
		localGroups: localGroups,
		lastWritten: map[string]string{},
	}
}

func (m *Manager) Start() {
	go func() {
		m.pollOnce()
		ticker := time.NewTicker(updateInterval)
		defer ticker.Stop()
		for range ticker.C {
			m.pollOnce()
		}
	}()
}

func (m *Manager) pollOnce() {
	for _, g := range m.localGroups() {
		m.processGroup(g.KeyPair.ID, g.Name)
	}
}

func (m *Manager) processGroup(groupID, groupName string) {
	rm, encrypted, available := m.mapsFeature.GetMap(groupID, CommonStoreName)
	if encrypted && !available {
		return
	}
	entry, ok := rm.Entries[expirationKey]
	if !ok {
		return
	}
	var exp Expiration
	if err := json.Unmarshal([]byte(entry.Value), &exp); err != nil {
		return
	}
	if time.Now().UnixMilli() >= exp.ExpiresAtUnixMillis {
		return
	}

	localID := m.localPeerID()
	if localID == "" {
		return
	}

	m.reportStarted(groupID, groupName, localID, rm)

	conns := []Connection{}
	for _, p := range m.peers.List() {
		if p.ID == localID || !hasGroupName(p.GroupNames, groupName) {
			continue
		}
		for _, addr := range m.engine.ConnectedAddresses(p.ID) {
			conns = append(conns, Connection{RemotePeerID: p.ID, Address: addr})
		}
	}
	data, err := json.Marshal(conns)
	if err != nil {
		return
	}

	m.mu.Lock()
	unchanged := m.lastWritten[groupID] == string(data)
	if !unchanged {
		m.lastWritten[groupID] = string(data)
	}
	m.mu.Unlock()
	if unchanged {
		return
	}

	if err := m.mapsFeature.SetValue(groupID, CommonStoreName, connectionsKey(localID), string(data)); err != nil {
		log.Printf("group troubleshooting: failed to update connections for group %s %s: %v", groupName, realmmodel.ShortID(groupID), err)
	}
}

func (m *Manager) reportStarted(groupID, groupName, localID string, rm realmmodel.RealmMap) {
	startEntry, ok := rm.Entries[startKey]
	if !ok {
		return
	}
	var start Start
	if err := json.Unmarshal([]byte(startEntry.Value), &start); err != nil {
		return
	}

	if startedEntry, ok := rm.Entries[startedKey(localID)]; ok {
		var started Started
		if json.Unmarshal([]byte(startedEntry.Value), &started) == nil && started.StartAtUnixMillis >= start.StartAtUnixMillis {
			return
		}
	}

	started := Started{StartAtUnixMillis: start.StartAtUnixMillis, StartedAtUnixMillis: time.Now().UnixMilli()}
	data, err := json.Marshal(started)
	if err != nil {
		return
	}
	if err := m.mapsFeature.SetValue(groupID, CommonStoreName, startedKey(localID), string(data)); err != nil {
		log.Printf("group troubleshooting: failed to update started entry for group %s %s: %v", groupName, realmmodel.ShortID(groupID), err)
	}
}

func hasGroupName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func (m *Manager) StartSession(groupID string) error {
	if groupID == "" {
		return fmt.Errorf("please select a group")
	}
	rm, encrypted, available := m.mapsFeature.GetMap(groupID, CommonStoreName)
	if encrypted && !available {
		return fmt.Errorf("group troubleshooting: %s isn't currently decryptable", CommonStoreName)
	}
	if entry, ok := rm.Entries[expirationKey]; ok {
		var exp Expiration
		if json.Unmarshal([]byte(entry.Value), &exp) == nil && time.Now().UnixMilli() < exp.ExpiresAtUnixMillis {
			return fmt.Errorf("group troubleshooting already running")
		}
	}

	for key := range rm.Entries {
		if strings.HasPrefix(key, keyPrefix) {
			if err := m.mapsFeature.DeleteValue(groupID, CommonStoreName, key); err != nil {
				log.Printf("group troubleshooting: failed to clear previous entry %q for group %s: %v", key, realmmodel.GroupLabel(m.localGroups(), groupID), err)
			}
		}
	}

	m.mu.Lock()
	delete(m.lastWritten, groupID)
	m.mu.Unlock()

	now := time.Now()
	start := Start{StartAtUnixMillis: now.UnixMilli()}
	startData, err := json.Marshal(start)
	if err != nil {
		return err
	}
	if err := m.mapsFeature.SetValue(groupID, CommonStoreName, startKey, string(startData)); err != nil {
		return err
	}

	exp := Expiration{ExpiresAtUnixMillis: now.Add(SessionDuration).UnixMilli()}
	expData, err := json.Marshal(exp)
	if err != nil {
		return err
	}
	return m.mapsFeature.SetValue(groupID, CommonStoreName, expirationKey, string(expData))
}
