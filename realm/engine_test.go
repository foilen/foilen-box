package realm

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"

	"foilen-realm/keypair"
	"foilen-realm/model"
	"foilen-realm/peers"
)

func newTestEngine(t *testing.T, dir string) *Engine {
	t.Helper()
	peerStore, err := peers.New(dir)
	if err != nil {
		t.Fatalf("peers.New() error = %v", err)
	}
	return New(dir, peerStore)
}

func TestGroupTopicRotatesDailyAndIsDeterministic(t *testing.T) {
	group := model.Group{Name: "family", KeyPair: model.KeyPair{PrivateKeyBase64: "secret"}}

	a := groupTopic(group, "2026-07-01")
	b := groupTopic(group, "2026-07-01")
	c := groupTopic(group, "2026-07-02")

	if a != b {
		t.Errorf("groupTopic() not deterministic: %q != %q", a, b)
	}
	if a == c {
		t.Errorf("groupTopic() did not rotate across days: %q == %q", a, c)
	}
}

func TestGroupTopicDiffersPerGroup(t *testing.T) {
	groupA := model.Group{Name: "family", KeyPair: model.KeyPair{PrivateKeyBase64: "secretA"}}
	groupB := model.Group{Name: "friends", KeyPair: model.KeyPair{PrivateKeyBase64: "secretB"}}

	if groupTopic(groupA, "2026-07-01") == groupTopic(groupB, "2026-07-01") {
		t.Error("groupTopic() collided across distinct groups")
	}
}

func TestStartWithoutPeerIDIsNoop(t *testing.T) {
	dir := t.TempDir()
	e := newTestEngine(t, dir)

	if err := e.Start(model.Config{}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if e.Running() {
		t.Error("Running() = true after Start() with no peer id")
	}
}

func TestStartStopLifecycle(t *testing.T) {
	dir := t.TempDir()
	kp, err := keypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate() error = %v", err)
	}
	e := newTestEngine(t, dir)

	cfg := model.Config{PeerID: kp, DhtMode: model.DhtModeClient, EnableMdns: true, EnableDht: true}
	if err := e.Start(cfg); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !e.Running() {
		t.Fatal("Running() = false after Start()")
	}
	if e.HostID() != kp.ID {
		t.Errorf("HostID() = %q, want %q", e.HostID(), kp.ID)
	}

	// Starting again while already running must be a no-op, not an error.
	if err := e.Start(cfg); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}

	e.Stop()
	if e.Running() {
		t.Error("Running() = true after Stop()")
	}

	// Stopping twice must be safe.
	e.Stop()
}

func TestRestartWithNewGroup(t *testing.T) {
	dir := t.TempDir()
	kp, err := keypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate() error = %v", err)
	}
	e := newTestEngine(t, dir)
	defer e.Stop()

	cfg := model.Config{PeerID: kp, DhtMode: model.DhtModeClient, EnableMdns: false, EnableDht: false}
	if err := e.Restart(cfg); err != nil {
		t.Fatalf("Restart() error = %v", err)
	}
	firstID := e.HostID()

	groupKP, err := keypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate() error = %v", err)
	}
	cfg.Groups = []model.Group{{Name: "family", KeyPair: groupKP}}
	if err := e.Restart(cfg); err != nil {
		t.Fatalf("Restart() with group error = %v", err)
	}
	if e.HostID() != firstID {
		t.Errorf("HostID() changed across Restart(): %q != %q", e.HostID(), firstID)
	}
}

func TestReconcilePrunesGroupNameFromKnownPeersOnGroupDeletion(t *testing.T) {
	dir := t.TempDir()
	e := newTestEngine(t, dir)

	e.peers.Upsert(model.PeerInfo{ID: "peerA", GroupNames: []string{"family", "work"}}, "")
	e.peers.Upsert(model.PeerInfo{ID: "peerB", GroupNames: []string{"work"}}, "")

	e.mu.Lock()
	e.cfg = model.Config{Groups: []model.Group{
		{Name: "family", KeyPair: model.KeyPair{PrivateKeyBase64: "a"}},
		{Name: "work", KeyPair: model.KeyPair{PrivateKeyBase64: "b"}},
	}}
	e.mu.Unlock()

	if err := e.Reconcile(model.Config{Groups: []model.Group{
		{Name: "family", KeyPair: model.KeyPair{PrivateKeyBase64: "a"}},
	}}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	peerA, _ := e.peers.Get("peerA")
	if len(peerA.GroupNames) != 1 || peerA.GroupNames[0] != "family" {
		t.Errorf("peerA.GroupNames = %v, want [family]", peerA.GroupNames)
	}
	peerB, _ := e.peers.Get("peerB")
	if len(peerB.GroupNames) != 0 {
		t.Errorf("peerB.GroupNames = %v, want empty", peerB.GroupNames)
	}
}

func TestAddedGroupKeys(t *testing.T) {
	a := model.Group{Name: "family", KeyPair: model.KeyPair{PrivateKeyBase64: "a"}}
	b := model.Group{Name: "work", KeyPair: model.KeyPair{PrivateKeyBase64: "b"}}
	renamedA := model.Group{Name: "kin", KeyPair: model.KeyPair{PrivateKeyBase64: "a"}}

	added := addedGroupKeys([]model.Group{a}, []model.Group{renamedA, b})
	if len(added) != 1 || added[0].Name != "work" {
		t.Errorf("addedGroupKeys() = %v, want just [work] (a renamed group must not count as added)", added)
	}

	if got := addedGroupKeys([]model.Group{a, b}, []model.Group{a}); len(got) != 0 {
		t.Errorf("addedGroupKeys() = %v, want none for a group removal", got)
	}
}

// TestHandleFoundPeerDoesNotGrantGroupMembership guards against discovery
// alone granting group membership — that must only come from a passed
// group-challenge (group_challenge.go).
func TestHandleFoundPeerDoesNotGrantGroupMembership(t *testing.T) {
	dir := t.TempDir()
	kp, err := keypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate() error = %v", err)
	}
	e := newTestEngine(t, dir)
	defer e.Stop()

	if err := e.Start(model.Config{PeerID: kp}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	strangerKP, err := keypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate() error = %v", err)
	}
	strangerID, err := peer.Decode(strangerKP.ID)
	if err != nil {
		t.Fatalf("peer.Decode() error = %v", err)
	}

	e.handleFoundPeer(peer.AddrInfo{ID: strangerID}, "family", "mdns")

	info, ok := e.peers.Get(strangerID.String())
	if !ok {
		t.Fatal("expected discovered peer to be recorded")
	}
	if len(info.GroupNames) != 0 {
		t.Errorf("GroupNames = %v, want empty: discovery alone must not grant group membership", info.GroupNames)
	}
}

func TestReconcileAddingGroupKeepsHostRunning(t *testing.T) {
	dir := t.TempDir()
	kp, err := keypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate() error = %v", err)
	}
	e := newTestEngine(t, dir)
	defer e.Stop()

	cfg := model.Config{PeerID: kp, DhtMode: model.DhtModeClient, EnableMdns: true, EnableDht: false}
	if err := e.Start(cfg); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	firstID := e.HostID()

	groupKP, err := keypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate() error = %v", err)
	}
	cfg.Groups = []model.Group{{Name: "family", KeyPair: groupKP}}
	if err := e.Reconcile(cfg); err != nil {
		t.Fatalf("Reconcile() with new group error = %v", err)
	}
	if !e.Running() {
		t.Fatal("Running() = false after Reconcile()")
	}
	if e.HostID() != firstID {
		t.Errorf("HostID() changed across Reconcile(): %q != %q", e.HostID(), firstID)
	}
	if _, ok := e.mdnsSvcs[groupKey(cfg.Groups[0])]; !ok {
		t.Error("expected mDNS service to be started for newly added group")
	}

	// Reconciling with the same config again must be a no-op, not an error.
	if err := e.Reconcile(cfg); err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}
	if e.HostID() != firstID {
		t.Errorf("HostID() changed across no-op Reconcile(): %q != %q", e.HostID(), firstID)
	}

	// Removing the group again must close its mDNS service without
	// restarting the host.
	cfg.Groups = nil
	if err := e.Reconcile(cfg); err != nil {
		t.Fatalf("Reconcile() removing group error = %v", err)
	}
	if e.HostID() != firstID {
		t.Errorf("HostID() changed across group-removal Reconcile(): %q != %q", e.HostID(), firstID)
	}
	if len(e.mdnsSvcs) != 0 {
		t.Errorf("expected mDNS service map to be empty after group removal, got %d entries", len(e.mdnsSvcs))
	}
}
