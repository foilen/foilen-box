package realm

import (
	"encoding/base64"
	"testing"

	"foilen-realm/keypair"
	"foilen-realm/model"
)

func TestGroupChallengeSignAndVerify(t *testing.T) {
	groupKP, err := keypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate() error = %v", err)
	}
	group := model.Group{Name: "family", KeyPair: groupKP}

	req := groupChallengeRequest{
		RequesterID: "peerA",
		ResponderID: "peerB",
		GroupID:     group.KeyPair.ID,
		Nonce:       "nonce-1",
	}

	priv, err := keypair.PrivateKey(group.KeyPair)
	if err != nil {
		t.Fatalf("keypair.PrivateKey() error = %v", err)
	}
	sig, err := priv.Sign(challengeHash(req))
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	sigBase64 := base64.StdEncoding.EncodeToString(sig)

	if !verifyGroupChallenge(group, req, sigBase64) {
		t.Error("verifyGroupChallenge() = false, want true for a genuine signature")
	}
}

func TestGroupChallengeRejectsWrongGroup(t *testing.T) {
	groupA, err := keypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate() error = %v", err)
	}
	groupB, err := keypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate() error = %v", err)
	}

	req := groupChallengeRequest{RequesterID: "peerA", ResponderID: "peerB", GroupID: groupA.ID, Nonce: "nonce-1"}
	priv, err := keypair.PrivateKey(groupA)
	if err != nil {
		t.Fatalf("keypair.PrivateKey() error = %v", err)
	}
	sig, err := priv.Sign(challengeHash(req))
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}

	// Signature was made with groupA's key but is presented as proof of
	// membership in groupB: verification against groupB must fail.
	if verifyGroupChallenge(model.Group{Name: "b", KeyPair: groupB}, req, base64.StdEncoding.EncodeToString(sig)) {
		t.Error("verifyGroupChallenge() = true for a signature made with a different group's key")
	}
}

func TestGroupChallengeRejectsTamperedNonce(t *testing.T) {
	groupKP, err := keypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate() error = %v", err)
	}
	group := model.Group{Name: "family", KeyPair: groupKP}

	req := groupChallengeRequest{RequesterID: "peerA", ResponderID: "peerB", GroupID: group.KeyPair.ID, Nonce: "nonce-1"}
	priv, err := keypair.PrivateKey(group.KeyPair)
	if err != nil {
		t.Fatalf("keypair.PrivateKey() error = %v", err)
	}
	sig, err := priv.Sign(challengeHash(req))
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}

	replayed := req
	replayed.Nonce = "nonce-2"
	if verifyGroupChallenge(group, replayed, base64.StdEncoding.EncodeToString(sig)) {
		t.Error("verifyGroupChallenge() = true for a signature replayed against a different nonce")
	}
}

func TestFindGroupByID(t *testing.T) {
	groupKP, err := keypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate() error = %v", err)
	}
	groups := []model.Group{{Name: "family", KeyPair: groupKP}}

	if _, ok := findGroupByID(groups, groupKP.ID); !ok {
		t.Error("findGroupByID() = false, want true for a known group id")
	}
	if _, ok := findGroupByID(groups, "not-a-real-id"); ok {
		t.Error("findGroupByID() = true, want false for an unknown group id")
	}
}
