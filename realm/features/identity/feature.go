// Package identity is the "common/identity" Realm feature: lets this peer
// push one of its standalone Identity keypairs directly to another peer,
// which imports it automatically (subject to the push permission).
package identity

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
	// PushProtocolID delivers an identity's name and private key to a peer
	// in one shot; the connection is already libp2p-authenticated, so
	// nothing further is signed.
	PushProtocolID = protocol.ID("/foilen-box/identity-push/1.0.0")
	ioTimeout      = 10 * time.Second
	maxBytes       = 16 * 1024

	// FeatureName is this feature's namespace, used to prefix its actions.
	FeatureName = "common/identity"

	// ActionPush gates accepting an identity pushed from another peer.
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

// Feature implements realm.Feature. onReceive is called whenever this peer
// accepts a pushed identity, so the application can persist it into its own
// config (e.g. Config.Identities) — a Feature has no write access to the
// shared Config itself, so this callback is how app-specific behavior is
// plugged in, the same way spec.Feature takes a TextProvider.
type Feature struct {
	mu        sync.Mutex
	reg       *realm.Registrar
	onReceive func(name string, kp model.KeyPair) error
}

// New builds the identity Feature. onReceive is called (synchronously, from
// the incoming stream's handler goroutine) with the name and keypair of
// every identity this peer accepts a push for.
func New(onReceive func(name string, kp model.KeyPair) error) *Feature {
	return &Feature{onReceive: onReceive}
}

func (f *Feature) registrar() *realm.Registrar {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reg
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
			log.Printf("realm identity: push of identity %q to %s failed: %v", name, to, err)
		} else {
			log.Printf("realm identity: pushed identity %q to %s", name, to)
		}
	}()

	reg := f.registrar()
	if reg == nil {
		return fmt.Errorf("realm identity: not registered on an engine")
	}
	h := reg.Host()
	ctx := reg.Context()
	if h == nil || ctx == nil {
		return fmt.Errorf("realm identity: not running")
	}

	pid, err := peer.Decode(to)
	if err != nil {
		return fmt.Errorf("realm identity: invalid peer id %q: %w", to, err)
	}
	if err := reg.EnsureConnected(ctx, pid); err != nil {
		return fmt.Errorf("realm identity: peer %s unreachable to push identity: %w", to, err)
	}

	streamCtx, cancel := context.WithTimeout(ctx, ioTimeout)
	defer cancel()
	s, err := h.NewStream(streamCtx, pid, PushProtocolID)
	if err != nil {
		return fmt.Errorf("realm identity: peer %s unreachable to push identity: %w", to, err)
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(ioTimeout))

	if err := json.NewEncoder(s).Encode(pushRequest{Name: name, PrivateKeyBase64: kp.PrivateKeyBase64}); err != nil {
		return fmt.Errorf("realm identity: failed to send identity to %s: %w", to, err)
	}

	var ack pushAck
	if err := json.NewDecoder(io.LimitReader(s, maxBytes)).Decode(&ack); err != nil {
		return fmt.Errorf("realm identity: failed to read ack from %s: %w", to, err)
	}
	if !ack.Imported {
		return fmt.Errorf("realm identity: %s refused identity %q: %s", to, name, ack.Error)
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
			log.Printf("realm identity: failed to decode pushed identity from %s: %v", remote, err)
			return
		}

		if !reg.IsAllowed(remote, ActionPush) {
			log.Printf("realm identity: pushed identity from %s rejected: no permission", remote)
			_ = json.NewEncoder(s).Encode(pushAck{Imported: false, Error: "not allowed"})
			return
		}

		kp, err := realmkeypair.Import(req.PrivateKeyBase64)
		if err != nil {
			log.Printf("realm identity: pushed identity from %s has an invalid key: %v", remote, err)
			_ = json.NewEncoder(s).Encode(pushAck{Imported: false, Error: "invalid key"})
			return
		}

		f.mu.Lock()
		onReceive := f.onReceive
		f.mu.Unlock()

		if onReceive == nil {
			_ = json.NewEncoder(s).Encode(pushAck{Imported: false, Error: "not accepting identities"})
			return
		}
		if err := onReceive(req.Name, kp); err != nil {
			log.Printf("realm identity: failed to import identity %q pushed from %s: %v", req.Name, remote, err)
			_ = json.NewEncoder(s).Encode(pushAck{Imported: false, Error: err.Error()})
			return
		}

		log.Printf("realm identity: imported identity %q pushed from %s", req.Name, remote)
		_ = json.NewEncoder(s).Encode(pushAck{Imported: true})
	}
}
