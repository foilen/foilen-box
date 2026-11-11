// TODO - Human verify
package realm

import (
	"testing"
	"time"

	"foilen-realm/keypair"
	"foilen-realm/model"
)

func TestUnknownInboundPeerIsRecordedAndGroupConfirmed(t *testing.T) {
	groupKP, err := keypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate() error = %v", err)
	}
	group := model.Group{Name: "simon-shared", KeyPair: groupKP}

	kpA, err := keypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate() error = %v", err)
	}
	kpB, err := keypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate() error = %v", err)
	}

	// A is the new peer: it knows B's addresses, but B has never discovered A
	eA := newTestEngine(t, t.TempDir())
	defer eA.Stop()
	eB := newTestEngine(t, t.TempDir())
	defer eB.Stop()

	cfg := func(kp model.KeyPair) model.Config {
		return model.Config{PeerID: kp, DhtMode: model.DhtModeClient, EnableUdpBroadcast: false, EnableDht: false, RealmListenPort: 0, Groups: []model.Group{group}}
	}
	if err := eA.Start(cfg(kpA)); err != nil {
		t.Fatalf("A Start() error = %v", err)
	}
	if err := eB.Start(cfg(kpB)); err != nil {
		t.Fatalf("B Start() error = %v", err)
	}

	if err := eA.AddPeer(kpB.ID, eB.Addrs(), "import"); err != nil {
		t.Fatalf("A AddPeer(B) error = %v", err)
	}
	if _, ok := eB.peers.Get(kpA.ID); ok {
		t.Fatal("B must not know A before the connection")
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ia, _ := eA.peers.Get(kpB.ID)
		ib, _ := eB.peers.Get(kpA.ID)
		if len(ia.GroupNames) > 0 && len(ib.GroupNames) > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	ia, _ := eA.peers.Get(kpB.ID)
	ib, _ := eB.peers.Get(kpA.ID)
	t.Errorf("group not confirmed on both sides: A->B groups %v, B->A groups %v (B knows A: %v)", ia.GroupNames, ib.GroupNames, ib.ID != "")
}
