package model

// DHT mode values for Config.DhtMode: "client" only queries/uses the DHT,
// "server" also stores/serves records for other nodes (helps network
// health, costs some bandwidth/storage).
const (
	DhtModeClient = "client"
	DhtModeServer = "server"
)

// DefaultPeerRetentionDays is the number of days a known peer may go
// without being seen before it's pruned, used whenever
// Config.PeerRetentionDays is 0 (unset, e.g. a config saved before this
// field existed).
const DefaultPeerRetentionDays = 14

// Config is the persisted Realm configuration: the peer's own identity,
// the groups it has joined, and the discovery settings from decision 1.
type Config struct {
	PeerID      KeyPair `json:"peerId"`
	Description string  `json:"description"`
	Groups      []Group `json:"groups"`
	// Identities are standalone keypairs this peer holds, independent of
	// its own PeerID and of any Group — created, exported/imported, or
	// received via a push from another peer.
	Identities []Identity `json:"identities"`
	// Scripts are fixed, owner-defined shell commands this peer offers to
	// run on request from peers/groups granted ActionRunScript.
	Scripts []Script `json:"scripts"`
	// Services are local services this peer offers to proxy to, on request
	// from peers/groups granted services.ActionConnect.
	Services []Service `json:"services"`
	// Permissions grants specific peers or groups the right to invoke an
	// action against this machine; deny-by-default for anything not listed.
	Permissions []Permission `json:"permissions"`
	DhtMode     string       `json:"dhtMode"`
	EnableMdns  bool         `json:"enableMdns"`
	EnableDht   bool         `json:"enableDht"`

	// Disabled turns the whole Realm networking stack off (DHT, mDNS,
	// relay, all peer connections) without discarding the peer identity or
	// groups. False (the zero value) means enabled, so configs saved
	// before this field existed default to enabled. Turning it back on
	// brings the host up fresh, as if the app had just started.
	Disabled bool `json:"disabled"`

	// EnableRelayService opts this peer into acting as a circuit-relay-v2
	// server for its groups, so other members can reserve a relay slot on it
	// and be reached through it when they're otherwise unreachable behind a
	// NAT. Useful for a publicly-reachable box (e.g. a VPS); off by default
	// since it costs the host bandwidth on behalf of other peers.
	EnableRelayService bool `json:"enableRelayService"`

	// PeerRetentionDays is how many days a known peer may go without being
	// seen before it's automatically removed from the peer store. 0 means
	// "use DefaultPeerRetentionDays"; a negative value disables pruning
	// entirely (peers are kept forever).
	PeerRetentionDays int `json:"peerRetentionDays"`

	// RealmListenPort is the TCP/UDP port this peer's libp2p host listens
	// on (distinct from the other ports foilen-box opens, e.g. the web
	// UI's). 0 means "not yet assigned"; the caller (see
	// foilen-realm.PickFreeListenPort) picks a free port on first use and
	// persists it here so it stays fixed across restarts, keeping this
	// peer's advertised addresses stable for other peers instead of
	// changing on every launch.
	RealmListenPort int `json:"realmListenPort"`

	// ExposeWebEnabled adds a websocket listen address (typically on port
	// 80 or 443) to the libp2p host, alongside its normal TCP/QUIC
	// listeners, so this peer stays dialable through firewalls/proxies
	// that only allow common web ports out. Off by default: it only makes
	// sense for a peer with a stable public hostname/IP (e.g. a VPS), and
	// binding a privileged port (<1024) requires OS permission the process
	// may not have. The engine's websocket transport always accepts
	// self-signed certs on both ends (see engine.go), since the actual
	// peer authentication happens via libp2p's own Noise handshake, not
	// this transport's TLS layer.
	ExposeWebEnabled bool `json:"exposeWebEnabled"`

	// ExposeWebListenProtocol is "wss" (default, TLS) or "ws" (plain).
	// Use "ws" when this peer sits behind a reverse proxy that already
	// terminates TLS on the public port and forwards plain HTTP/websocket
	// traffic to ExposeWebListenPort locally.
	ExposeWebListenProtocol string `json:"exposeWebListenProtocol"`

	// ExposeWebListenPort is the port the libp2p host binds locally for
	// the websocket listener, e.g. 443 for a direct setup, or an
	// unprivileged internal port (e.g. 8080) behind a reverse proxy.
	ExposeWebListenPort int `json:"exposeWebListenPort"`

	// ExposeWebAnnounceHost is the hostname or IP other peers should dial
	// to reach this listener (e.g. this box's public hostname, or the
	// reverse proxy's). Required for the listener to be of any use, since
	// this address can't be discovered automatically the way LAN/DHT
	// addresses are.
	ExposeWebAnnounceHost string `json:"exposeWebAnnounceHost"`

	// ExposeWebAnnouncePort is the port other peers should dial; 0 means
	// "same as ExposeWebListenPort". Set this when a reverse proxy
	// exposes a different public port (typically 443) than the port this
	// process actually binds locally.
	ExposeWebAnnouncePort int `json:"exposeWebAnnouncePort"`

	// ExposeWebAnnounceProtocol is "wss" or "ws"; "" means "same as
	// ExposeWebListenProtocol". Set this when a reverse proxy terminates
	// TLS in front of a plain "ws" local listener, so peers are still
	// told to dial "wss" externally.
	ExposeWebAnnounceProtocol string `json:"exposeWebAnnounceProtocol"`
}
