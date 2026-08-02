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
	// PushProtocolID carries one signed MapEventEnvelope per stream,
	// fire-and-forget. Sent to every peer currently subscribed to that
	// specific group/store (see broadcast); a peer that misses it (offline,
	// or never subscribed) catches up via SubscribeProtocolID's catch-up
	// events the next time it (re)subscribes.
	PushProtocolID = protocol.ID("/foilen-box/maps-push/1.0.0")

	// SubscribeProtocolID is a synchronous request/response: "I want
	// pushes for these stores under this group from now on, and give me
	// every event newer than the per-store cursor I already have."
	SubscribeProtocolID = protocol.ID("/foilen-box/maps-subscribe/1.0.0")

	// UnsubscribeProtocolID is fire-and-forget: "stop pushing me these
	// stores under this group."
	UnsubscribeProtocolID = protocol.ID("/foilen-box/maps-unsubscribe/1.0.0")

	// SystemConfigStoreName is the reserved store holding one RealmMapConfig
	// entry (JSON-encoded) per map name, keyed by that map's storeName. Like
	// any other store, it isn't special-cased for read/write access — it's
	// just the one every peer watches to know which other stores to
	// subscribe to (see reconcileDesiredStores) and what settings (e.g.
	// AutoDeleteEntriesHours) apply to them.
	SystemConfigStoreName = "_realmMaps"

	ioTimeout = 10 * time.Second
	maxBytes  = 256 * 1024

	// FeatureName namespaces this feature, even though it declares no
	// Permission actions: a valid event signature (made with the group's
	// own private key, which every member already holds) is itself the
	// write authorization, and subscribe access is simply "confirmed member
	// of the group" (see handleSubscribeStream) — there's nothing left for
	// a Permission to gate.
	FeatureName = "common/maps"
)

// storeCursor is one entry of a subscribeRequest: "give me events for
// StoreName newer than SinceUnix."
type storeCursor struct {
	StoreName string `json:"storeName"`
	SinceUnix int64  `json:"sinceUnix"`
}

// subscribeRequest asks to subscribe to (and catch up on) each of Stores
// under GroupID.
type subscribeRequest struct {
	GroupID string        `json:"groupId"`
	Stores  []storeCursor `json:"stores"`
}

type subscribeResponse struct {
	Events []model.MapEventEnvelope `json:"events"`
}

// unsubscribeRequest asks to stop receiving pushes for StoreNames under
// GroupID.
type unsubscribeRequest struct {
	GroupID    string   `json:"groupId"`
	StoreNames []string `json:"storeNames"`
}

// groupSubs is one group's worth of outgoing subscription bookkeeping: what
// we want from peers, and who we've already asked.
type groupSubs struct {
	initializedPeers map[string]bool            // peerID -> done initial (common + _realmMaps) subscribe
	desiredStores    map[string]bool            // mirrors live keys of local _realmMaps for this group
	subscribedPeers  map[string]map[string]bool // storeName -> peerID -> bool (peers we've asked for this store)
}

// Feature implements realm.Feature, realm.PeerConnectedHook,
// realm.GroupConfirmedHook, realm.PeerDisconnectedHook, and
// realm.PeriodicHook. Unlike the old blanket-sync design, a peer only
// receives pushes for the stores it has explicitly subscribed to (see
// incomingSubs/broadcast); onPeerAvailable and reconcileDesiredStores keep
// our own outgoing subscriptions in sync with the local _realmMaps map.
type Feature struct {
	store *Store

	mu  sync.Mutex
	reg *realm.Registrar

	// incomingSubs: who (peerID) is subscribed to which storeName under
	// which group — consulted by broadcast() to decide who to push to.
	incomingSubs map[string]map[string]map[string]bool // groupID -> storeName -> peerID -> bool

	// groupStates: outgoing per-group subscription state, see groupSubs.
	groupStates map[string]*groupSubs // groupID -> ...

	changeListenerInstalled bool

	// sweepMinute is the minute-of-hour (fixed at process startup) this
	// instance runs its auto-delete sweep at, so peers sharing a group
	// don't all sweep on the same tick (see RunPeriodic).
	sweepMinute   int
	lastSweptHour time.Time
}

// New builds the maps Feature backed by store (see NewStore).
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

	// Only install once: RegisterHandlers is called again whenever the
	// engine's host is recreated, and Store.Subscribe has no dedup of its
	// own — a second install would fire reconcileDesiredStores twice per
	// _realmMaps change.
	if !alreadyInstalled {
		f.store.Subscribe(f.onStoreChange)
	}
}

// onStoreChange is the internal Store.Subscribe listener that keeps our
// outgoing subscriptions in sync with each group's _realmMaps content.
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

// OnPeerConnected converges with OnGroupConfirmed below via onPeerAvailable,
// for every group id shared knows it belongs to, per realm.PeerConnectedHook.
func (f *Feature) OnPeerConnected(reg *realm.Registrar, id peer.ID) {
	info, ok := reg.Peers().Get(id.String())
	if !ok {
		return
	}
	cfg := reg.Config()
	for _, groupName := range info.GroupNames {
		if group, ok := findGroupByName(cfg.Groups, groupName); ok {
			go f.onPeerAvailable(reg, id, group)
		}
	}
}

// OnGroupConfirmed converges with OnPeerConnected above via onPeerAvailable
// the moment id's membership in group is cryptographically confirmed, per
// realm.GroupConfirmedHook — covers the case where the challenge completes
// after OnPeerConnected already ran and found no confirmed groups yet.
func (f *Feature) OnGroupConfirmed(reg *realm.Registrar, id peer.ID, group model.Group) {
	f.onPeerAvailable(reg, id, group)
}

// onPeerAvailable runs once per (peer, group): does the initial subscribe to
// the system stores ("common" plus SystemConfigStoreName), then reconciles
// our desired stores against the now-fetched _realmMaps content, which
// naturally also subscribes to whatever application stores that group
// already has.
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

// reconcileDesiredStores diffs the group's local _realmMaps live keys
// against what we last knew, subscribes every already-initialized,
// connected peer to every currently-desired store it hasn't been asked for
// yet (via claimStoresToSubscribe's per-peer bookkeeping, so this is cheap
// even though it re-walks stores that aren't new), and unsubscribes+purges
// stores that are no longer desired by anyone. Called both after a peer's
// initial subscribe (onPeerAvailable) and whenever _realmMaps itself changes
// (onStoreChange).
//
// Using the full desired set (rather than only the group-wide "added" diff)
// matters for a peer that (re)connects after a store already exists: without
// it, that peer would never be asked to subscribe to pre-existing stores,
// since "added" only fires the first time a store name is ever seen for the
// group, regardless of which peer triggered this call.
func (f *Feature) reconcileDesiredStores(reg *realm.Registrar, groupID string) {
	group, ok := findGroupByID(reg.Config().Groups, groupID)
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
				log.Printf("realm maps: failed to purge removed store %q for group %q: %v", storeName, groupID, err)
			}
		}
	}
}

// groupSubsFor returns (creating if necessary) groupID's bookkeeping.
func (f *Feature) groupSubsFor(groupID string) *groupSubs {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.groupStatesLocked(groupID)
}

// groupStatesLocked assumes f.mu is already held.
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

// claimStoresToSubscribe filters storeNames down to the ones gs doesn't
// already record as asked of peerID, marking them asked as it goes (so a
// concurrent caller won't also claim them) and returning only the ones the
// caller should actually go request.
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

// ListSummaries returns every locally-known map for a currently-configured
// group, for the UI's map list, with each summary's AutoDeleteEntriesHours
// filled in from that group's _realmMaps config (0 if missing/unparseable).
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

// EncryptionIdentityID returns the identityId groupID/storeName is
// encrypted to, or "" if it isn't encrypted.
func (f *Feature) EncryptionIdentityID(groupID, storeName string) string {
	cfg := f.configForStore(groupID, storeName)
	if cfg.Encryption == nil {
		return ""
	}
	return cfg.Encryption.IdentityID
}

// configForStore returns storeName's RealmMapConfig within groupID's
// _realmMaps system store (see SystemConfigStoreName), or a zero-value
// config if none is set yet (e.g. storeName is itself SystemConfigStoreName,
// or SetValue was somehow called before CreateMap).
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

// GetMap returns groupID/storeName's current entries. For an encrypted map
// (see RealmMapConfig.Encryption), entries are only decrypted -- and
// returned keyed by their real keys, with plaintext values -- if the target
// identity is configured locally; encrypted reports whether the map is
// encrypted at all, and available reports whether decryption was actually
// possible. encrypted&&!available means the caller can see the map exists
// (and is replicating/copying it, per the group signature) but not read it.
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
	identity, ok := findIdentityByID(reg.Config().Identities, cfg.Encryption.IdentityID)
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
		log.Printf("realm maps: failed to unlock symmetric key for %s/%s: %v", groupID, storeName, err)
		return locked, true, false
	}
	identityPub := identityPriv.GetPublic()

	decrypted := model.RealmMap{GroupID: groupID, StoreName: storeName, Entries: map[string]model.MapEntry{}}
	for storageKey, entry := range raw.Entries {
		ev := model.MapEvent{GroupID: groupID, StoreName: storeName, Key: storageKey, Value: entry.Value, Deleted: entry.Deleted, UpdatedAtUnixMillis: entry.UpdatedAtUnixMillis, OriginPeerID: entry.OriginPeerID, Nonce: entry.Nonce, IdentitySignature: entry.IdentitySignature}
		if !verifyEncryptedEvent(identityPub, ev) {
			log.Printf("realm maps: dropping entry with invalid identity signature in %s/%s", groupID, storeName)
			continue
		}
		realKey, value, err := decryptEntry(entry.Value, entry.Nonce, symmetricKey)
		if err != nil {
			log.Printf("realm maps: failed to decrypt entry in %s/%s: %v", groupID, storeName, err)
			continue
		}
		decrypted.Entries[realKey] = model.MapEntry{Value: value, UpdatedAtUnixMillis: entry.UpdatedAtUnixMillis, OriginPeerID: entry.OriginPeerID}
	}
	return decrypted, true, true
}

// CreateMap ensures an (initially empty) map exists locally for
// groupID/storeName, so it shows up in ListSummaries before any key is set,
// and writes config into _realmMaps — which is what makes the store show up
// as a live key other peers watching this group will subscribe to.
// Re-creating an existing map updates its config.
//
// If encryptToIdentityID is non-empty, the map's entries become
// confidential to that identity: a random symmetric key is generated and
// sealed to the identity's public key, which is derivable from
// encryptToIdentityID alone (see identityPubKeyFromID) — the caller doesn't
// need to hold that identity locally just to create the map, only to later
// write to or read it.
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

// SetValue writes key=value into groupID/storeName, applies it locally, and
// broadcasts it (signed) to every peer currently subscribed to that store.
func (f *Feature) SetValue(groupID, storeName, key, value string) error {
	return f.mutate(groupID, storeName, key, model.MapEntry{Value: value})
}

// DeleteValue tombstones key inside groupID/storeName and broadcasts the
// deletion, same as SetValue.
func (f *Feature) DeleteValue(groupID, storeName, key string) error {
	return f.mutate(groupID, storeName, key, model.MapEntry{Deleted: true})
}

// DeleteMap removes storeName from groupID entirely: it deletes the map's
// entry from _realmMaps (which broadcasts normally, so every subscribed
// peer sees the key disappear and self-reconciles via reconcileDesiredStores)
// and also purges it locally right away, rather than waiting on our own
// reconcile pass to get to it.
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

	if _, err := f.store.ApplyEvent(groupID, storeName, storageKey, entry); err != nil {
		return err
	}

	ev := model.MapEvent{GroupID: groupID, StoreName: storeName, Key: storageKey, Value: entry.Value, Deleted: entry.Deleted, UpdatedAtUnixMillis: entry.UpdatedAtUnixMillis, OriginPeerID: entry.OriginPeerID, Nonce: entry.Nonce, IdentitySignature: entry.IdentitySignature}
	env, err := signEvent(group, ev)
	if err != nil {
		return err
	}
	log.Printf("realm maps: local write to %s/%s key=%q deleted=%v", group.Name, storeName, key, entry.Deleted)
	f.broadcast(reg, group, storeName, env)
	return nil
}

// encryptMutation transforms a plaintext mutation (real key, and for
// non-deletes a real Value) for an encrypted map into its wire form: the
// storage key becomes hashKey(identityID, key) so the real key never leaves
// this function, Value becomes ciphertext (a delete tombstone carries no
// Value — there's nothing about it worth hiding), and the result is signed
// with the target identity's private key. Requires that identity to be
// configured locally — without it, writing to an encrypted map isn't
// possible, matching that only identity holders can meaningfully write.
func (f *Feature) encryptMutation(reg *realm.Registrar, enc *model.MapEncryptionConfig, groupID, storeName, key string, entry model.MapEntry) (string, model.MapEntry, error) {
	identity, ok := findIdentityByID(reg.Config().Identities, enc.IdentityID)
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

// groupFor returns the locally-configured group whose public id is
// groupID, or an error if we don't hold that group's key.
func (f *Feature) groupFor(groupID string) (model.Group, error) {
	reg := f.registrar()
	if reg == nil {
		return model.Group{}, fmt.Errorf("realm maps: not registered on an engine")
	}
	group, ok := findGroupByID(reg.Config().Groups, groupID)
	if !ok {
		return model.Group{}, fmt.Errorf("realm maps: no locally-configured group for %q", groupID)
	}
	return group, nil
}

// broadcast sends env to every peer currently subscribed to
// group/storeName, fire-and-forget: incomingSubs is already pruned of a peer
// the moment it disconnects (see OnPeerDisconnected), so there's no need to
// re-check Connected here — a peer that's actually gone will just fail
// sendPush's stream open, which logs it instead of silently dropping the
// event, and picks the change up via its next subscribe catch-up.
func (f *Feature) broadcast(reg *realm.Registrar, group model.Group, storeName string, env model.MapEventEnvelope) {
	h := reg.Host()
	ctx := reg.Context()
	if h == nil || ctx == nil {
		return
	}
	recipients := f.incomingSubscribers(group.KeyPair.ID, storeName)
	if len(recipients) > 0 {
		log.Printf("realm maps: broadcasting %s/%s key=%q to %d subscriber(s)", group.Name, storeName, env.Key, len(recipients))
	}
	for _, peerID := range recipients {
		pid, err := peer.Decode(peerID)
		if err != nil {
			continue
		}
		go sendPush(ctx, h, pid, env)
	}
}

func sendPush(ctx context.Context, h host.Host, pid peer.ID, env model.MapEventEnvelope) {
	streamCtx, cancel := context.WithTimeout(ctx, ioTimeout)
	defer cancel()
	s, err := h.NewStream(streamCtx, pid, PushProtocolID)
	if err != nil {
		log.Printf("realm maps: peer %s unreachable for push: %v", pid, err)
		return
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(ioTimeout))
	if err := json.NewEncoder(s).Encode(env); err != nil {
		log.Printf("realm maps: failed to push to %s: %v", pid, err)
	}
}

// subscribeToPeer asks id to subscribe us to storeNames under group,
// catching us up with everything newer than each store's per-peer cursor,
// verifies and applies each returned event, and advances the cursors.
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
		log.Printf("realm maps: peer %s unreachable for subscribe: %v", id, err)
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
		log.Printf("realm maps: failed to send subscribe request to %s: %v", id, err)
		return
	}

	var resp subscribeResponse
	if err := json.NewDecoder(io.LimitReader(s, maxBytes)).Decode(&resp); err != nil {
		log.Printf("realm maps: failed to read subscribe response from %s: %v", id, err)
		return
	}

	maxTsByStore := make(map[string]int64, len(storeNames))
	for _, env := range resp.Events {
		f.applyVerified(group, env)
		if env.UpdatedAtUnixMillis > maxTsByStore[env.StoreName] {
			maxTsByStore[env.StoreName] = env.UpdatedAtUnixMillis
		}
	}
	for storeName, ts := range maxTsByStore {
		if err := f.store.RecordFromPeerForStore(groupID, storeName, peerID, ts); err != nil {
			log.Printf("realm maps: failed to persist subscribe cursor for peer %s/%s: %v", id, storeName, err)
		}
	}
	log.Printf("realm maps: subscribed to peer %s for group %q stores %v (%d event(s) received)", id, group.Name, storeNames, len(resp.Events))
}

// sendUnsubscribe tells id to stop pushing us storeNames under groupID,
// fire-and-forget.
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
		log.Printf("realm maps: peer %s unreachable for unsubscribe: %v", id, err)
		return
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(ioTimeout))
	req := unsubscribeRequest{GroupID: groupID, StoreNames: storeNames}
	if err := json.NewEncoder(s).Encode(req); err != nil {
		log.Printf("realm maps: failed to send unsubscribe request to %s: %v", id, err)
		return
	}
	log.Printf("realm maps: unsubscribed from peer %s for group %q stores %v", id, groupID, storeNames)
}

// applyVerified verifies env's signature against group's key and, if valid,
// merges it into the store.
func (f *Feature) applyVerified(group model.Group, env model.MapEventEnvelope) {
	if !verifyEvent(group, env) {
		log.Printf("realm maps: dropping event for group %q with invalid signature", env.GroupID)
		return
	}
	entry := model.MapEntry{Value: env.Value, Deleted: env.Deleted, UpdatedAtUnixMillis: env.UpdatedAtUnixMillis, OriginPeerID: env.OriginPeerID, Nonce: env.Nonce, IdentitySignature: env.IdentitySignature}
	if _, err := f.store.ApplyEvent(env.GroupID, env.StoreName, env.Key, entry); err != nil {
		log.Printf("realm maps: failed to persist event for group %q: %v", env.GroupID, err)
		return
	}
	log.Printf("realm maps: applied event for %s/%s key=%q deleted=%v from peer %s", group.Name, env.StoreName, env.Key, env.Deleted, env.OriginPeerID)
}

// handlePushStream is the libp2p stream handler for PushProtocolID: one
// signed event, applied if it verifies against a group we're a member of.
func (f *Feature) handlePushStream(reg *realm.Registrar) network.StreamHandler {
	return func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(ioTimeout))

		var env model.MapEventEnvelope
		if err := json.NewDecoder(io.LimitReader(s, maxBytes)).Decode(&env); err != nil {
			log.Printf("realm maps: failed to decode incoming push: %v", err)
			return
		}
		group, ok := findGroupByID(reg.Config().Groups, env.GroupID)
		if !ok {
			// We're not a member of this group (or don't hold its key) —
			// can't verify, so we can't trust it either.
			return
		}
		f.applyVerified(group, env)
	}
}

// handleSubscribeStream is the libp2p stream handler for
// SubscribeProtocolID: registers the requester as an incoming subscriber
// for each requested store (so future broadcasts reach it) and answers with
// catch-up events for each, but only if the requester is a confirmed member
// of the requested group — otherwise (or if we don't hold that group
// ourselves) we can't verify anything, so we register nothing and answer
// empty.
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
		log.Printf("realm maps: received subscribe request from peer %s for group %q stores %v", remotePeerID, req.GroupID, storeNames)

		group, ok := findGroupByID(reg.Config().Groups, req.GroupID)
		if !ok {
			log.Printf("realm maps: rejecting subscribe request from peer %s: not a member of group %q ourselves", remotePeerID, req.GroupID)
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
			log.Printf("realm maps: rejecting subscribe request from peer %s: not a confirmed member of group %q", remotePeerID, group.Name)
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
		log.Printf("realm maps: accepted subscribe request from peer %s for group %q stores %v (%d event(s) sent)", remotePeerID, group.Name, storeNames, len(resp.Events))
	}
}

// handleUnsubscribeStream is the libp2p stream handler for
// UnsubscribeProtocolID: removes the requester from the incoming-subscriber
// table for each listed store. Fire-and-forget, no response sent.
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
		log.Printf("realm maps: received unsubscribe request from peer %s for group %q stores %v", remotePeerID, req.GroupID, req.StoreNames)
		for _, storeName := range req.StoreNames {
			f.removeIncomingSub(req.GroupID, storeName, remotePeerID)
		}
	}
}

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

// OnPeerDisconnected implements realm.PeerDisconnectedHook: forgets every
// purely in-memory subscription (incoming and outgoing) we kept for id,
// since none of it is persisted — a peer that reconnects starts over with a
// fresh initial subscribe (see onPeerAvailable).
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

// RunPeriodic implements realm.PeriodicHook, driven by the engine's
// keep-alive tick (currently every 10 minutes): at most once per hour, at
// this process's own randomly-chosen minute (so peers sharing a group don't
// all sweep on the same tick), tombstones every entry older than its map's
// configured AutoDeleteEntriesHours. Any single subscribed peer doing this
// is enough — the tombstone propagates to every other subscriber via the
// normal push/subscribe path — so correctness doesn't depend on any
// particular peer's turn coming up first.
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
						log.Printf("realm maps: failed to auto-delete expired entry %q in %s/%s: %v", key, groupID, storeName, err)
					}
				}
			}
		}
	}
}

// signEvent signs ev's SigningBytes with group's private key — every member
// holds it, so a valid signature both proves and is the sole check for
// write authorization to that group.
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

// verifyEvent reports whether env.Signature is a valid signature, made with
// group's private key, over env.MapEvent.SigningBytes().
func verifyEvent(group model.Group, env model.MapEventEnvelope) bool {
	priv, err := keypair.PrivateKey(group.KeyPair)
	if err != nil {
		return false
	}
	ok, err := priv.GetPublic().Verify(env.MapEvent.SigningBytes(), env.Signature)
	return err == nil && ok
}

// findGroupByID returns the locally-configured group whose public group id
// (KeyPair.ID) matches id.
func findGroupByID(groups []model.Group, id string) (model.Group, bool) {
	for _, g := range groups {
		if g.KeyPair.ID == id {
			return g, true
		}
	}
	return model.Group{}, false
}

// findGroupByName returns the locally-configured group named name.
func findGroupByName(groups []model.Group, name string) (model.Group, bool) {
	for _, g := range groups {
		if g.Name == name {
			return g, true
		}
	}
	return model.Group{}, false
}

// findIdentityByID returns the locally-configured identity whose public id
// (KeyPair.ID) matches id.
func findIdentityByID(identities []model.Identity, id string) (model.Identity, bool) {
	for _, idn := range identities {
		if idn.KeyPair.ID == id {
			return idn, true
		}
	}
	return model.Identity{}, false
}
