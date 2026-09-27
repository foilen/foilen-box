package keypair

import (
	"encoding/base64"
	"fmt"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"foilen-realm/model"
)

func Generate() (model.KeyPair, error) {
	priv, _, err := crypto.GenerateKeyPair(crypto.Ed25519, -1)
	if err != nil {
		return model.KeyPair{}, fmt.Errorf("failed to generate keypair: %w", err)
	}
	return fromPrivateKey(priv)
}

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
