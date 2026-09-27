package sms

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const storePrefix = "SMS-"

const kindCreate = "create"

const kindEnabled = "enabled"

type SmsMessage struct {
	PeerID              string `json:"peerId,omitempty"`
	PhoneNumber         string `json:"phoneNumber"`
	Direction           string `json:"direction"`
	Body                string `json:"body"`
	Sender              string `json:"sender"`
	Receiver            string `json:"receiver"`
	TimestampUnixMillis int64  `json:"timestampUnixMillis"`

	Raw map[string]string `json:"raw,omitempty"`
}

const (
	DirectionIncoming = "incoming"
	DirectionOutgoing = "outgoing"
)

type SmsCreateRequest struct {
	PhoneNumber string `json:"phoneNumber"`
	Body        string `json:"body"`
}

type ConversationSummary struct {
	PhoneNumber             string `json:"phoneNumber"`
	MessageCount            int    `json:"messageCount"`
	LastMessageBody         string `json:"lastMessageBody"`
	LastMessageDirection    string `json:"lastMessageDirection"`
	LastTimestampUnixMillis int64  `json:"lastTimestampUnixMillis"`
}

func IsSmsStore(storeName string) bool {
	return strings.HasPrefix(storeName, storePrefix)
}

func StoreNameFor(suffix string) string {
	return storePrefix + suffix
}

func hashValue(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

func messageKey(peerID string, ts int64, value []byte) string {
	return fmt.Sprintf("%s/%d/%s", peerID, ts, hashValue(value))
}

func createKey(peerID, uniqueID string) string {
	return peerID + "/" + kindCreate + "/" + uniqueID
}

func enabledKey(peerID string) string {
	return peerID + "/" + kindEnabled
}

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

func randomHex() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
