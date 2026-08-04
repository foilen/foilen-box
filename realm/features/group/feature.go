// Package group is the "common/group" Realm feature: lets this peer push
// one of its Groups directly to another peer, which imports it automatically
// (subject to the push permission).
package group

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	realm "foilen-realm"
	realmkeypair "foilen-realm/keypair"
	"foilen-realm/model"
)

const (
	// PushProtocolID delivers a group's name and private key in one shot;
	// the connection is already libp2p-authenticated, so nothing further is signed.
	PushProtocolID = protocol.ID("/foilen-box/group-push/1.0.0")
	ioTimeout      = 10 * time.Second
	maxBytes       = 16 * 1024

	// FeatureName is this feature's namespace, used to prefix its actions.
	FeatureName = "common/group"

	// ActionPush gates accepting a group pushed from another peer.
	ActionPush model.PermissionAction = FeatureName + "/push"
)

// pushRequest is the wire payload for PushProtocolID.
type pushRequest struct {
	Name             string `json:"name"`
	PrivateKeyBase64 string `json:"privateKeyBase64"`
}

type pushAck struct {
	Imported bool   `json:"imported"`
	Error    string `json:"error,omitempty"`
}

// Feature implements realm.Feature. onReceive persists an accepted group
// into app config — Features have no write access to Config, so this
// callback is how app-specific behavior plugs in.
type Feature struct {
	mu        sync.Mutex
	reg       *realm.Registrar
	onReceive func(name string, kp model.KeyPair) error
}

// New builds the group Feature. onReceive is called synchronously, from
// the stream handler goroutine, for every accepted push.
func New(onReceive func(name string, kp model.KeyPair) error) *Feature {
	return &Feature{onReceive: onReceive}
}

func (f *Feature) registrar() *realm.Registrar {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reg
}

// peerLabel resolves id to "hostname (description) [shortid]" via the
// registered engine's peer store, or just the bracketed short id if the
// feature isn't registered yet or the peer isn't known.
func (f *Feature) peerLabel(id string) string {
	if reg := f.registrar(); reg != nil {
		return reg.Peers().Label(id)
	}
	return model.ShortID(id)
}

func (f *Feature) Name() string { return FeatureName }

func (f *Feature) Actions() []model.PermissionAction {
	return []model.PermissionAction{ActionPush}
}

func (f *Feature) RegisterHandlers(reg *realm.Registrar) {
	f.mu.Lock()
	f.reg = reg
	f.mu.Unlock()
	reg.SetStreamHandler(PushProtocolID, f.handlePushStream(reg))
}

// Push sends name/kp to peer "to", which imports it automatically if it has
// granted this peer (or one of its groups) the push action.
func (f *Feature) Push(to, name string, kp model.KeyPair) (err error) {
	defer func() {
		if err != nil {
			log.Printf("realm group: push of group %q to %s failed: %v", name, f.peerLabel(to), err)
		} else {
			log.Printf("realm group: pushed group %q to %s", name, f.peerLabel(to))
		}
	}()

	reg := f.registrar()
	if reg == nil {
		return fmt.Errorf("realm group: not registered on an engine")
	}
	h := reg.Host()
	ctx := reg.Context()
	if h == nil || ctx == nil {
		return fmt.Errorf("realm group: not running")
	}

	pid, err := peer.Decode(to)
	if err != nil {
		return fmt.Errorf("realm group: invalid peer id %q: %w", to, err)
	}
	if err := reg.EnsureConnected(ctx, pid); err != nil {
		return fmt.Errorf("realm group: peer %s unreachable to push group: %w", to, err)
	}

	streamCtx, cancel := context.WithTimeout(ctx, ioTimeout)
	defer cancel()
	s, err := h.NewStream(streamCtx, pid, PushProtocolID)
	if err != nil {
		return fmt.Errorf("realm group: peer %s unreachable to push group: %w", to, err)
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(ioTimeout))

	if err := json.NewEncoder(s).Encode(pushRequest{Name: name, PrivateKeyBase64: kp.PrivateKeyBase64}); err != nil {
		return fmt.Errorf("realm group: failed to send group to %s: %w", to, err)
	}

	var ack pushAck
	if err := json.NewDecoder(io.LimitReader(s, maxBytes)).Decode(&ack); err != nil {
		return fmt.Errorf("realm group: failed to read ack from %s: %w", to, err)
	}
	if !ack.Imported {
		return fmt.Errorf("realm group: %s refused group %q: %s", to, name, ack.Error)
	}
	return nil
}

// handlePushStream is the libp2p stream handler for PushProtocolID: check
// permission, re-derive the keypair's id from the received private key
// (never trusted from the wire), hand it to onReceive, and ack the outcome.
func (f *Feature) handlePushStream(reg *realm.Registrar) network.StreamHandler {
	return func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(ioTimeout))

		remote := s.Conn().RemotePeer()

		var req pushRequest
		if err := json.NewDecoder(io.LimitReader(s, maxBytes)).Decode(&req); err != nil {
			log.Printf("realm group: failed to decode pushed group from %s: %v", f.peerLabel(remote.String()), err)
			return
		}

		if !reg.IsAllowed(remote, ActionPush) {
			log.Printf("realm group: pushed group from %s rejected: no permission", f.peerLabel(remote.String()))
			_ = json.NewEncoder(s).Encode(pushAck{Imported: false, Error: "not allowed"})
			return
		}

		kp, err := realmkeypair.Import(req.PrivateKeyBase64)
		if err != nil {
			log.Printf("realm group: pushed group from %s has an invalid key: %v", f.peerLabel(remote.String()), err)
			_ = json.NewEncoder(s).Encode(pushAck{Imported: false, Error: "invalid key"})
			return
		}

		f.mu.Lock()
		onReceive := f.onReceive
		f.mu.Unlock()

		if onReceive == nil {
			_ = json.NewEncoder(s).Encode(pushAck{Imported: false, Error: "not accepting groups"})
			return
		}
		if err := onReceive(req.Name, kp); err != nil {
			log.Printf("realm group: failed to import group %q pushed from %s: %v", req.Name, f.peerLabel(remote.String()), err)
			_ = json.NewEncoder(s).Encode(pushAck{Imported: false, Error: err.Error()})
			return
		}

		log.Printf("realm group: imported group %q pushed from %s", req.Name, f.peerLabel(remote.String()))
		_ = json.NewEncoder(s).Encode(pushAck{Imported: true})
	}
}
