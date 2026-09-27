package model

const (
	DhtModeClient = "client"
	DhtModeServer = "server"
)

const DefaultPeerRetentionDays = 14

const (
	ListenPortModeDefault  = ""
	ListenPortModeSpecific = "specific"
)

type Config struct {
	PeerID      KeyPair `json:"peerId"`
	Description string  `json:"description"`
	Groups      []Group `json:"groups"`

	Identities []Identity `json:"identities"`

	Scripts []Script `json:"scripts"`

	Services []Service `json:"services"`

	Permissions        []Permission `json:"permissions"`
	DhtMode            string       `json:"dhtMode"`
	EnableUdpBroadcast bool         `json:"enableUdpBroadcast"`
	EnableDht          bool         `json:"enableDht"`

	Disabled bool `json:"disabled"`

	EnableRelayService bool `json:"enableRelayService"`

	PeerRetentionDays int `json:"peerRetentionDays"`

	RealmListenPort int `json:"realmListenPort"`

	RealmListenPortMode string `json:"realmListenPortMode"`

	ExposeWebEnabled bool `json:"exposeWebEnabled"`

	ExposeWebListenProtocol string `json:"exposeWebListenProtocol"`

	ExposeWebListenPort int `json:"exposeWebListenPort"`

	ExposeWebAnnounceHost string `json:"exposeWebAnnounceHost"`

	ExposeWebAnnouncePort int `json:"exposeWebAnnouncePort"`

	ExposeWebAnnounceProtocol string `json:"exposeWebAnnounceProtocol"`
}
