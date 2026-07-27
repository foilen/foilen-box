package model

// KeyPair is a libp2p identity: ID is the human-checkable text peer id
// (peer.ID.String()); PrivateKeyBase64 is the base64 encoding of the
// protobuf-marshaled private key (crypto.MarshalPrivateKey). The public key
// and peer ID are both re-derivable from PrivateKeyBase64 at load time, so
// nothing else needs to be persisted.
type KeyPair struct {
	ID               string `json:"id"`
	PrivateKeyBase64 string `json:"privateKeyBase64"`
}
