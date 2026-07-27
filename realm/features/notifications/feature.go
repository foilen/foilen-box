// Package notifications is the "common/notifications" Realm feature:
// signed, best-effort, queued-while-offline notifications between peers.
package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	realm "foilen-realm"
	"foilen-realm/model"
)

const (
	// ProtocolID is the libp2p protocol used to deliver a single signed
	// model.NotificationEnvelope per stream (decision: one-shot,
	// fire-and-forget streams rather than a long-lived session).
	ProtocolID = protocol.ID("/foilen-box/notification/1.0.0")
	ioTimeout  = 10 * time.Second
	maxBytes   = 8 * 1024

	// FeatureName is this feature's namespace, used to prefix its actions.
	FeatureName = "common/notifications"

	// ActionReceive gates handling of an incoming notification from a peer.
	ActionReceive model.PermissionAction = FeatureName + "/receive"
)

// Sink lets platform-specific code (a desktop tray notification, the
// Android app's system notification) react the moment a verified
// notification is received, independent of whether a UI is open.
type Sink interface {
	Notify(from, title, body string)
}

// Feature implements realm.Feature and realm.PeerConnectedHook: on every
// peer (re)connect it retries any of this peer's own queued sends to that
// peer.
type Feature struct {
	store *Store

	mu   sync.Mutex
	sink Sink
	reg  *realm.Registrar // set by RegisterHandlers once attached to an Engine
}

// New builds the notifications Feature backed by store (see NewStore).
func New(store *Store) *Feature {
	return &Feature{store: store}
}

// SetSink registers the platform-specific callback invoked when a verified
// notification is received. Safe to call at any time; nil disables it.
func (f *Feature) SetSink(sink Sink) {
	f.mu.Lock()
	f.sink = sink
	f.mu.Unlock()
}

func (f *Feature) registrar() *realm.Registrar {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reg
}

func (f *Feature) Name() string { return FeatureName }

func (f *Feature) Actions() []model.PermissionAction {
	return []model.PermissionAction{ActionReceive}
}

func (f *Feature) RegisterHandlers(reg *realm.Registrar) {
	f.mu.Lock()
	f.reg = reg
	f.mu.Unlock()
	reg.SetStreamHandler(ProtocolID, f.handleStream(reg))
}

// OnPeerConnected retries any undelivered sends of ours to id, per
// realm.PeerConnectedHook.
func (f *Feature) OnPeerConnected(reg *realm.Registrar, id peer.ID) {
	f.flushPendingSends(reg, id)
}

// SendNotification signs and sends a notification to peer id "to",
// persisting it either way: delivered immediately if to is currently
// reachable, or queued to be retried the next time to reconnects (up to
// ttlSeconds after which it's given up on, per Notification.Expired).
func (f *Feature) SendNotification(to, title, body string, ttlSeconds int) (model.Notification, error) {
	reg := f.registrar()
	if reg == nil {
		return model.Notification{}, fmt.Errorf("realm notifications: not registered on an engine")
	}
	h := reg.Host()
	priv := reg.PrivKey()
	ctx := reg.Context()
	if h == nil || priv == nil {
		return model.Notification{}, fmt.Errorf("realm notifications: not running")
	}

	n := model.Notification{
		ID:         uuid.NewString(),
		From:       h.ID().String(),
		To:         to,
		Title:      title,
		Body:       body,
		SentAt:     time.Now().UTC(),
		TTLSeconds: ttlSeconds,
		Direction:  model.NotificationSent,
	}

	env, err := signNotification(priv, n)
	if err != nil {
		return model.Notification{}, err
	}
	n.Delivered = trySendEnvelope(ctx, reg, env)
	f.store.Upsert(n)
	return n, nil
}

// ListNotifications returns every not-yet-expired notification, newest first.
func (f *Feature) ListNotifications() []model.Notification {
	return f.store.List()
}

// OnPeerRemoved deletes every notification to/from id, per
// realm.PeerRemovedHook.
func (f *Feature) OnPeerRemoved(id string) {
	f.store.RemovePeer(id)
}

// flushPendingSends retries every undelivered "sent" notification queued
// for pid, called whenever it (re)connects.
func (f *Feature) flushPendingSends(reg *realm.Registrar, pid peer.ID) {
	h := reg.Host()
	priv := reg.PrivKey()
	ctx := reg.Context()
	if h == nil || priv == nil {
		return
	}

	for _, n := range f.store.ListPendingSendsTo(pid.String()) {
		env, err := signNotification(priv, n)
		if err != nil {
			log.Printf("realm notifications: failed to sign queued notification %s: %v", n.ID, err)
			continue
		}
		if trySendEnvelope(ctx, reg, env) {
			f.store.MarkDelivered(n.ID)
		}
	}
}

// trySendEnvelope opens a one-shot stream to env.To, writes env, and waits
// for a short ack. Returns false (not an error) if the peer is unreachable
// right now — that's the expected offline case, handled by queueing.
func trySendEnvelope(ctx context.Context, reg *realm.Registrar, env model.NotificationEnvelope) bool {
	if ctx == nil {
		return false
	}
	h := reg.Host()
	if h == nil {
		return false
	}
	pid, err := peer.Decode(env.To)
	if err != nil {
		log.Printf("realm notifications: invalid notification recipient %q: %v", env.To, err)
		return false
	}
	if err := reg.EnsureConnected(ctx, pid); err != nil {
		log.Printf("realm notifications: peer %s unreachable for notification (queued): %v", env.To, err)
		return false
	}

	streamCtx, cancel := context.WithTimeout(ctx, ioTimeout)
	defer cancel()
	s, err := h.NewStream(streamCtx, pid, ProtocolID)
	if err != nil {
		log.Printf("realm notifications: peer %s unreachable for notification (queued): %v", env.To, err)
		return false
	}
	defer s.Close()

	_ = s.SetDeadline(time.Now().Add(ioTimeout))
	if err := json.NewEncoder(s).Encode(env); err != nil {
		log.Printf("realm notifications: failed to send notification to %s: %v", env.To, err)
		return false
	}

	ack := make([]byte, 2)
	if _, err := io.ReadFull(s, ack); err != nil {
		log.Printf("realm notifications: no ack from %s for notification: %v", env.To, err)
		return false
	}
	return true
}

// handleStream is the libp2p stream handler for ProtocolID: decode, verify
// the original author's signature (so a future relay hop can forward
// without being trusted), persist if not already expired, and notify the
// platform sink.
func (f *Feature) handleStream(reg *realm.Registrar) network.StreamHandler {
	return func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(ioTimeout))

		var env model.NotificationEnvelope
		if err := json.NewDecoder(io.LimitReader(s, maxBytes)).Decode(&env); err != nil {
			log.Printf("realm notifications: failed to decode incoming notification: %v", err)
			return
		}

		fromID, err := peer.Decode(env.From)
		if err != nil {
			log.Printf("realm notifications: notification with invalid sender id %q: %v", env.From, err)
			return
		}
		pubKey, err := fromID.ExtractPublicKey()
		if err != nil {
			log.Printf("realm notifications: cannot extract public key for sender %s: %v", env.From, err)
			return
		}
		ok, err := pubKey.Verify(env.SigningBytes(), env.Signature)
		if err != nil || !ok {
			log.Printf("realm notifications: notification signature verification failed from %s", env.From)
			return
		}
		if !reg.IsAllowed(fromID, ActionReceive) {
			log.Printf("realm notifications: notification from %s rejected: no permission", env.From)
			return
		}

		n := model.Notification{
			ID:         env.ID,
			From:       env.From,
			To:         env.To,
			Title:      env.Title,
			Body:       env.Body,
			SentAt:     time.Unix(env.SentAtUnix, 0).UTC(),
			TTLSeconds: env.TTLSeconds,
			Direction:  model.NotificationReceived,
			Delivered:  true,
		}
		if n.Expired(time.Now()) {
			log.Printf("realm notifications: dropping already-expired notification %s from %s", n.ID, n.From)
			return
		}
		f.store.Upsert(n)

		if _, err := s.Write([]byte("ok")); err != nil {
			log.Printf("realm notifications: failed to ack notification %s: %v", n.ID, err)
		}

		f.mu.Lock()
		sink := f.sink
		f.mu.Unlock()
		if sink != nil {
			sink.Notify(n.From, n.Title, n.Body)
		}
	}
}

// signNotification builds the signed wire envelope for a Notification.
func signNotification(priv crypto.PrivKey, n model.Notification) (model.NotificationEnvelope, error) {
	env := model.NotificationEnvelope{
		ID:         n.ID,
		From:       n.From,
		To:         n.To,
		Title:      n.Title,
		Body:       n.Body,
		SentAtUnix: n.SentAt.Unix(),
		TTLSeconds: n.TTLSeconds,
	}
	sig, err := priv.Sign(env.SigningBytes())
	if err != nil {
		return model.NotificationEnvelope{}, fmt.Errorf("realm notifications: failed to sign notification: %w", err)
	}
	env.Signature = sig
	return env, nil
}
