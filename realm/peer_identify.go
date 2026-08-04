package realm

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	// identifyProtocolID: engine-owned exchange of hostname/description/claimed
	// groups, run automatically on connect (unlike the on-demand spec protocol).
	identifyProtocolID = "/foilen-box/identify/1.0.0"
	identifyIOTimeout  = 10 * time.Second
	identifyMaxBytes   = 4 * 1024
)

// identifyPayload is exchanged both ways over identifyProtocolID. GroupIDs
// are self-claimed — receiving one only tells the recipient which groups to
// challenge (challengeGroup), it doesn't grant membership. RelayServiceEnabled
// feeds Engine.connectedRelayPeers. Addresses is recorded as the peer store's
// "announce" fallback dial source for reconnecting after discovery drops off.
type identifyPayload struct {
	Hostname            string   `json:"hostname"`
	Description         string   `json:"description"`
	GroupIDs            []string `json:"groupIds"`
	RelayServiceEnabled bool     `json:"relayServiceEnabled"`
	Version             string   `json:"version"`
	Addresses           []string `json:"addresses"`
}

// handleIdentifyStream answers a connected, known peer's identify request:
// it reads the requester's own identifyPayload first, replies with ours,
// then processes the requester's claimed groups. Only peers already tracked
// as known (surfaced by our own discovery) are answered.
func (e *Engine) handleIdentifyStream(s network.Stream) {
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(identifyIOTimeout))

	remote := s.Conn().RemotePeer()
	if _, ok := e.peers.Get(remote.String()); !ok {
		log.Printf("realm engine: identify request from %s rejected: not a known peer", e.peers.Label(remote.String()))
		return
	}

	var reqPayload identifyPayload
	if err := json.NewDecoder(io.LimitReader(s, identifyMaxBytes)).Decode(&reqPayload); err != nil {
		log.Printf("realm engine: failed to read identify payload from %s: %v", e.peers.Label(remote.String()), err)
		return
	}
	e.peers.SetAnnouncedAddresses(remote.String(), reqPayload.Addresses)

	e.mu.Lock()
	h := e.host
	e.mu.Unlock()
	if h == nil {
		return
	}
	if err := json.NewEncoder(s).Encode(e.selfIdentifyPayload(h)); err != nil {
		log.Printf("realm engine: failed to send identify payload to %s: %v", e.peers.Label(remote.String()), err)
		return
	}

	e.processClaimedGroups(remote, reqPayload.GroupIDs)
}

// fetchPeerIdentity dials id over identifyProtocolID, exchanges
// identifyPayloads, records the response, and processes the peer's claimed
// groups. Called on connect and whenever a group is added to our config.
func (e *Engine) fetchPeerIdentity(id peer.ID) {
	e.mu.Lock()
	h := e.host
	e.mu.Unlock()
	if h == nil {
		return
	}

	streamCtx, cancel := context.WithTimeout(e.Context(), identifyIOTimeout)
	defer cancel()
	s, err := h.NewStream(streamCtx, id, identifyProtocolID)
	if err != nil {
		log.Printf("realm engine: failed to open identify stream to %s: %v", e.peers.Label(id.String()), err)
		return
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(identifyIOTimeout))

	if err := json.NewEncoder(s).Encode(e.selfIdentifyPayload(h)); err != nil {
		log.Printf("realm engine: failed to send identify payload to %s: %v", e.peers.Label(id.String()), err)
		return
	}

	var payload identifyPayload
	if err := json.NewDecoder(io.LimitReader(s, identifyMaxBytes)).Decode(&payload); err != nil {
		log.Printf("realm engine: failed to read identify payload from %s: %v", e.peers.Label(id.String()), err)
		return
	}
	e.peers.SetHostnameDescription(id.String(), payload.Hostname, payload.Description, payload.RelayServiceEnabled, payload.Version)
	e.peers.SetAnnouncedAddresses(id.String(), payload.Addresses)

	e.processClaimedGroups(id, payload.GroupIDs)
}

// selfIdentifyPayload builds our own identifyPayload from current config and
// h's listen addrs.
func (e *Engine) selfIdentifyPayload(h host.Host) identifyPayload {
	e.mu.Lock()
	hostname := e.hostnameOverride
	description := e.cfg.Description
	groups := e.cfg.Groups
	relayServiceEnabled := e.cfg.EnableRelayService
	version := e.appVersion
	e.mu.Unlock()

	if hostname == "" {
		var err error
		hostname, err = os.Hostname()
		if err != nil {
			log.Printf("realm engine: failed to read hostname: %v", err)
		}
	}

	groupIDs := make([]string, 0, len(groups))
	for _, g := range groups {
		groupIDs = append(groupIDs, g.KeyPair.ID)
	}
	return identifyPayload{
		Hostname:            hostname,
		Description:         description,
		GroupIDs:            groupIDs,
		RelayServiceEnabled: relayServiceEnabled,
		Version:             version,
		Addresses:           addrsToStrings(h.Addrs()),
	}
}

// processClaimedGroups issues a group-challenge (see challengeGroup) toward
// remote for each of its claimed group ids that matches one of our own
// configured groups and isn't already confirmed for that peer.
func (e *Engine) processClaimedGroups(remote peer.ID, claimedGroupIDs []string) {
	e.mu.Lock()
	groups := e.cfg.Groups
	e.mu.Unlock()

	info, _ := e.peers.Get(remote.String())

	for _, gid := range claimedGroupIDs {
		group, ok := findGroupByID(groups, gid)
		if !ok {
			continue
		}
		if alreadyConfirmed(info.GroupNames, group.Name) {
			continue
		}
		go e.challengeGroup(remote, group)
	}
}

// alreadyConfirmed reports whether groupName is already in groupNames.
func alreadyConfirmed(groupNames []string, groupName string) bool {
	for _, gn := range groupNames {
		if gn == groupName {
			return true
		}
	}
	return false
}
