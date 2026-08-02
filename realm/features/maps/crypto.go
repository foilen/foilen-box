package maps

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"filippo.io/edwards25519"
	gocrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/crypto/nacl/box"
	"golang.org/x/crypto/nacl/secretbox"

	"foilen-realm/model"
)

// identityPubKeyFromID derives an identity's Ed25519 public key from its
// peer-id-shaped identityID (libp2p inlines small public keys via an
// identity multihash) — any peer can encrypt to an identity without holding it.
func identityPubKeyFromID(identityID string) (gocrypto.PubKey, error) {
	id, err := peer.Decode(identityID)
	if err != nil {
		return nil, fmt.Errorf("realm maps: invalid identity id %q: %w", identityID, err)
	}
	pub, err := id.ExtractPublicKey()
	if err != nil {
		return nil, fmt.Errorf("realm maps: could not extract public key from identity id %q: %w", identityID, err)
	}
	return pub, nil
}

// ed25519PubToX25519 converts an Ed25519 public key to X25519 for box
// encryption (libsodium's crypto_sign_ed25519_pk_to_curve25519).
func ed25519PubToX25519(pub gocrypto.PubKey) (*[32]byte, error) {
	raw, err := pub.Raw()
	if err != nil {
		return nil, fmt.Errorf("realm maps: failed to read raw public key: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("realm maps: expected 32-byte ed25519 public key, got %d", len(raw))
	}
	p, err := (&edwards25519.Point{}).SetBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("realm maps: invalid ed25519 public key: %w", err)
	}
	var out [32]byte
	copy(out[:], p.BytesMontgomery())
	return &out, nil
}

// ed25519PrivToX25519 converts an Ed25519 private key to its X25519 scalar
// (crypto_sign_ed25519_sk_to_curve25519): SHA-512 the seed and clamp the low
// 32 bytes, same as Ed25519's own scalar derivation. Derived fresh each time, never persisted.
func ed25519PrivToX25519(priv gocrypto.PrivKey) (*[32]byte, error) {
	raw, err := priv.Raw()
	if err != nil {
		return nil, fmt.Errorf("realm maps: failed to read raw private key: %w", err)
	}
	if len(raw) != 64 {
		return nil, fmt.Errorf("realm maps: expected 64-byte ed25519 private key, got %d", len(raw))
	}
	h := sha512.Sum512(raw[:32])
	h[0] &= 248
	h[31] &= 127
	h[31] |= 64
	var out [32]byte
	copy(out[:], h[:32])
	return &out, nil
}

// sealSymmetricKey generates a random map symmetric key and seals it
// (crypto_box_seal-equivalent) to recipientPub — only that identity's private
// key holder can recover it; the caller need not hold it.
func sealSymmetricKey(recipientPub gocrypto.PubKey) (encoded string, key [32]byte, err error) {
	if _, err = rand.Read(key[:]); err != nil {
		return "", key, fmt.Errorf("realm maps: failed to generate symmetric key: %w", err)
	}
	recipientX25519, err := ed25519PubToX25519(recipientPub)
	if err != nil {
		return "", key, err
	}
	sealed, err := box.SealAnonymous(nil, key[:], recipientX25519, nil)
	if err != nil {
		return "", key, fmt.Errorf("realm maps: failed to seal symmetric key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sealed), key, nil
}

// openSymmetricKey recovers the key sealed by sealSymmetricKey using the
// target identity's private key.
func openSymmetricKey(encoded string, identityPriv gocrypto.PrivKey) ([32]byte, error) {
	var key [32]byte
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return key, fmt.Errorf("realm maps: invalid encrypted symmetric key: %w", err)
	}
	privX25519, err := ed25519PrivToX25519(identityPriv)
	if err != nil {
		return key, err
	}
	pubX25519, err := ed25519PubToX25519(identityPriv.GetPublic())
	if err != nil {
		return key, err
	}
	opened, ok := box.OpenAnonymous(nil, sealed, pubX25519, privX25519)
	if !ok || len(opened) != 32 {
		return key, errors.New("realm maps: failed to open sealed symmetric key (wrong identity or corrupted data)")
	}
	copy(key[:], opened)
	return key, nil
}

// hashKey returns the external storage key for realKey inside a map
// encrypted to identityID: a length-prefixed hash so the real key stays
// hidden from members without that identity, while still letting them
// replicate the entry under a stable key.
func hashKey(identityID, realKey string) string {
	h := sha256.New()
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(identityID)))
	h.Write(lenBuf[:])
	h.Write([]byte(identityID))
	h.Write([]byte(realKey))
	return hex.EncodeToString(h.Sum(nil))
}

// encryptedPayload bundles key and value into one sealed plaintext, avoiding
// nonce reuse across two ciphertexts and letting the real key be recovered
// on decrypt despite the external key being hashKey's opaque hash.
type encryptedPayload struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// encryptEntry seals {key,value} together as one secretbox under
// symmetricKey and a fresh random nonce, returning both base64-encoded.
func encryptEntry(key, value string, symmetricKey [32]byte) (ciphertext, nonceB64 string, err error) {
	payload, err := json.Marshal(encryptedPayload{Key: key, Value: value})
	if err != nil {
		return "", "", fmt.Errorf("realm maps: failed to marshal encrypted payload: %w", err)
	}
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", "", fmt.Errorf("realm maps: failed to generate nonce: %w", err)
	}
	sealed := secretbox.Seal(nil, payload, &nonce, &symmetricKey)
	return base64.StdEncoding.EncodeToString(sealed), base64.StdEncoding.EncodeToString(nonce[:]), nil
}

// decryptEntry reverses encryptEntry.
func decryptEntry(ciphertextB64, nonceB64 string, symmetricKey [32]byte) (key, value string, err error) {
	sealed, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return "", "", fmt.Errorf("realm maps: invalid ciphertext: %w", err)
	}
	nonceBytes, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil || len(nonceBytes) != 24 {
		return "", "", errors.New("realm maps: invalid nonce")
	}
	var nonce [24]byte
	copy(nonce[:], nonceBytes)
	opened, ok := secretbox.Open(nil, sealed, &nonce, &symmetricKey)
	if !ok {
		return "", "", errors.New("realm maps: failed to decrypt entry (wrong key or corrupted data)")
	}
	var payload encryptedPayload
	if err := json.Unmarshal(opened, &payload); err != nil {
		return "", "", fmt.Errorf("realm maps: failed to unmarshal decrypted payload: %w", err)
	}
	return payload.Key, payload.Value, nil
}

// signEncryptedEvent signs ev's EncryptedSigningBytes with identityPriv,
// proving the producer holds the target identity's key (not just the
// group's), base64-encoded for MapEntry/MapEvent.IdentitySignature.
func signEncryptedEvent(identityPriv gocrypto.PrivKey, ev model.MapEvent) (string, error) {
	sig, err := identityPriv.Sign(ev.EncryptedSigningBytes())
	if err != nil {
		return "", fmt.Errorf("realm maps: failed to sign encrypted event: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// verifyEncryptedEvent reports whether ev.IdentitySignature validates against
// identityPub over ev.EncryptedSigningBytes — checked before decrypting ev.
func verifyEncryptedEvent(identityPub gocrypto.PubKey, ev model.MapEvent) bool {
	if ev.IdentitySignature == "" {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(ev.IdentitySignature)
	if err != nil {
		return false
	}
	ok, err := identityPub.Verify(ev.EncryptedSigningBytes(), sig)
	return err == nil && ok
}
