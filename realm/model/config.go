package model

// DHT mode values for Config.DhtMode: "client" only queries/uses the DHT,
// "server" also stores/serves records for other nodes.
const (
	DhtModeClient = "client"
	DhtModeServer = "server"
)

// DefaultPeerRetentionDays is used when Config.PeerRetentionDays is 0 (unset).
const DefaultPeerRetentionDays = 14

// Listen port mode values for Config.RealmListenPortMode: ListenPortModeDefault
// auto-picks a free port (see PickFreeListenPort); ListenPortModeSpecific uses
// the user-chosen Config.RealmListenPort as-is.
const (
	ListenPortModeDefault  = ""
	ListenPortModeSpecific = "specific"
)

// Config is the persisted Realm configuration.
type Config struct {
	PeerID      KeyPair `json:"peerId"`
	Description string  `json:"description"`
	Groups      []Group `json:"groups"`
	// Identities are standalone keypairs held independent of PeerID/Group.
	Identities []Identity `json:"identities"`
	// Scripts are shell commands this peer offers to run on request, gated by ActionRunScript.
	Scripts []Script `json:"scripts"`
	// Services are local services this peer proxies to on request, gated by services.ActionConnect.
	Services []Service `json:"services"`
	// Permissions grants peers/groups the right to invoke an action; deny-by-default otherwise.
	Permissions []Permission `json:"permissions"`
	DhtMode     string       `json:"dhtMode"`
	EnableMdns  bool         `json:"enableMdns"`
	EnableDht   bool         `json:"enableDht"`

	// Disabled turns the whole networking stack off without discarding identity/groups.
	Disabled bool `json:"disabled"`

	// EnableRelayService runs a circuit-relay-v2 server for this peer's groups so
	// NAT-stuck members can reserve a slot and be reached through it. Off by
	// default (costs bandwidth); useful on a publicly-reachable box.
	EnableRelayService bool `json:"enableRelayService"`

	// PeerRetentionDays: days a known peer may go unseen before pruning. 0 =
	// DefaultPeerRetentionDays; negative disables pruning.
	PeerRetentionDays int `json:"peerRetentionDays"`

	// RealmListenPort is the libp2p host's TCP/UDP port. 0 = unassigned; see
	// PickFreeListenPort, which persists it so addresses stay stable across restarts.
	RealmListenPort int `json:"realmListenPort"`

	// RealmListenPortMode: ListenPortModeDefault (auto-assigned, see
	// RealmListenPort) or ListenPortModeSpecific (user-chosen RealmListenPort).
	RealmListenPortMode string `json:"realmListenPortMode"`

	// ExposeWebEnabled adds a websocket listen address so this peer stays
	// dialable through firewalls/proxies that only allow web ports out.
	// Only useful for a peer with a stable public hostname/IP.
	ExposeWebEnabled bool `json:"exposeWebEnabled"`

	// ExposeWebListenProtocol: "wss" (default, TLS) or "ws" (behind a
	// reverse proxy that already terminates TLS).
	ExposeWebListenProtocol string `json:"exposeWebListenProtocol"`

	// ExposeWebListenPort is where the libp2p host binds the websocket listener locally.
	ExposeWebListenPort int `json:"exposeWebListenPort"`

	// ExposeWebAnnounceHost is the hostname/IP peers should dial to reach
	// this listener — required, since it can't be discovered like LAN/DHT addresses.
	ExposeWebAnnounceHost string `json:"exposeWebAnnounceHost"`

	// ExposeWebAnnouncePort: port peers should dial; 0 = same as ExposeWebListenPort.
	ExposeWebAnnouncePort int `json:"exposeWebAnnouncePort"`

	// ExposeWebAnnounceProtocol: "wss"/"ws"; "" = same as ExposeWebListenProtocol.
	ExposeWebAnnounceProtocol string `json:"exposeWebAnnounceProtocol"`
}
