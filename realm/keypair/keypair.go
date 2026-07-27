// Package keypair generates and imports Realm identities (peer id and
// group keys), all stored as a model.KeyPair per decision 4: only the
// base64-encoded, protobuf-marshaled private key is persisted; the public
// key and peer id are re-derived from it at load time.
package keypair

import (
	"encoding/base64"
	"fmt"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"foilen-realm/model"
)

// Generate creates a new Ed25519 identity and returns it as a model.KeyPair.
func Generate() (model.KeyPair, error) {
	priv, _, err := crypto.GenerateKeyPair(crypto.Ed25519, -1)
	if err != nil {
		return model.KeyPair{}, fmt.Errorf("failed to generate keypair: %w", err)
	}
	return fromPrivateKey(priv)
}

// Import decodes and validates a base64-encoded, protobuf-marshaled private
// key (as produced by Generate/exported via model.KeyPair.PrivateKeyBase64)
// and returns the corresponding model.KeyPair, with its ID re-derived from
// the key rather than trusted from input.
func Import(privateKeyBase64 string) (model.KeyPair, error) {
	raw, err := base64.StdEncoding.DecodeString(privateKeyBase64)
	if err != nil {
		return model.KeyPair{}, fmt.Errorf("invalid base64 private key: %w", err)
	}
	priv, err := crypto.UnmarshalPrivateKey(raw)
	if err != nil {
		return model.KeyPair{}, fmt.Errorf("invalid private key: %w", err)
	}
	return fromPrivateKey(priv)
}

// PrivateKey decodes and returns the go-libp2p PrivKey backing kp, for use
// building a host identity or deriving a rendezvous topic.
func PrivateKey(kp model.KeyPair) (crypto.PrivKey, error) {
	raw, err := base64.StdEncoding.DecodeString(kp.PrivateKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 private key: %w", err)
	}
	priv, err := crypto.UnmarshalPrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}
	return priv, nil
}

func fromPrivateKey(priv crypto.PrivKey) (model.KeyPair, error) {
	raw, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return model.KeyPair{}, fmt.Errorf("failed to marshal private key: %w", err)
	}
	id, err := peer.IDFromPublicKey(priv.GetPublic())
	if err != nil {
		return model.KeyPair{}, fmt.Errorf("failed to derive peer id: %w", err)
	}
	return model.KeyPair{
		ID:               id.String(),
		PrivateKeyBase64: base64.StdEncoding.EncodeToString(raw),
	}, nil
}
