package model

// Group is a Realm group a peer has joined: a user-facing label and the
// group's own KeyPair, which is shared out-of-band with other members and
// used both as the group identity and to derive the rotating DHT
// rendezvous topic.
type Group struct {
	Name    string  `json:"name"`
	KeyPair KeyPair `json:"keyPair"`
}
