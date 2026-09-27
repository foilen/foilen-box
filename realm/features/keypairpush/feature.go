package keypairpush

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
	ioTimeout = 10 * time.Second
	maxBytes  = 16 * 1024
)

type pushRequest struct {
	Name             string `json:"name"`
	PrivateKeyBase64 string `json:"privateKeyBase64"`
}

type pushAck struct {
	Imported bool   `json:"imported"`
	Error    string `json:"error,omitempty"`
}

type Feature struct {
	kind        string
	featureName string
	protocolID  protocol.ID
	actionPush  model.PermissionAction
	onReceive   func(name string, kp model.KeyPair) error

	mu  sync.Mutex
	reg *realm.Registrar
}

func New(kind, featureName string, protocolID protocol.ID, actionPush model.PermissionAction, onReceive func(name string, kp model.KeyPair) error) *Feature {
	return &Feature{
		kind:        kind,
		featureName: featureName,
		protocolID:  protocolID,
		actionPush:  actionPush,
		onReceive:   onReceive,
	}
}

func (f *Feature) registrar() *realm.Registrar {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reg
}

func (f *Feature) Name() string { return f.featureName }

func (f *Feature) Actions() []model.PermissionAction {
	return []model.PermissionAction{f.actionPush}
}

func (f *Feature) RegisterHandlers(reg *realm.Registrar) {
	f.mu.Lock()
	f.reg = reg
	f.mu.Unlock()
	reg.SetStreamHandler(f.protocolID, f.handlePushStream(reg))
}

func (f *Feature) Push(to, name string, kp model.KeyPair) (err error) {
	defer func() {
		if err != nil {
			log.Printf("realm %s: push of %s %q to %s failed: %v", f.kind, f.kind, name, f.registrar().PeerLabel(to), err)
		} else {
			log.Printf("realm %s: pushed %s %q to %s", f.kind, f.kind, name, f.registrar().PeerLabel(to))
		}
	}()

	reg := f.registrar()
	if reg == nil {
		return fmt.Errorf("realm %s: not registered on an engine", f.kind)
	}
	h := reg.Host()
	ctx := reg.Context()
	if h == nil || ctx == nil {
		return fmt.Errorf("realm %s: not running", f.kind)
	}

	pid, err := peer.Decode(to)
	if err != nil {
		return fmt.Errorf("realm %s: invalid peer id %q: %w", f.kind, to, err)
	}
	if err := reg.EnsureConnected(ctx, pid); err != nil {
		return fmt.Errorf("realm %s: peer %s unreachable to push %s: %w", f.kind, to, f.kind, err)
	}

	streamCtx, cancel := context.WithTimeout(ctx, ioTimeout)
	defer cancel()
	s, err := h.NewStream(streamCtx, pid, f.protocolID)
	if err != nil {
		return fmt.Errorf("realm %s: peer %s unreachable to push %s: %w", f.kind, to, f.kind, err)
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(ioTimeout))

	if err := json.NewEncoder(s).Encode(pushRequest{Name: name, PrivateKeyBase64: kp.PrivateKeyBase64}); err != nil {
		return fmt.Errorf("realm %s: failed to send %s to %s: %w", f.kind, f.kind, to, err)
	}

	var ack pushAck
	if err := json.NewDecoder(io.LimitReader(s, maxBytes)).Decode(&ack); err != nil {
		return fmt.Errorf("realm %s: failed to read ack from %s: %w", f.kind, to, err)
	}
	if !ack.Imported {
		return fmt.Errorf("realm %s: %s refused %s %q: %s", f.kind, to, f.kind, name, ack.Error)
	}
	return nil
}

func (f *Feature) handlePushStream(reg *realm.Registrar) network.StreamHandler {
	return func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(ioTimeout))

		remote := s.Conn().RemotePeer()
		remoteLabel := reg.PeerLabel(remote.String())

		var req pushRequest
		if err := json.NewDecoder(io.LimitReader(s, maxBytes)).Decode(&req); err != nil {
			log.Printf("realm %s: failed to decode pushed %s from %s: %v", f.kind, f.kind, remoteLabel, err)
			return
		}

		if !reg.IsAllowed(remote, f.actionPush) {
			log.Printf("realm %s: pushed %s from %s rejected: no permission", f.kind, f.kind, remoteLabel)
			_ = json.NewEncoder(s).Encode(pushAck{Imported: false, Error: "not allowed"})
			return
		}

		kp, err := realmkeypair.Import(req.PrivateKeyBase64)
		if err != nil {
			log.Printf("realm %s: pushed %s from %s has an invalid key: %v", f.kind, f.kind, remoteLabel, err)
			_ = json.NewEncoder(s).Encode(pushAck{Imported: false, Error: "invalid key"})
			return
		}

		if f.onReceive == nil {
			_ = json.NewEncoder(s).Encode(pushAck{Imported: false, Error: "not accepting " + f.kind + " pushes"})
			return
		}
		if err := f.onReceive(req.Name, kp); err != nil {
			log.Printf("realm %s: failed to import %s %q pushed from %s: %v", f.kind, f.kind, req.Name, remoteLabel, err)
			_ = json.NewEncoder(s).Encode(pushAck{Imported: false, Error: err.Error()})
			return
		}

		log.Printf("realm %s: imported %s %q pushed from %s", f.kind, f.kind, req.Name, remoteLabel)
		_ = json.NewEncoder(s).Encode(pushAck{Imported: true})
	}
}
