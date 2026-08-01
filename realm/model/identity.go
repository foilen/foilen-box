package model

// Identity is a standalone keypair a peer holds, independent of its own
// PeerID and of any Group: just a named, exportable/importable/pushable
// keypair with no networking behavior of its own (yet).
type Identity struct {
	Name    string  `json:"name"`
	KeyPair KeyPair `json:"keyPair"`
}
