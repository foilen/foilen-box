package realm

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"foilen-realm/keypair"
	"foilen-realm/model"
)

// groupChallengeProtocolID proves a peer actually holds a group's private
// key (vs. merely claiming its public id over identifyProtocolID) before
// adding it to confirmed GroupNames.
const groupChallengeProtocolID = "/foilen-box/group-challenge/1.0.0"

// groupChallengeRequest asks responderID to prove membership in the group
// identified by GroupID (its public group id, model.Group.KeyPair.ID) by
// signing challengeHash(this request) with the group's private key.
type groupChallengeRequest struct {
	RequesterID string `json:"requesterId"`
	ResponderID string `json:"responderId"`
	GroupID     string `json:"groupId"`
	Nonce       string `json:"nonce"`
}

// groupChallengeResponse carries the base64-encoded signature over
// challengeHash(the request being answered).
type groupChallengeResponse struct {
	SignatureBase64 string `json:"signatureBase64"`
}

// challengeHash is what a group-challenge response signs: it binds both
// peer ids, the group being challenged, and a fresh nonce, so a captured
// signature can't be replayed against a different peer pair or group, or
// reused for a later challenge.
func challengeHash(req groupChallengeRequest) []byte {
	sum := md5.Sum([]byte(req.RequesterID + "|" + req.ResponderID + "|" + req.GroupID + "|" + req.Nonce))
	return sum[:]
}

// challengeGroup asks remote to prove it holds group's private key and, if
// verified, records the group as confirmed for remote. Membership claimed
// via identifyPayload isn't trusted until this succeeds.
func (e *Engine) challengeGroup(remote peer.ID, group model.Group) {
	e.mu.Lock()
	h := e.host
	e.mu.Unlock()
	if h == nil {
		return
	}

	req := groupChallengeRequest{
		RequesterID: h.ID().String(),
		ResponderID: remote.String(),
		GroupID:     group.KeyPair.ID,
		Nonce:       uuid.NewString(),
	}

	streamCtx, cancel := context.WithTimeout(e.Context(), identifyIOTimeout)
	defer cancel()
	s, err := h.NewStream(streamCtx, remote, groupChallengeProtocolID)
	if err != nil {
		log.Printf("realm engine: failed to open group-challenge stream to %s: %v", e.peers.Label(remote.String()), err)
		return
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(identifyIOTimeout))

	if err := json.NewEncoder(s).Encode(req); err != nil {
		log.Printf("realm engine: failed to send group-challenge to %s: %v", e.peers.Label(remote.String()), err)
		return
	}

	var resp groupChallengeResponse
	if err := json.NewDecoder(io.LimitReader(s, identifyMaxBytes)).Decode(&resp); err != nil {
		log.Printf("realm engine: failed to read group-challenge response from %s: %v", e.peers.Label(remote.String()), err)
		return
	}

	if !verifyGroupChallenge(group, req, resp.SignatureBase64) {
		log.Printf("realm engine: peer %s failed group-challenge for group %q", e.peers.Label(remote.String()), group.Name)
		return
	}

	e.peers.AddGroupName(remote.String(), group.Name)
	log.Printf("realm engine: peer %s confirmed membership in group %q", e.peers.Label(remote.String()), group.Name)

	reg := &Registrar{e: e}
	for _, gh := range e.groupConfirmedHooks {
		go gh.OnGroupConfirmed(reg, remote, group)
	}
}

// handleGroupChallengeStream answers a group-challenge by signing
// challengeHash(req) if we hold the requested group's private key. Doesn't
// touch our own peer store — answering says nothing about the requester's membership.
func (e *Engine) handleGroupChallengeStream(s network.Stream) {
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(identifyIOTimeout))

	var req groupChallengeRequest
	if err := json.NewDecoder(io.LimitReader(s, identifyMaxBytes)).Decode(&req); err != nil {
		log.Printf("realm engine: failed to read group-challenge request: %v", err)
		return
	}

	e.mu.Lock()
	h := e.host
	groups := e.cfg.Groups
	e.mu.Unlock()
	if h == nil {
		return
	}
	if req.ResponderID != h.ID().String() {
		log.Printf("realm engine: group-challenge request meant for %s, not us; ignoring", model.ShortID(req.ResponderID))
		return
	}

	group, ok := findGroupByID(groups, req.GroupID)
	if !ok {
		log.Printf("realm engine: group-challenge for unknown group %s rejected", model.ShortID(req.GroupID))
		return
	}

	priv, err := keypair.PrivateKey(group.KeyPair)
	if err != nil {
		log.Printf("realm engine: failed to load private key for group %q: %v", group.Name, err)
		return
	}
	sig, err := priv.Sign(challengeHash(req))
	if err != nil {
		log.Printf("realm engine: failed to sign group-challenge for group %q: %v", group.Name, err)
		return
	}

	resp := groupChallengeResponse{SignatureBase64: base64.StdEncoding.EncodeToString(sig)}
	if err := json.NewEncoder(s).Encode(resp); err != nil {
		log.Printf("realm engine: failed to send group-challenge response: %v", err)
	}
}

// verifyGroupChallenge reports whether sigBase64 is a valid signature, made
// with group's private key, over challengeHash(req).
func verifyGroupChallenge(group model.Group, req groupChallengeRequest, sigBase64 string) bool {
	sig, err := base64.StdEncoding.DecodeString(sigBase64)
	if err != nil {
		return false
	}
	priv, err := keypair.PrivateKey(group.KeyPair)
	if err != nil {
		return false
	}
	ok, err := priv.GetPublic().Verify(challengeHash(req), sig)
	return err == nil && ok
}
