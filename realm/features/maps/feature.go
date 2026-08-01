package maps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
	// fire-and-forget. Sent to every connected, confirmed member of the
	// scope group right after a local edit; a peer that misses it (offline,
	// or simply never directly connected) still catches up via
	// SyncProtocolID the next time it connects or its group membership is
	// (re)confirmed.
	PushProtocolID = protocol.ID("/foilen-box/maps-push/1.0.0")

	// SyncProtocolID is a synchronous request/response, same shape as
	// services.ListProtocolID: "give me every event for this scope newer
	// than sinceUnix."
	SyncProtocolID = protocol.ID("/foilen-box/maps-sync/1.0.0")

	ioTimeout = 10 * time.Second
	maxBytes  = 256 * 1024

	// FeatureName namespaces this feature, even though it declares no
	// Permission actions: a valid event signature (made with the scope
	// group's own private key, which every member already holds) is itself
	// the write authorization, and subscription/read access is simply
	// "confirmed member of the scope group" (see OnPeerConnected/
	// OnGroupConfirmed) — there's nothing left for a Permission to gate.
	FeatureName = "common/maps"
)

// syncRequest asks for every event under ScopeID newer than SinceUnix.
type syncRequest struct {
	ScopeID   string `json:"scopeId"`
	SinceUnix int64  `json:"sinceUnix"`
}

type syncResponse struct {
	Events []model.MapEventEnvelope `json:"events"`
}

// Feature implements realm.Feature, realm.PeerConnectedHook, and
// realm.GroupConfirmedHook: both trigger the same "pull anything I might be
// missing" sync against the peer, which is what makes "peers we share a
// group with" and "maps of that group" converge without any explicit
// subscribe/unsubscribe bookkeeping.
type Feature struct {
	store *Store

	mu  sync.Mutex
	reg *realm.Registrar
}

// New builds the maps Feature backed by store (see NewStore).
func New(store *Store) *Feature {
	return &Feature{store: store}
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
	f.mu.Unlock()
	reg.SetStreamHandler(PushProtocolID, f.handlePushStream(reg))
	reg.SetStreamHandler(SyncProtocolID, f.handleSyncStream(reg))
}

// OnPeerConnected pulls, for every group id shared knows it belongs to,
// anything we might be missing, per realm.PeerConnectedHook.
func (f *Feature) OnPeerConnected(reg *realm.Registrar, id peer.ID) {
	info, ok := reg.Peers().Get(id.String())
	if !ok {
		return
	}
	cfg := reg.Config()
	for _, groupName := range info.GroupNames {
		if group, ok := findGroupByName(cfg.Groups, groupName); ok {
			go f.pullFrom(reg, id, group)
		}
	}
}

// OnGroupConfirmed pulls maps.group's state from id the moment its
// membership is cryptographically confirmed, per realm.GroupConfirmedHook —
// covers the case where the challenge completes after OnPeerConnected
// already ran and found no confirmed groups yet.
func (f *Feature) OnGroupConfirmed(reg *realm.Registrar, id peer.ID, group model.Group) {
	f.pullFrom(reg, id, group)
}

// ListSummaries returns every locally-known map for a currently-configured
// group, for the UI's map list.
func (f *Feature) ListSummaries() []model.RealmMapSummary {
	reg := f.registrar()
	if reg == nil {
		return nil
	}
	return f.store.ListSummaries(reg.Config().Groups)
}

// GetMap returns scopeId/storeName's current entries.
func (f *Feature) GetMap(scopeID, storeName string) model.RealmMap {
	return f.store.GetMap(scopeID, storeName)
}

// CreateMap ensures an (initially empty) map exists locally for
// scopeId/storeName, so it shows up in ListSummaries before any key is set.
func (f *Feature) CreateMap(scopeID, storeName string) error {
	if _, err := f.groupFor(scopeID); err != nil {
		return err
	}
	return f.store.CreateMap(scopeID, storeName)
}

// SetValue writes key=value into scopeId/storeName, applies it locally, and
// broadcasts it (signed) to every currently-connected, confirmed member of
// the scope group.
func (f *Feature) SetValue(scopeID, storeName, key, value string) error {
	return f.mutate(scopeID, storeName, key, model.MapEntry{Value: value})
}

// DeleteValue tombstones key inside scopeId/storeName and broadcasts the
// deletion, same as SetValue.
func (f *Feature) DeleteValue(scopeID, storeName, key string) error {
	return f.mutate(scopeID, storeName, key, model.MapEntry{Deleted: true})
}

// DeleteMap tombstones every current key in scopeId/storeName (broadcasting
// each deletion like any edit); once every entry is deleted the map drops
// out of ListSummaries on its own (see Store.ListSummaries).
func (f *Feature) DeleteMap(scopeID, storeName string) error {
	rm := f.store.GetMap(scopeID, storeName)
	for key := range rm.Entries {
		if err := f.DeleteValue(scopeID, storeName, key); err != nil {
			return err
		}
	}
	return nil
}

func (f *Feature) mutate(scopeID, storeName, key string, entry model.MapEntry) error {
	reg := f.registrar()
	if reg == nil {
		return fmt.Errorf("realm maps: not registered on an engine")
	}
	group, err := f.groupFor(scopeID)
	if err != nil {
		return err
	}

	entry.UpdatedAtUnixMillis = time.Now().UnixMilli()
	entry.OriginPeerID = reg.Config().PeerID.ID

	if _, err := f.store.ApplyEvent(scopeID, storeName, key, entry); err != nil {
		return err
	}

	ev := model.MapEvent{ScopeID: scopeID, StoreName: storeName, Key: key, Value: entry.Value, Deleted: entry.Deleted, UpdatedAtUnixMillis: entry.UpdatedAtUnixMillis, OriginPeerID: entry.OriginPeerID}
	env, err := signEvent(group, ev)
	if err != nil {
		return err
	}
	f.broadcast(reg, group, env)
	return nil
}

// groupFor returns the locally-configured group whose public id is
// scopeID, or an error if we don't hold that group's key.
func (f *Feature) groupFor(scopeID string) (model.Group, error) {
	reg := f.registrar()
	if reg == nil {
		return model.Group{}, fmt.Errorf("realm maps: not registered on an engine")
	}
	group, ok := findGroupByID(reg.Config().Groups, scopeID)
	if !ok {
		return model.Group{}, fmt.Errorf("realm maps: no locally-configured group for scope %q", scopeID)
	}
	return group, nil
}

// broadcast sends env to every peer currently connected and confirmed as a
// member of group, fire-and-forget: a peer that's offline or unreachable
// right now will simply pick this change up via its own next sync pull.
func (f *Feature) broadcast(reg *realm.Registrar, group model.Group, env model.MapEventEnvelope) {
	h := reg.Host()
	ctx := reg.Context()
	if h == nil || ctx == nil {
		return
	}
	for _, info := range reg.Peers().List() {
		if !info.Connected {
			continue
		}
		hasGroup := false
		for _, gn := range info.GroupNames {
			if gn == group.Name {
				hasGroup = true
				break
			}
		}
		if !hasGroup {
			continue
		}
		pid, err := peer.Decode(info.ID)
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

// pullFrom asks id for every event under group's scope newer than the
// newest one we already have, verifies and applies each. Used both from
// OnPeerConnected and OnGroupConfirmed.
func (f *Feature) pullFrom(reg *realm.Registrar, id peer.ID, group model.Group) {
	h := reg.Host()
	ctx := reg.Context()
	if h == nil || ctx == nil {
		return
	}
	if err := reg.EnsureConnected(ctx, id); err != nil {
		return
	}

	streamCtx, cancel := context.WithTimeout(ctx, ioTimeout)
	s, err := h.NewStream(streamCtx, id, SyncProtocolID)
	cancel()
	if err != nil {
		log.Printf("realm maps: peer %s unreachable for sync: %v", id, err)
		return
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(ioTimeout))

	peerID := id.String()
	req := syncRequest{ScopeID: group.KeyPair.ID, SinceUnix: f.store.LastFromPeer(group.KeyPair.ID, peerID)}
	if err := json.NewEncoder(s).Encode(req); err != nil {
		log.Printf("realm maps: failed to send sync request to %s: %v", id, err)
		return
	}

	var resp syncResponse
	if err := json.NewDecoder(io.LimitReader(s, maxBytes)).Decode(&resp); err != nil {
		log.Printf("realm maps: failed to read sync response from %s: %v", id, err)
		return
	}
	var maxTs int64
	for _, env := range resp.Events {
		f.applyVerified(group, env)
		if env.UpdatedAtUnixMillis > maxTs {
			maxTs = env.UpdatedAtUnixMillis
		}
	}
	if maxTs > 0 {
		if err := f.store.RecordFromPeer(group.KeyPair.ID, peerID, maxTs); err != nil {
			log.Printf("realm maps: failed to persist sync cursor for peer %s: %v", id, err)
		}
	}
}

// applyVerified verifies env's signature against group's key and, if valid,
// merges it into the store.
func (f *Feature) applyVerified(group model.Group, env model.MapEventEnvelope) {
	if !verifyEvent(group, env) {
		log.Printf("realm maps: dropping event for scope %q with invalid signature", env.ScopeID)
		return
	}
	entry := model.MapEntry{Value: env.Value, Deleted: env.Deleted, UpdatedAtUnixMillis: env.UpdatedAtUnixMillis, OriginPeerID: env.OriginPeerID}
	if _, err := f.store.ApplyEvent(env.ScopeID, env.StoreName, env.Key, entry); err != nil {
		log.Printf("realm maps: failed to persist event for scope %q: %v", env.ScopeID, err)
	}
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
		group, ok := findGroupByID(reg.Config().Groups, env.ScopeID)
		if !ok {
			// We're not a member of this scope's group (or don't hold its
			// key) — can't verify, so we can't trust it either.
			return
		}
		f.applyVerified(group, env)
	}
}

// handleSyncStream is the libp2p stream handler for SyncProtocolID: answers
// with every event we hold for the requested scope newer than SinceUnix, if
// we're a member of that scope's group (if not, we have nothing to verify
// against anyway, so nothing meaningful to answer with).
func (f *Feature) handleSyncStream(reg *realm.Registrar) network.StreamHandler {
	return func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(ioTimeout))

		var req syncRequest
		if err := json.NewDecoder(io.LimitReader(s, maxBytes)).Decode(&req); err != nil {
			log.Printf("realm maps: failed to decode sync request: %v", err)
			return
		}
		group, ok := findGroupByID(reg.Config().Groups, req.ScopeID)
		if !ok {
			_ = json.NewEncoder(s).Encode(syncResponse{})
			return
		}

		evs := f.store.EventsSince(req.ScopeID, req.SinceUnix)
		resp := syncResponse{Events: make([]model.MapEventEnvelope, 0, len(evs))}
		for _, ev := range evs {
			env, err := signEvent(group, ev)
			if err != nil {
				log.Printf("realm maps: failed to sign event for sync response: %v", err)
				continue
			}
			resp.Events = append(resp.Events, env)
		}
		if err := json.NewEncoder(s).Encode(resp); err != nil {
			log.Printf("realm maps: failed to send sync response: %v", err)
		}
	}
}

// signEvent signs ev's SigningBytes with group's private key — every member
// holds it, so a valid signature both proves and is the sole check for
// write authorization to that scope.
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
