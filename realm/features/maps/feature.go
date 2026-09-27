package maps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	realm "foilen-realm"
	"foilen-realm/keypair"
	"foilen-realm/model"
)

const (
	PushProtocolID = protocol.ID("/foilen-box/maps-push/1.0.0")

	SubscribeProtocolID = protocol.ID("/foilen-box/maps-subscribe/1.0.0")

	UnsubscribeProtocolID = protocol.ID("/foilen-box/maps-unsubscribe/1.0.0")

	SystemConfigStoreName = "_realmMaps"

	ioTimeout = 10 * time.Second
	maxBytes  = 256 * 1024

	FeatureName = "common/maps"
)

type storeCursor struct {
	StoreName string `json:"storeName"`
	SinceUnix int64  `json:"sinceUnix"`
}

type subscribeRequest struct {
	GroupID string        `json:"groupId"`
	Stores  []storeCursor `json:"stores"`
}

type subscribeResponse struct {
	Events []model.MapEventEnvelope `json:"events"`
}

type unsubscribeRequest struct {
	GroupID    string   `json:"groupId"`
	StoreNames []string `json:"storeNames"`
}

type groupSubs struct {
	initializedPeers map[string]bool
	desiredStores    map[string]bool
	subscribedPeers  map[string]map[string]bool
}

type Feature struct {
	store *Store

	mu  sync.Mutex
	reg *realm.Registrar

	incomingSubs map[string]map[string]map[string]bool

	groupStates map[string]*groupSubs

	changeListenerInstalled bool

	sweepMinute   int
	lastSweptHour time.Time
}

// Feature

func New(store *Store) *Feature {
	return &Feature{
		store:        store,
		incomingSubs: map[string]map[string]map[string]bool{},
		groupStates:  map[string]*groupSubs{},
		sweepMinute:  rand.Intn(60),
	}
}

func (f *Feature) registrar() *realm.Registrar {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reg
}

func (f *Feature) Name() string { return FeatureName }

func (f *Feature) Actions() []model.PermissionAction { return nil }

func (f *Feature) RegisterHandlers(reg *realm.Registrar) {
	f.mu.Lock()
	f.reg = reg
	alreadyInstalled := f.changeListenerInstalled
	f.changeListenerInstalled = true
	f.mu.Unlock()

	reg.SetStreamHandler(PushProtocolID, f.handlePushStream(reg))
	reg.SetStreamHandler(SubscribeProtocolID, f.handleSubscribeStream(reg))
	reg.SetStreamHandler(UnsubscribeProtocolID, f.handleUnsubscribeStream(reg))

	if !alreadyInstalled {
		f.store.Subscribe(f.onStoreChange)
	}
}

// Outgoing subscriptions

func (f *Feature) onStoreChange(ev model.ChangeEvent) {
	if ev.StoreName != SystemConfigStoreName {
		return
	}
	reg := f.registrar()
	if reg == nil {
		return
	}
	f.reconcileDesiredStores(reg, ev.GroupID)
}

func (f *Feature) OnPeerConnected(reg *realm.Registrar, id peer.ID) {
	info, ok := reg.Peers().Get(id.String())
	if !ok {
		return
	}
	cfg := reg.Config()
	for _, groupName := range info.GroupNames {
		if group, ok := model.FindGroupByName(cfg.Groups, groupName); ok {
			go f.onPeerAvailable(reg, id, group)
		}
	}
}

func (f *Feature) OnGroupConfirmed(reg *realm.Registrar, id peer.ID, group model.Group) {
	f.onPeerAvailable(reg, id, group)
}

func (f *Feature) onPeerAvailable(reg *realm.Registrar, id peer.ID, group model.Group) {
	groupID := group.KeyPair.ID
	peerID := id.String()
	gs := f.groupSubsFor(groupID)

	f.mu.Lock()
	if gs.initializedPeers[peerID] {
		f.mu.Unlock()
		return
	}
	gs.initializedPeers[peerID] = true
	f.mu.Unlock()

	initial := f.claimStoresToSubscribe(gs, peerID, []string{"common", SystemConfigStoreName})
	if len(initial) > 0 {
		f.subscribeToPeer(reg, id, group, initial)
	}

	f.reconcileDesiredStores(reg, groupID)
}

func (f *Feature) reconcileDesiredStores(reg *realm.Registrar, groupID string) {
	group, ok := model.FindGroupByID(reg.Config().Groups, groupID)
	if !ok {
		return
	}

	cfgMap := f.store.GetMap(groupID, SystemConfigStoreName)
	desired := make(map[string]bool, len(cfgMap.Entries))
	for name := range cfgMap.Entries {
		desired[name] = true
	}

	gs := f.groupSubsFor(groupID)

	f.mu.Lock()
	var removed []string
	for name := range gs.desiredStores {
		if !desired[name] {
			removed = append(removed, name)
		}
	}
	gs.desiredStores = desired
	allDesired := make([]string, 0, len(desired))
	for name := range desired {
		allDesired = append(allDesired, name)
	}
	initializedPeers := make([]string, 0, len(gs.initializedPeers))
	for pid := range gs.initializedPeers {
		initializedPeers = append(initializedPeers, pid)
	}
	f.mu.Unlock()

	if len(allDesired) > 0 {
		for _, pidStr := range initializedPeers {
			info, ok := reg.Peers().Get(pidStr)
			if !ok || !info.Connected {
				continue
			}
			pid, err := peer.Decode(pidStr)
			if err != nil {
				continue
			}
			toAsk := f.claimStoresToSubscribe(gs, pidStr, allDesired)
			if len(toAsk) > 0 {
				go f.subscribeToPeer(reg, pid, group, toAsk)
			}
		}
	}

	if len(removed) > 0 {
		for _, pidStr := range initializedPeers {
			info, ok := reg.Peers().Get(pidStr)
			if !ok || !info.Connected {
				continue
			}
			pid, err := peer.Decode(pidStr)
			if err != nil {
				continue
			}
			f.sendUnsubscribe(reg, pid, groupID, removed)
		}

		f.mu.Lock()
		for _, storeName := range removed {
			delete(gs.subscribedPeers, storeName)
		}
		f.mu.Unlock()

		for _, storeName := range removed {
			if err := f.store.DeleteMap(groupID, storeName); err != nil {
				log.Printf("realm maps: failed to purge removed store %q for group %s: %v", storeName, group.Label(), err)
			}
		}
	}
}

func (f *Feature) groupSubsFor(groupID string) *groupSubs {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.groupStatesLocked(groupID)
}

func (f *Feature) groupStatesLocked(groupID string) *groupSubs {
	gs, ok := f.groupStates[groupID]
	if !ok {
		gs = &groupSubs{
			initializedPeers: map[string]bool{},
			desiredStores:    map[string]bool{},
			subscribedPeers:  map[string]map[string]bool{},
		}
		f.groupStates[groupID] = gs
	}
	return gs
}

func (f *Feature) claimStoresToSubscribe(gs *groupSubs, peerID string, storeNames []string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var toAsk []string
	for _, name := range storeNames {
		peers := gs.subscribedPeers[name]
		if peers == nil {
			peers = map[string]bool{}
			gs.subscribedPeers[name] = peers
		}
		if peers[peerID] {
			continue
		}
		peers[peerID] = true
		toAsk = append(toAsk, name)
	}
	return toAsk
}

// Maps API

func (f *Feature) ListSummaries() []model.RealmMapSummary {
	reg := f.registrar()
	if reg == nil {
		return nil
	}
	summaries := f.store.ListSummaries(reg.Config().Groups)

	configByGroup := map[string]model.RealmMap{}
	for i := range summaries {
		groupID := summaries[i].GroupID
		cfgMap, ok := configByGroup[groupID]
		if !ok {
			cfgMap = f.store.GetMap(groupID, SystemConfigStoreName)
			configByGroup[groupID] = cfgMap
		}
		entry, ok := cfgMap.Entries[summaries[i].StoreName]
		if !ok {
			continue
		}
		var cfg model.RealmMapConfig
		if err := json.Unmarshal([]byte(entry.Value), &cfg); err != nil {
			continue
		}
		summaries[i].AutoDeleteEntriesHours = cfg.AutoDeleteEntriesHours
		if cfg.Encryption != nil {
			summaries[i].EncryptionIdentityID = cfg.Encryption.IdentityID
		}
	}
	return summaries
}

func (f *Feature) EncryptionIdentityID(groupID, storeName string) string {
	cfg := f.configForStore(groupID, storeName)
	if cfg.Encryption == nil {
		return ""
	}
	return cfg.Encryption.IdentityID
}

func (f *Feature) configForStore(groupID, storeName string) model.RealmMapConfig {
	cfgMap := f.store.GetMap(groupID, SystemConfigStoreName)
	entry, ok := cfgMap.Entries[storeName]
	if !ok {
		return model.RealmMapConfig{}
	}
	var cfg model.RealmMapConfig
	if err := json.Unmarshal([]byte(entry.Value), &cfg); err != nil {
		return model.RealmMapConfig{}
	}
	return cfg
}

func (f *Feature) GetMap(groupID, storeName string) (rm model.RealmMap, encrypted bool, available bool) {
	raw := f.store.GetMap(groupID, storeName)

	cfg := f.configForStore(groupID, storeName)
	if cfg.Encryption == nil {
		return raw, false, true
	}

	locked := model.RealmMap{GroupID: groupID, StoreName: storeName, Entries: map[string]model.MapEntry{}}

	reg := f.registrar()
	if reg == nil {
		return locked, true, false
	}
	identity, ok := model.FindIdentityByID(reg.Config().Identities, cfg.Encryption.IdentityID)
	if !ok {
		return locked, true, false
	}
	identityPriv, err := keypair.PrivateKey(identity.KeyPair)
	if err != nil {
		log.Printf("realm maps: failed to load identity %q private key: %v", identity.Name, err)
		return locked, true, false
	}
	symmetricKey, err := openSymmetricKey(cfg.Encryption.EncryptedSymmetricKey, identityPriv)
	if err != nil {
		log.Printf("realm maps: failed to unlock symmetric key for %s/%s: %v", model.GroupLabel(reg.Config().Groups, groupID), storeName, err)
		return locked, true, false
	}
	identityPub := identityPriv.GetPublic()

	decrypted := model.RealmMap{GroupID: groupID, StoreName: storeName, Entries: map[string]model.MapEntry{}}
	for storageKey, entry := range raw.Entries {
		ev := model.MapEvent{GroupID: groupID, StoreName: storeName, Key: storageKey, Value: entry.Value, Deleted: entry.Deleted, UpdatedAtUnixMillis: entry.UpdatedAtUnixMillis, OriginPeerID: entry.OriginPeerID, Nonce: entry.Nonce, IdentitySignature: entry.IdentitySignature}
		if !verifyEncryptedEvent(identityPub, ev) {
			log.Printf("realm maps: dropping entry with invalid identity signature in %s/%s", model.GroupLabel(reg.Config().Groups, groupID), storeName)
			continue
		}
		realKey, value, err := decryptEntry(entry.Value, entry.Nonce, symmetricKey)
		if err != nil {
			log.Printf("realm maps: failed to decrypt entry in %s/%s: %v", model.GroupLabel(reg.Config().Groups, groupID), storeName, err)
			continue
		}
		decrypted.Entries[realKey] = model.MapEntry{Value: value, UpdatedAtUnixMillis: entry.UpdatedAtUnixMillis, OriginPeerID: entry.OriginPeerID}
	}
	return decrypted, true, true
}

func (f *Feature) CreateMap(groupID, storeName string, config model.RealmMapConfig, encryptToIdentityID string) error {
	if _, err := f.groupFor(groupID); err != nil {
		return err
	}
	if encryptToIdentityID != "" {
		pub, err := identityPubKeyFromID(encryptToIdentityID)
		if err != nil {
			return err
		}
		encryptedSymmetricKey, _, err := sealSymmetricKey(pub)
		if err != nil {
			return err
		}
		config.Encryption = &model.MapEncryptionConfig{IdentityID: encryptToIdentityID, EncryptedSymmetricKey: encryptedSymmetricKey}
	}
	if err := f.store.CreateMap(groupID, storeName); err != nil {
		return err
	}
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return f.SetValue(groupID, SystemConfigStoreName, storeName, string(data))
}

func (f *Feature) SetValue(groupID, storeName, key, value string) error {
	return f.mutate(groupID, storeName, key, model.MapEntry{Value: value})
}

func (f *Feature) DeleteValue(groupID, storeName, key string) error {
	return f.mutate(groupID, storeName, key, model.MapEntry{Deleted: true})
}

func (f *Feature) DeleteMap(groupID, storeName string) error {
	if err := f.DeleteValue(groupID, SystemConfigStoreName, storeName); err != nil {
		return err
	}
	return f.store.DeleteMap(groupID, storeName)
}

func (f *Feature) mutate(groupID, storeName, key string, entry model.MapEntry) error {
	reg := f.registrar()
	if reg == nil {
		return fmt.Errorf("realm maps: not registered on an engine")
	}
	group, err := f.groupFor(groupID)
	if err != nil {
		return err
	}

	entry.UpdatedAtUnixMillis = time.Now().UnixMilli()
	entry.OriginPeerID = reg.Config().PeerID.ID

	storageKey := key
	if cfg := f.configForStore(groupID, storeName); cfg.Encryption != nil {
		storageKey, entry, err = f.encryptMutation(reg, cfg.Encryption, groupID, storeName, key, entry)
		if err != nil {
			return err
		}
	}

	changed, err := f.store.ApplyEvent(groupID, storeName, storageKey, entry)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}

	ev := model.MapEvent{GroupID: groupID, StoreName: storeName, Key: storageKey, Value: entry.Value, Deleted: entry.Deleted, UpdatedAtUnixMillis: entry.UpdatedAtUnixMillis, OriginPeerID: entry.OriginPeerID, Nonce: entry.Nonce, IdentitySignature: entry.IdentitySignature}
	env, err := signEvent(group, ev)
	if err != nil {
		return err
	}
	log.Printf("realm maps: local write to %s/%s key=%q deleted=%v", group.Label(), storeName, key, entry.Deleted)
	f.broadcast(reg, group, storeName, env)
	return nil
}

func (f *Feature) encryptMutation(reg *realm.Registrar, enc *model.MapEncryptionConfig, groupID, storeName, key string, entry model.MapEntry) (string, model.MapEntry, error) {
	identity, ok := model.FindIdentityByID(reg.Config().Identities, enc.IdentityID)
	if !ok {
		return "", model.MapEntry{}, fmt.Errorf("realm maps: %s/%s is encrypted to identity %q, which is not available locally", groupID, storeName, enc.IdentityID)
	}
	identityPriv, err := keypair.PrivateKey(identity.KeyPair)
	if err != nil {
		return "", model.MapEntry{}, fmt.Errorf("realm maps: failed to load identity %q private key: %w", identity.Name, err)
	}

	storageKey := hashKey(enc.IdentityID, key)

	if entry.Deleted {
		entry.Value = ""
		entry.Nonce = ""
	} else {
		symmetricKey, err := openSymmetricKey(enc.EncryptedSymmetricKey, identityPriv)
		if err != nil {
			return "", model.MapEntry{}, fmt.Errorf("realm maps: failed to unlock symmetric key for %s/%s: %w", groupID, storeName, err)
		}
		ciphertext, nonce, err := encryptEntry(key, entry.Value, symmetricKey)
		if err != nil {
			return "", model.MapEntry{}, err
		}
		entry.Value = ciphertext
		entry.Nonce = nonce
	}

	ev := model.MapEvent{GroupID: groupID, StoreName: storeName, Key: storageKey, Value: entry.Value, Deleted: entry.Deleted, UpdatedAtUnixMillis: entry.UpdatedAtUnixMillis, OriginPeerID: entry.OriginPeerID, Nonce: entry.Nonce}
	sig, err := signEncryptedEvent(identityPriv, ev)
	if err != nil {
		return "", model.MapEntry{}, err
	}
	entry.IdentitySignature = sig

	return storageKey, entry, nil
}

func (f *Feature) groupFor(groupID string) (model.Group, error) {
	reg := f.registrar()
	if reg == nil {
		return model.Group{}, fmt.Errorf("realm maps: not registered on an engine")
	}
	group, ok := model.FindGroupByID(reg.Config().Groups, groupID)
	if !ok {
		return model.Group{}, fmt.Errorf("realm maps: no locally-configured group for %q", groupID)
	}
	return group, nil
}

// Replication

func (f *Feature) broadcast(reg *realm.Registrar, group model.Group, storeName string, env model.MapEventEnvelope) {
	f.broadcastExcept(reg, group, storeName, env, "")
}

func (f *Feature) broadcastExcept(reg *realm.Registrar, group model.Group, storeName string, env model.MapEventEnvelope, exceptPeerID string) {
	h := reg.Host()
	ctx := reg.Context()
	if h == nil || ctx == nil {
		return
	}
	recipients := f.incomingSubscribers(group.KeyPair.ID, storeName)
	if len(recipients) > 0 {
		log.Printf("realm maps: broadcasting %s/%s key=%q to %d subscriber(s)", group.Label(), storeName, env.Key, len(recipients))
	}
	for _, peerID := range recipients {
		if peerID == exceptPeerID {
			continue
		}
		pid, err := peer.Decode(peerID)
		if err != nil {
			continue
		}
		go sendPush(ctx, h, pid, reg.PeerLabel(peerID), env)
	}
}

func sendPush(ctx context.Context, h host.Host, pid peer.ID, label string, env model.MapEventEnvelope) {
	streamCtx, cancel := context.WithTimeout(ctx, ioTimeout)
	defer cancel()
	s, err := h.NewStream(streamCtx, pid, PushProtocolID)
	if err != nil {
		log.Printf("realm maps: peer %s unreachable for push: %v", label, err)
		return
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(ioTimeout))
	if err := json.NewEncoder(s).Encode(env); err != nil {
		log.Printf("realm maps: failed to push to %s: %v", label, err)
	}
}

func (f *Feature) subscribeToPeer(reg *realm.Registrar, id peer.ID, group model.Group, storeNames []string) {
	if len(storeNames) == 0 {
		return
	}
	h := reg.Host()
	ctx := reg.Context()
	if h == nil || ctx == nil {
		return
	}
	if err := reg.EnsureConnected(ctx, id); err != nil {
		return
	}

	streamCtx, cancel := context.WithTimeout(ctx, ioTimeout)
	s, err := h.NewStream(streamCtx, id, SubscribeProtocolID)
	cancel()
	if err != nil {
		log.Printf("realm maps: peer %s unreachable for subscribe: %v", reg.PeerLabel(id.String()), err)
		return
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(ioTimeout))

	groupID := group.KeyPair.ID
	peerID := id.String()
	req := subscribeRequest{GroupID: groupID, Stores: make([]storeCursor, 0, len(storeNames))}
	for _, name := range storeNames {
		req.Stores = append(req.Stores, storeCursor{StoreName: name, SinceUnix: f.store.LastFromPeerForStore(groupID, name, peerID)})
	}
	if err := json.NewEncoder(s).Encode(req); err != nil {
		log.Printf("realm maps: failed to send subscribe request to %s: %v", reg.PeerLabel(id.String()), err)
		return
	}

	var resp subscribeResponse
	if err := json.NewDecoder(io.LimitReader(s, maxBytes)).Decode(&resp); err != nil {
		log.Printf("realm maps: failed to read subscribe response from %s: %v", reg.PeerLabel(id.String()), err)
		return
	}

	maxTsByStore := make(map[string]int64, len(storeNames))
	for _, env := range resp.Events {
		if f.applyVerified(group, env) {
			f.broadcastExcept(reg, group, env.StoreName, env, peerID)
		}
		if env.UpdatedAtUnixMillis > maxTsByStore[env.StoreName] {
			maxTsByStore[env.StoreName] = env.UpdatedAtUnixMillis
		}
	}
	for storeName, ts := range maxTsByStore {
		if err := f.store.RecordFromPeerForStore(groupID, storeName, peerID, ts); err != nil {
			log.Printf("realm maps: failed to persist subscribe cursor for peer %s/%s: %v", reg.PeerLabel(id.String()), storeName, err)
		}
	}
	log.Printf("realm maps: subscribed to peer %s for group %s stores %v (%d event(s) received)", reg.PeerLabel(id.String()), group.Label(), storeNames, len(resp.Events))
}

func (f *Feature) sendUnsubscribe(reg *realm.Registrar, id peer.ID, groupID string, storeNames []string) {
	h := reg.Host()
	ctx := reg.Context()
	if h == nil || ctx == nil {
		return
	}
	streamCtx, cancel := context.WithTimeout(ctx, ioTimeout)
	s, err := h.NewStream(streamCtx, id, UnsubscribeProtocolID)
	cancel()
	if err != nil {
		log.Printf("realm maps: peer %s unreachable for unsubscribe: %v", reg.PeerLabel(id.String()), err)
		return
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(ioTimeout))
	req := unsubscribeRequest{GroupID: groupID, StoreNames: storeNames}
	if err := json.NewEncoder(s).Encode(req); err != nil {
		log.Printf("realm maps: failed to send unsubscribe request to %s: %v", reg.PeerLabel(id.String()), err)
		return
	}
	log.Printf("realm maps: unsubscribed from peer %s for group %s stores %v", reg.PeerLabel(id.String()), model.GroupLabel(reg.Config().Groups, groupID), storeNames)
}

func (f *Feature) applyVerified(group model.Group, env model.MapEventEnvelope) bool {
	if !verifyEvent(group, env) {
		log.Printf("realm maps: dropping event for group %s with invalid signature", group.Label())
		return false
	}
	entry := model.MapEntry{Value: env.Value, Deleted: env.Deleted, UpdatedAtUnixMillis: env.UpdatedAtUnixMillis, OriginPeerID: env.OriginPeerID, Nonce: env.Nonce, IdentitySignature: env.IdentitySignature}
	changed, err := f.store.ApplyEvent(env.GroupID, env.StoreName, env.Key, entry)
	if err != nil {
		log.Printf("realm maps: failed to persist event for group %s: %v", group.Label(), err)
		return false
	}
	if changed {
		log.Printf("realm maps: applied event for %s/%s key=%q deleted=%v from peer %s", group.Label(), env.StoreName, env.Key, env.Deleted, f.registrar().PeerLabel(env.OriginPeerID))
	}
	return changed
}

// Stream handlers

func (f *Feature) handlePushStream(reg *realm.Registrar) network.StreamHandler {
	return func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(ioTimeout))

		var env model.MapEventEnvelope
		if err := json.NewDecoder(io.LimitReader(s, maxBytes)).Decode(&env); err != nil {
			log.Printf("realm maps: failed to decode incoming push: %v", err)
			return
		}
		group, ok := model.FindGroupByID(reg.Config().Groups, env.GroupID)
		if !ok {
			return
		}
		if f.applyVerified(group, env) {
			f.broadcastExcept(reg, group, env.StoreName, env, s.Conn().RemotePeer().String())
		}
	}
}

func (f *Feature) handleSubscribeStream(reg *realm.Registrar) network.StreamHandler {
	return func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(ioTimeout))

		var req subscribeRequest
		if err := json.NewDecoder(io.LimitReader(s, maxBytes)).Decode(&req); err != nil {
			log.Printf("realm maps: failed to decode subscribe request: %v", err)
			return
		}

		remotePeerID := s.Conn().RemotePeer().String()
		storeNames := make([]string, len(req.Stores))
		for i, sc := range req.Stores {
			storeNames[i] = sc.StoreName
		}
		log.Printf("realm maps: received subscribe request from peer %s for group %s stores %v", reg.PeerLabel(remotePeerID), model.GroupLabel(reg.Config().Groups, req.GroupID), storeNames)

		group, ok := model.FindGroupByID(reg.Config().Groups, req.GroupID)
		if !ok {
			log.Printf("realm maps: rejecting subscribe request from peer %s: not a member of group %s ourselves", reg.PeerLabel(remotePeerID), model.ShortID(req.GroupID))
			_ = json.NewEncoder(s).Encode(subscribeResponse{})
			return
		}

		info, known := reg.Peers().Get(remotePeerID)
		isMember := false
		for _, gn := range info.GroupNames {
			if gn == group.Name {
				isMember = true
				break
			}
		}
		if !known || !isMember {
			log.Printf("realm maps: rejecting subscribe request from peer %s: not a confirmed member of group %s", reg.PeerLabel(remotePeerID), group.Label())
			_ = json.NewEncoder(s).Encode(subscribeResponse{})
			return
		}

		var resp subscribeResponse
		for _, sc := range req.Stores {
			f.addIncomingSub(req.GroupID, sc.StoreName, remotePeerID)
			for _, ev := range f.store.EventsSinceForStore(req.GroupID, sc.StoreName, sc.SinceUnix) {
				env, err := signEvent(group, ev)
				if err != nil {
					log.Printf("realm maps: failed to sign event for subscribe response: %v", err)
					continue
				}
				resp.Events = append(resp.Events, env)
			}
		}
		if err := json.NewEncoder(s).Encode(resp); err != nil {
			log.Printf("realm maps: failed to send subscribe response: %v", err)
			return
		}
		log.Printf("realm maps: accepted subscribe request from peer %s for group %s stores %v (%d event(s) sent)", reg.PeerLabel(remotePeerID), group.Label(), storeNames, len(resp.Events))
	}
}

func (f *Feature) handleUnsubscribeStream(reg *realm.Registrar) network.StreamHandler {
	return func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(ioTimeout))

		var req unsubscribeRequest
		if err := json.NewDecoder(io.LimitReader(s, maxBytes)).Decode(&req); err != nil {
			log.Printf("realm maps: failed to decode unsubscribe request: %v", err)
			return
		}
		remotePeerID := s.Conn().RemotePeer().String()
		log.Printf("realm maps: received unsubscribe request from peer %s for group %s stores %v", reg.PeerLabel(remotePeerID), model.GroupLabel(reg.Config().Groups, req.GroupID), req.StoreNames)
		for _, storeName := range req.StoreNames {
			f.removeIncomingSub(req.GroupID, storeName, remotePeerID)
		}
	}
}

// Incoming subscriptions

func (f *Feature) addIncomingSub(groupID, storeName, peerID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	byStore := f.incomingSubs[groupID]
	if byStore == nil {
		byStore = map[string]map[string]bool{}
		f.incomingSubs[groupID] = byStore
	}
	peers := byStore[storeName]
	if peers == nil {
		peers = map[string]bool{}
		byStore[storeName] = peers
	}
	peers[peerID] = true
}

func (f *Feature) removeIncomingSub(groupID, storeName, peerID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if byStore, ok := f.incomingSubs[groupID]; ok {
		delete(byStore[storeName], peerID)
	}
}

func (f *Feature) incomingSubscribers(groupID, storeName string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	peers := f.incomingSubs[groupID][storeName]
	result := make([]string, 0, len(peers))
	for pid := range peers {
		result = append(result, pid)
	}
	return result
}

func (f *Feature) OnPeerDisconnected(id peer.ID) {
	peerID := id.String()

	f.mu.Lock()
	defer f.mu.Unlock()
	for _, gs := range f.groupStates {
		delete(gs.initializedPeers, peerID)
		for storeName := range gs.subscribedPeers {
			delete(gs.subscribedPeers[storeName], peerID)
		}
	}
	for _, byStore := range f.incomingSubs {
		for storeName := range byStore {
			delete(byStore[storeName], peerID)
		}
	}
}

// Auto-delete sweep

func (f *Feature) RunPeriodic(reg *realm.Registrar) {
	now := time.Now()
	hourBucket := now.Truncate(time.Hour)

	f.mu.Lock()
	if f.lastSweptHour.Equal(hourBucket) || now.Minute() < f.sweepMinute {
		f.mu.Unlock()
		return
	}
	f.lastSweptHour = hourBucket
	f.mu.Unlock()

	for _, group := range reg.Config().Groups {
		groupID := group.KeyPair.ID
		cfgMap := f.store.GetMap(groupID, SystemConfigStoreName)
		for storeName, entry := range cfgMap.Entries {
			var cfg model.RealmMapConfig
			if err := json.Unmarshal([]byte(entry.Value), &cfg); err != nil || cfg.AutoDeleteEntriesHours <= 0 {
				continue
			}
			cutoff := now.Add(-time.Duration(cfg.AutoDeleteEntriesHours) * time.Hour).UnixMilli()
			rm := f.store.GetMap(groupID, storeName)
			for key, e := range rm.Entries {
				if e.UpdatedAtUnixMillis < cutoff {
					if err := f.DeleteValue(groupID, storeName, key); err != nil {
						log.Printf("realm maps: failed to auto-delete expired entry %q in %s/%s: %v", key, group.Label(), storeName, err)
					}
				}
			}
		}
	}
}

// Signatures

func signEvent(group model.Group, ev model.MapEvent) (model.MapEventEnvelope, error) {
	priv, err := keypair.PrivateKey(group.KeyPair)
	if err != nil {
		return model.MapEventEnvelope{}, fmt.Errorf("realm maps: failed to load private key for group %q: %w", group.Name, err)
	}
	sig, err := priv.Sign(ev.SigningBytes())
	if err != nil {
		return model.MapEventEnvelope{}, fmt.Errorf("realm maps: failed to sign event: %w", err)
	}
	return model.MapEventEnvelope{MapEvent: ev, Signature: sig}, nil
}

func verifyEvent(group model.Group, env model.MapEventEnvelope) bool {
	priv, err := keypair.PrivateKey(group.KeyPair)
	if err != nil {
		return false
	}
	ok, err := priv.GetPublic().Verify(env.MapEvent.SigningBytes(), env.Signature)
	return err == nil && ok
}
