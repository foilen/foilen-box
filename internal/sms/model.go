// Package sms implements the "SMS" Realm subtab: one Android device acts as
// the read/write authority for its own SMS messages, synced to other peers
// through an encrypted foilen-realm map named "SMS-<suffix>". Manager
// registers as a realm.Feature only to receive PeriodicHook ticks — all wire
// traffic goes through foilen-realm/features/maps.
package sms

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// storePrefix names every realmmap this feature manages or reads.
const storePrefix = "SMS-"

// kindCreate marks a create-request key's second segment; a plain message
// key has a unix-millis timestamp there instead (see parseKey).
const kindCreate = "create"

// kindEnabled marks a presence-marker key: its mere presence means peerId
// currently manages that store (see Manager.touchEnabledMarker), used to
// restrict "Send from peer" pickers to peers that can fulfill a create-request.
const kindEnabled = "enabled"

// SmsMessage is the JSON value of one message entry, keyed
// "<peerId>/<unixMillis>/<hash>" (see messageKey).
type SmsMessage struct {
	PeerID              string `json:"peerId,omitempty"`
	PhoneNumber         string `json:"phoneNumber"`
	Direction           string `json:"direction"` // "incoming" | "outgoing"
	Body                string `json:"body"`
	Sender              string `json:"sender"`
	Receiver            string `json:"receiver"`
	TimestampUnixMillis int64  `json:"timestampUnixMillis"`

	// Raw is a temporary diagnostic dump of every content://sms column
	// (reconcileDeviceStore), to check for a "Trash" state column; remove once settled.
	Raw map[string]string `json:"raw,omitempty"`
}

const (
	DirectionIncoming = "incoming"
	DirectionOutgoing = "outgoing"
)

// SmsCreateRequest is the JSON value of a create-request entry, keyed
// "<peerId>/create/<uniqueId>" (see createKey).
type SmsCreateRequest struct {
	PhoneNumber string `json:"phoneNumber"`
	Body        string `json:"body"`
}

// ConversationSummary is one row of the conversation list: every message
// sharing the same PhoneNumber, summarized for display.
type ConversationSummary struct {
	PhoneNumber             string `json:"phoneNumber"`
	MessageCount            int    `json:"messageCount"`
	LastMessageBody         string `json:"lastMessageBody"`
	LastMessageDirection    string `json:"lastMessageDirection"`
	LastTimestampUnixMillis int64  `json:"lastTimestampUnixMillis"`
}

// IsSmsStore reports whether storeName is one this package manages.
func IsSmsStore(storeName string) bool {
	return strings.HasPrefix(storeName, storePrefix)
}

// StoreNameFor builds the realmmap store name for a given suffix, e.g.
// "phone1" -> "SMS-phone1".
func StoreNameFor(suffix string) string {
	return storePrefix + suffix
}

// hashValue returns a short opaque hash of a message's JSON bytes, used as
// the last segment of its key so two messages with the same peer/timestamp
// (e.g. two texts in the same millisecond) don't collide.
func hashValue(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

// messageKey builds the key for a message entry: peerID is the owning
// device that authored/imported it, ts is its own timestamp (not when it was
// synced), and value is its already-marshaled JSON.
func messageKey(peerID string, ts int64, value []byte) string {
	return fmt.Sprintf("%s/%d/%s", peerID, ts, hashValue(value))
}

// createKey builds the key for a create-request entry targeting peerID.
func createKey(peerID, uniqueID string) string {
	return peerID + "/" + kindCreate + "/" + uniqueID
}

// enabledKey builds the key for peerID's "enabled" presence marker, one per
// peer per store.
func enabledKey(peerID string) string {
	return peerID + "/" + kindEnabled
}

// parseKey splits a key into its owning peerID and kind ("" for a plain
// message, kindCreate for a create-request, kindEnabled for a presence
// marker). ok is false for anything that doesn't match any of those shapes
// (defensive against malformed/foreign entries).
func parseKey(key string) (peerID string, kind string, ok bool) {
	parts := strings.SplitN(key, "/", 3)
	if len(parts) < 2 || parts[0] == "" {
		return "", "", false
	}
	if parts[1] == kindCreate {
		if len(parts) != 3 || parts[2] == "" {
			return "", "", false
		}
		return parts[0], kindCreate, true
	}
	if parts[1] == kindEnabled {
		if len(parts) != 2 {
			return "", "", false
		}
		return parts[0], kindEnabled, true
	}
	if len(parts) != 3 {
		return "", "", false
	}
	return parts[0], "", true
}

// randomHex returns a random hex id for a create-request's uniqueId
// segment, the same primitive as webserver's session token (newToken).
func randomHex() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
