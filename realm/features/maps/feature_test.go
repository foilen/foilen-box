package maps

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	realm "foilen-realm"
	"foilen-realm/keypair"
	"foilen-realm/model"
	"foilen-realm/peers"
)

func testGroup(t *testing.T) model.Group {
	t.Helper()
	kp, err := keypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate: %v", err)
	}
	return model.Group{Name: "g", KeyPair: kp}
}

func TestSignAndVerifyEventRoundTrip(t *testing.T) {
	group := testGroup(t)
	ev := model.MapEvent{GroupID: group.KeyPair.ID, StoreName: "s", Key: "k", Value: "v", UpdatedAtUnixMillis: 42, OriginPeerID: "peer1"}

	env, err := signEvent(group, ev)
	if err != nil {
		t.Fatalf("signEvent: %v", err)
	}
	if !verifyEvent(group, env) {
		t.Fatal("verifyEvent should accept a validly-signed event")
	}
}

func TestVerifyEventRejectsTamperedPayload(t *testing.T) {
	group := testGroup(t)
	ev := model.MapEvent{GroupID: group.KeyPair.ID, StoreName: "s", Key: "k", Value: "v", UpdatedAtUnixMillis: 42, OriginPeerID: "peer1"}

	env, err := signEvent(group, ev)
	if err != nil {
		t.Fatalf("signEvent: %v", err)
	}
	env.Value = "tampered"
	if verifyEvent(group, env) {
		t.Fatal("verifyEvent should reject a tampered event")
	}
}

func TestVerifyEventRejectsWrongGroup(t *testing.T) {
	group := testGroup(t)
	otherGroup := testGroup(t)
	ev := model.MapEvent{GroupID: group.KeyPair.ID, StoreName: "s", Key: "k", Value: "v", UpdatedAtUnixMillis: 42}

	env, err := signEvent(group, ev)
	if err != nil {
		t.Fatalf("signEvent: %v", err)
	}
	if verifyEvent(otherGroup, env) {
		t.Fatal("verifyEvent should reject a signature made by a different group's key")
	}
}

// featurePair is two Feature instances, each on its own real libp2p host,
// connected and pre-seeded as already-confirmed group members — the actual
// challenge handshake is covered by group_challenge_test.go, not re-driven here.
type featurePair struct {
	f1, f2           *Feature
	peerID1, peerID2 peer.ID
	group            model.Group
	groupID          string
}

func newConnectedFeaturePair(t *testing.T) *featurePair {
	t.Helper()
	return newConnectedFeaturePairWithIdentities(t, nil, nil)
}

func newConnectedFeaturePairWithIdentities(t *testing.T, identities1, identities2 []model.Identity) *featurePair {
	t.Helper()

	group := testGroup(t)
	kp1, err := keypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate: %v", err)
	}
	kp2, err := keypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate: %v", err)
	}

	store1, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	store2, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	f1 := New(store1)
	f2 := New(store2)

	peerStore1, err := peers.New(t.TempDir())
	if err != nil {
		t.Fatalf("peers.New: %v", err)
	}
	peerStore2, err := peers.New(t.TempDir())
	if err != nil {
		t.Fatalf("peers.New: %v", err)
	}
	e1 := realm.New(t.TempDir(), peerStore1)
	e2 := realm.New(t.TempDir(), peerStore2)
	e1.Register(f1)
	e2.Register(f2)

	cfg1 := model.Config{PeerID: kp1, DhtMode: model.DhtModeClient, Groups: []model.Group{group}, Identities: identities1}
	cfg2 := model.Config{PeerID: kp2, DhtMode: model.DhtModeClient, Groups: []model.Group{group}, Identities: identities2}
	if err := e1.Start(cfg1); err != nil {
		t.Fatalf("e1.Start: %v", err)
	}
	if err := e2.Start(cfg2); err != nil {
		t.Fatalf("e2.Start: %v", err)
	}
	t.Cleanup(func() {
		e1.Stop()
		e2.Stop()
	})

	peerID1, err := peer.Decode(kp1.ID)
	if err != nil {
		t.Fatalf("peer.Decode: %v", err)
	}
	peerID2, err := peer.Decode(kp2.ID)
	if err != nil {
		t.Fatalf("peer.Decode: %v", err)
	}

	reg1 := f1.registrar()
	reg2 := f2.registrar()

	// Seed both peer stores before connecting: the engine's connection-ring
	// shaping runs immediately at Start() and disconnects any peer it doesn't
	// recognize as a required ring member.
	addrs1 := make([]string, 0, len(reg1.Host().Addrs()))
	for _, a := range reg1.Host().Addrs() {
		addrs1 = append(addrs1, a.String())
	}
	addrs2 := make([]string, 0, len(reg2.Host().Addrs()))
	for _, a := range reg2.Host().Addrs() {
		addrs2 = append(addrs2, a.String())
	}
	reg1.Peers().Upsert(model.PeerInfo{ID: peerID2.String(), GroupNames: []string{group.Name}, Connected: true, Addresses: addrs2}, "test")
	reg2.Peers().Upsert(model.PeerInfo{ID: peerID1.String(), GroupNames: []string{group.Name}, Connected: true, Addresses: addrs1}, "test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := reg1.Host().Connect(ctx, peer.AddrInfo{ID: peerID2, Addrs: reg2.Host().Addrs()}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	return &featurePair{f1: f1, f2: f2, peerID1: peerID1, peerID2: peerID2, group: group, groupID: group.KeyPair.ID}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", timeout)
	}
}

func TestSubscribeYieldsCatchUpEventsSincePerStoreCursor(t *testing.T) {
	p := newConnectedFeaturePair(t)

	if _, err := p.f2.store.ApplyEvent(p.groupID, "storeA", "k1", model.MapEntry{Value: "v1", UpdatedAtUnixMillis: 100}); err != nil {
		t.Fatal(err)
	}

	p.f1.subscribeToPeer(p.f1.registrar(), p.peerID2, p.group, []string{"storeA"})

	rm := p.f1.store.GetMap(p.groupID, "storeA")
	if got := rm.Entries["k1"].Value; got != "v1" {
		t.Fatalf("after subscribe, f1's copy of k1 = %q, want v1", got)
	}
	if got := p.f1.store.LastFromPeerForStore(p.groupID, "storeA", p.peerID2.String()); got != 100 {
		t.Fatalf("cursor after subscribe = %d, want 100", got)
	}
	if subs := p.f2.incomingSubscribers(p.groupID, "storeA"); len(subs) != 1 || subs[0] != p.peerID1.String() {
		t.Fatalf("f2's incoming subscribers for storeA = %v, want [%s]", subs, p.peerID1.String())
	}
}

func TestNonSubscribedStoreGetsNoPush(t *testing.T) {
	p := newConnectedFeaturePair(t)

	p.f1.subscribeToPeer(p.f1.registrar(), p.peerID2, p.group, []string{"storeA"})

	if subs := p.f2.incomingSubscribers(p.groupID, "storeB"); len(subs) != 0 {
		t.Fatalf("f2's incoming subscribers for storeB (never subscribed) = %v, want none", subs)
	}

	if err := p.f2.SetValue(p.groupID, "storeB", "k1", "v1"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if rm := p.f1.store.GetMap(p.groupID, "storeB"); len(rm.Entries) != 0 {
		t.Fatalf("f1 received storeB entries it never subscribed to: %+v", rm.Entries)
	}

	// storeA, which it did subscribe to, must receive the push.
	if err := p.f2.SetValue(p.groupID, "storeA", "k2", "v2"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		return p.f1.store.GetMap(p.groupID, "storeA").Entries["k2"].Value == "v2"
	})
}

func TestSubscribeFromNonConfirmedMemberGetsEmptyResponse(t *testing.T) {
	p := newConnectedFeaturePair(t)

	// Revoke peer1's confirmed membership as f2 sees it.
	p.f2.registrar().Peers().Upsert(model.PeerInfo{ID: p.peerID1.String(), GroupNames: nil, Connected: true}, "test")

	if _, err := p.f2.store.ApplyEvent(p.groupID, "storeA", "k1", model.MapEntry{Value: "v1", UpdatedAtUnixMillis: 100}); err != nil {
		t.Fatal(err)
	}

	p.f1.subscribeToPeer(p.f1.registrar(), p.peerID2, p.group, []string{"storeA"})

	if rm := p.f1.store.GetMap(p.groupID, "storeA"); len(rm.Entries) != 0 {
		t.Fatalf("non-member subscribe got data it shouldn't have: %+v", rm.Entries)
	}
	if subs := p.f2.incomingSubscribers(p.groupID, "storeA"); len(subs) != 0 {
		t.Fatalf("f2 registered a subscription for a non-confirmed member: %v", subs)
	}
}

func TestPeerDisconnectedClearsIncomingSubscriptions(t *testing.T) {
	p := newConnectedFeaturePair(t)

	p.f1.subscribeToPeer(p.f1.registrar(), p.peerID2, p.group, []string{"storeA"})
	if subs := p.f2.incomingSubscribers(p.groupID, "storeA"); len(subs) != 1 {
		t.Fatalf("expected f2 to have 1 incoming subscriber before disconnect, got %v", subs)
	}

	p.f2.OnPeerDisconnected(p.peerID1)

	if subs := p.f2.incomingSubscribers(p.groupID, "storeA"); len(subs) != 0 {
		t.Fatalf("expected f2's incoming subscribers to be cleared after disconnect, got %v", subs)
	}
}

func TestRemovingRealmMapsKeyTriggersUnsubscribeAndLocalPurgeOnSubscriber(t *testing.T) {
	p := newConnectedFeaturePair(t)

	if err := p.f1.CreateMap(p.groupID, "mystore", model.RealmMapConfig{}, ""); err != nil {
		t.Fatalf("CreateMap: %v", err)
	}
	if err := p.f1.SetValue(p.groupID, "mystore", "k1", "v1"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}

	// Drive the real convergence path: f2 discovers peer1, subscribes to
	// the system stores, and its reconcile then cascades into subscribing
	// to "mystore" too since it's already a live _realmMaps key.
	p.f2.onPeerAvailable(p.f2.registrar(), p.peerID1, p.group)

	waitFor(t, 2*time.Second, func() bool {
		return p.f2.store.GetMap(p.groupID, "mystore").Entries["k1"].Value == "v1"
	})

	if err := p.f1.DeleteMap(p.groupID, "mystore"); err != nil {
		t.Fatalf("DeleteMap: %v", err)
	}

	for _, s := range p.f1.store.ListSummaries([]model.Group{p.group}) {
		if s.StoreName == "mystore" {
			t.Fatalf("f1 still lists mystore after DeleteMap: %+v", s)
		}
	}

	waitFor(t, 2*time.Second, func() bool {
		for _, s := range p.f2.store.ListSummaries([]model.Group{p.group}) {
			if s.StoreName == "mystore" {
				return false
			}
		}
		return true
	})
}

func testIdentity(t *testing.T) model.Identity {
	t.Helper()
	kp, err := keypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate: %v", err)
	}
	return model.Identity{Name: "identity1", KeyPair: kp}
}

func TestEncryptedMapRoundTrip(t *testing.T) {
	identity := testIdentity(t)
	// f1 does not hold the identity; f2 does.
	p := newConnectedFeaturePairWithIdentities(t, nil, []model.Identity{identity})

	if err := p.f1.CreateMap(p.groupID, "secrets", model.RealmMapConfig{}, identity.KeyPair.ID); err != nil {
		t.Fatalf("CreateMap: %v", err)
	}

	// f1 (no identity) cannot write meaningful values to an encrypted map.
	if err := p.f1.SetValue(p.groupID, "secrets", "apiKey", "s3cr3t"); err == nil {
		t.Fatal("expected SetValue to fail without the target identity available locally")
	}

	// Drive f2's subscribe to f1 (see
	// TestRemovingRealmMapsKeyTriggersUnsubscribeAndLocalPurgeOnSubscriber)
	// so f2 learns the map's (unencrypted) _realmMaps config -- including
	// that it's encrypted -- before writing to it.
	p.f2.onPeerAvailable(p.f2.registrar(), p.peerID1, p.group)
	waitFor(t, 2*time.Second, func() bool {
		return p.f2.configForStore(p.groupID, "secrets").Encryption != nil
	})

	// f2 (holds identity) can write.
	if err := p.f2.SetValue(p.groupID, "secrets", "apiKey", "s3cr3t"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}

	// Drive f1's subscribe to f2 so the (opaque, encrypted) entry actually
	// replicates back to f1.
	p.f1.onPeerAvailable(p.f1.registrar(), p.peerID2, p.group)
	waitFor(t, 2*time.Second, func() bool {
		return len(p.f1.store.GetMap(p.groupID, "secrets").Entries) > 0
	})

	// f1 sees the map exists (encrypted) but can't decrypt it.
	rm1, encrypted1, available1 := p.f1.GetMap(p.groupID, "secrets")
	if !encrypted1 {
		t.Fatal("f1: expected map to be reported as encrypted")
	}
	if available1 {
		t.Fatal("f1: expected map to be unavailable (no identity)")
	}
	if len(rm1.Entries) != 0 {
		t.Fatalf("f1: expected no readable entries, got %+v", rm1.Entries)
	}

	// f1's raw local copy must be opaque: neither the real key nor the real
	// value appear anywhere in storage.
	raw := p.f1.store.GetMap(p.groupID, "secrets")
	if len(raw.Entries) == 0 {
		t.Fatal("f1: expected the (opaque) entry to have replicated")
	}
	for k, e := range raw.Entries {
		if k == "apiKey" {
			t.Fatalf("real key %q leaked into storage key", k)
		}
		if e.Value == "s3cr3t" {
			t.Fatalf("real value leaked into storage: %+v", e)
		}
	}

	// f2 (holds identity) can decrypt.
	rm2, encrypted2, available2 := p.f2.GetMap(p.groupID, "secrets")
	if !encrypted2 || !available2 {
		t.Fatalf("f2: expected encrypted=true available=true, got encrypted=%v available=%v", encrypted2, available2)
	}
	if got := rm2.Entries["apiKey"].Value; got != "s3cr3t" {
		t.Fatalf("f2: decrypted value = %q, want s3cr3t", got)
	}
}

func TestEncryptedMapRejectsTamperedIdentitySignature(t *testing.T) {
	identity := testIdentity(t)
	p := newConnectedFeaturePairWithIdentities(t, nil, []model.Identity{identity})

	if err := p.f2.CreateMap(p.groupID, "secrets", model.RealmMapConfig{}, identity.KeyPair.ID); err != nil {
		t.Fatalf("CreateMap: %v", err)
	}
	if err := p.f2.SetValue(p.groupID, "secrets", "apiKey", "s3cr3t"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}

	rm := p.f2.store.GetMap(p.groupID, "secrets")
	var storageKey string
	var entry model.MapEntry
	for k, e := range rm.Entries {
		storageKey, entry = k, e
	}
	if storageKey == "" {
		t.Fatal("expected exactly one entry")
	}
	entry.IdentitySignature = "dGFtcGVyZWQ=" // "tampered", base64
	if _, err := p.f2.store.ApplyEvent(p.groupID, "secrets", storageKey, entry); err != nil {
		t.Fatal(err)
	}

	rm2, encrypted, available := p.f2.GetMap(p.groupID, "secrets")
	if !encrypted || !available {
		t.Fatalf("expected encrypted=true available=true, got encrypted=%v available=%v", encrypted, available)
	}
	if len(rm2.Entries) != 0 {
		t.Fatalf("expected the tampered entry to be dropped, got %+v", rm2.Entries)
	}
}
