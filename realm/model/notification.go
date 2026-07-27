package model

import (
	"fmt"
	"time"
)

// Notification is a single Realm notification, either composed by this peer
// ("sent") or received from another one ("received"). TTLSeconds is chosen
// by the sender per message, since some notifications are only useful for a
// few minutes (e.g. "I'm heading out now") while others matter for a full
// day; it drives both when the local copy is auto-deleted and, for a "sent"
// notification still queued for an offline peer, when delivery is given up
// on rather than retried forever.
type Notification struct {
	ID         string    `json:"id"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	SentAt     time.Time `json:"sentAt"`
	TTLSeconds int       `json:"ttlSeconds"`
	Direction  string    `json:"direction"` // NotificationSent or NotificationReceived
	Delivered  bool      `json:"delivered"` // "sent" notifications only: whether it has reached To yet
}

const (
	NotificationSent     = "sent"
	NotificationReceived = "received"
)

// Expired reports whether n is past its sender-chosen TTL as of now.
func (n Notification) Expired(now time.Time) bool {
	return now.After(n.SentAt.Add(time.Duration(n.TTLSeconds) * time.Second))
}

// NotificationEnvelope is the signed wire format exchanged between peers
// over the notification libp2p protocol: signed by the original author
// (From) rather than relying on transport-level trust, so a future relay
// hop can forward it without being able to forge or having to be trusted.
type NotificationEnvelope struct {
	ID         string `json:"id"`
	From       string `json:"from"`
	To         string `json:"to"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	SentAtUnix int64  `json:"sentAtUnix"`
	TTLSeconds int    `json:"ttlSeconds"`
	Signature  []byte `json:"signature"`
}

// SigningBytes returns the canonical bytes signed/verified for the
// envelope, excluding the signature itself.
func (e NotificationEnvelope) SigningBytes() []byte {
	return []byte(fmt.Sprintf("%s|%s|%s|%s|%s|%d|%d", e.ID, e.From, e.To, e.Title, e.Body, e.SentAtUnix, e.TTLSeconds))
}
