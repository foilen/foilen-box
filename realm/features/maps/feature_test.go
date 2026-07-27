package maps

import (
	"testing"

	"foilen-realm/keypair"
	"foilen-realm/model"
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
	ev := model.MapEvent{ScopeID: group.KeyPair.ID, StoreName: "s", Key: "k", Value: "v", UpdatedAtUnixMillis: 42, OriginPeerID: "peer1"}

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
	ev := model.MapEvent{ScopeID: group.KeyPair.ID, StoreName: "s", Key: "k", Value: "v", UpdatedAtUnixMillis: 42, OriginPeerID: "peer1"}

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
	ev := model.MapEvent{ScopeID: group.KeyPair.ID, StoreName: "s", Key: "k", Value: "v", UpdatedAtUnixMillis: 42}

	env, err := signEvent(group, ev)
	if err != nil {
		t.Fatalf("signEvent: %v", err)
	}
	if verifyEvent(otherGroup, env) {
		t.Fatal("verifyEvent should reject a signature made by a different group's key")
	}
}
