# Realm library design notes

Design rationale for `realm/`. For how to write a feature, see `features.md`.

## Engine lifecycle (`engine.go`)

- `Engine` is idle after `New`; `Start`/`Stop` bring the libp2p host up and down. Features must be registered
  before the first `Start`.
- `Reconcile` applies a new config with minimal disruption: it adjusts UDP broadcast, DHT and per-group DHT
  loops in place. Only a change of peer identity, listen port (or its mode), relay service flag or web
  listener settings forces a full `Restart` (new host, new connections, fresh DHT routing table).
- On `Start`, every peer's `Connected` flag is reset: a persisted "connected" from a previous host doesn't
  apply to the new one.
- `Stop` releases `e.mu` before calling `h.Close()`: `Close` blocks until every `Disconnected` handler returns,
  and those handlers take `e.mu` themselves. `running`/`host`/`ctx` are cleared first so the handlers skip
  reconnecting.
- Host options:
  - `NATPortMap` forwards the listen port on the local router (UPnP/NAT-PMP).
  - `EnableHolePunching` (DCUtR) upgrades a relayed connection to a direct one.
  - Custom transports opt the host out of go-libp2p's default listen addresses and transports, so both are
    re-added explicitly.
- `listenAddrsForPort` mirrors go-libp2p's default listen addresses (TCP and QUIC, v4 and v6) but pins the
  port, so the advertised addresses only change when the IP changes. `PickFreeListenPort` picks the port
  once; the caller persists it.
- `ConnectedHosts` returns the IP actually dialed. For a relayed connection that is the relay's IP, which is
  what callers need to route around a VPN (see OpenVPN in `internal/browseropen`).
- The hostname override exists for Android, where the OS hostname is always `localhost`.

## Feature hooks (`feature.go`)

- `Registrar` is the narrow facade given to features so they don't reach into `Engine`.
- `Registrar.SetStreamHandler` reads `e.host` directly: its only caller runs inside `Engine.Start` while
  holding `e.mu`, so going through `Host()` would deadlock.
- `EnsureConnected` dials a peer on demand (from its peer store addresses, which may include relay addresses
  added by `announce`) so an action doesn't fail just because the periodic reconnect hasn't reached that peer
  yet.
- `PeerDisconnectedHook` fires once when a peer's last connection closes, not per connection and not on a
  stale-peer prune.

## Keep-alive tick (`peers_helper.go`)

Every `keepAliveInterval` (10 minutes), and on `RunPeriodicNow`:

1. Every `PeriodicHook` runs. They run first because `common/announce` merges reachability addresses into
   the peer store before the ring reconnect dials.
2. Group rings are maintained (see below).
3. The DHT swarm is trimmed in client mode.
4. Stale peers are pruned: known, disconnected peers unseen for `PeerRetentionDays`
   (`DefaultPeerRetentionDays` when 0, disabled when negative).

`onDisconnected` fires once per closed connection; only the drop of a peer's last connection is recorded.
Unknown peers (DHT strangers) are ignored.

## Connection shaping (`connection_ring.go`)

See `features.md` for the overview. Details:

- Ring members are the peer itself plus every known peer whose *confirmed* `GroupNames` contains the group.
  Discovery alone never counts.
- When a ring neighbor disconnects, a single reconnect attempt is made after `reconnectDelay` instead of
  waiting up to 10 minutes for the next tick.
- Extra connected group peers are closed unless a `PeerInUseHook` claims them. Untracked peers (DHT routing
  connections) are left alone.

## Discovery

### UDP broadcast (`discovery_udpbroadcast*.go`)

- Fixed port `58421`, shared by every node regardless of group.
- Messages carry `groupTopic` hashes (daily `hash(date + group private key)`) instead of group ids, so a
  passive LAN observer can't tell which groups exist without already holding a group key.
- Sent to `255.255.255.255` with `SO_BROADCAST` instead of each interface's directed broadcast address:
  enumerating interfaces is SELinux-blocked for regular Android apps (golang/go#40569).
- The delay between beats is random within bounds so nodes on the same LAN don't beat in lockstep.
- A beat is skipped when every one of our group hashes has already been seen from other nodes since the last
  beat. Several peers advertising different subsets can jointly satisfy this. With no groups, a node never
  broadcasts.

### DHT (`discovery_dht.go`)

- Each group advertises and searches under a daily-rotating rendezvous topic, `hash(yyyy-mm-dd + group
  private key)`, so a group's presence on the public DHT can't be linked long-term.
- Each group has its own loop with its own context, so a group can be added or removed without restarting
  the DHT or the host.
- The DHT doesn't own the leveldb datastore handed to it; it must be closed separately or its lock file
  prevents the next `Start` from reopening it.
- In client mode the engine disconnects from DHT swarm peers between lookups so it doesn't stay connected to
  public infrastructure. The last `maxRememberedDHTPeers` are remembered and redialed on the next lookup
  instead of starting from the public bootstrap list. Server mode stays connected. Stopping the DHT doesn't
  close its swarm connections, so they are explicitly disconnected too.

## Group membership (`peer_identify.go`, `group_challenge.go`)

- On connect, peers exchange an identify payload (hostname, description, version, relay flag, addresses and
  *claimed* group ids). Only peers already known through our own discovery are answered.
- A claimed group is never trusted. For every claimed group we also hold, a group challenge is sent: the
  remote signs `challengeHash` (both peer ids, the group id and a fresh nonce) with the group private key.
  This binding prevents replaying a signature for another peer pair, another group or a later challenge.
- Only a passed challenge adds the group to the peer's `GroupNames`. Answering a challenge says nothing about
  the requester, so it doesn't touch our peer store.
- When a group is added to our config, identify is re-run with every connected peer so the new group is
  announced right away.
- `groupKey` identifies a group by its key pair, so a rename is not treated as remove + add.

## Permissions (`relay.go`)

`isAllowed` is deny-by-default. A permission grants an action to a peer id, or to a group, based on the
peer's confirmed `GroupNames`.

## Relay transport (`relay_transport.go`)

An application-level replacement for circuit-relay-v2:

- The source peer (A) speaks the hop protocol to the relay (R), which speaks the stop protocol to the target
  (B), then pumps bytes between both streams. A and B run their normal libp2p security/mux handshake through
  that pipe, so the connection carries B's real, verified peer id.
- The relay never dials: it refuses unless it is already connected to B and both A and B share a group with
  it.
- Always registered, so a host can dial through a relay and be relayed to. `EnableRelayService` only
  controls whether the host advertises itself as a relay (via `announce`). Off by default since it costs
  bandwidth; useful on a publicly reachable box.
- Custom multiaddr protocol code from multicodec's private-use range, distinct from `p2p-circuit` so
  go-libp2p never mistakes these addresses for circuit-relay ones.
- `relayListenAddr` is a marker address: it has no socket and only makes libp2p call `Listen` so the
  handlers are wired before a relayed connection arrives. It is filtered out of advertised addresses (peers
  dialing it directly would fail with "no transport for protocol"). It is built in `init()` because
  package-level variables are initialized before any `init()`, i.e. before the protocol is registered.
- The swarm strips a trailing `/p2p/<id>` from an address before `CanDial`/`Dial`, so `Dial` takes the target
  from its `peer.ID` argument and `CanDial` accepts both the full and stripped forms.
- Handshake lines are read byte by byte (not with `bufio`) so no bytes of the following payload are left
  buffered. Their length is capped as a guard against misbehaving peers.

## Web listener (`web_transport.go`, `web_bridge.go`, `expose_web.go`)

- Makes a peer dialable through firewalls or proxies that only allow web ports. Only useful for a peer with a
  stable public hostname or IP; the announce host must be configured since it can't be discovered.
- A libp2p transport for custom `realm-http`/`realm-https` multiaddr protocols (private-use codes), not the
  standard `/http` or `/ws`, so nothing mistakes it for libp2p's websocket or HTTP transports.
- `Listen` runs an HTTP(S) server with an informational index page and a `/p2p` WebSocket endpoint. The
  WebSocket byte stream goes straight into libp2p's security/mux upgrade.
- The transport is always registered (to dial peers advertising one) but only listens when enabled. Its
  settings require a restart since they are only applied through `libp2p.New` options.
- HTTPS uses a throwaway self-signed certificate; dialers skip verification and any origin is accepted.
  Real authentication is libp2p's Noise handshake on top.
- ALPN is forced to HTTP/1.1: the WebSocket upgrade hijacks the connection, which HTTP/2 doesn't support.
- `http` listen protocol exists for running behind a reverse proxy that terminates TLS; the announced
  protocol/port can then differ from the listened ones. Without an announce host, the outbound IPv4 is used
  (found by a UDP "connect", which sends no packet).

## Peer store (`peers/store.go`)

- The app's persisted view of known peers, independent of go-libp2p's in-memory peerstore.
- Addresses are kept per source (`broadcast`, `announce`, `dht`) so sources don't clobber each other, then
  merged by priority: LAN first, then self-announced, then public DHT (least likely to be directly reachable).
- `broadcast`/`dht` can go stale when a peer stops being rediscovered and can be cleared; `announce` comes
  from the `common` map and is kept.
- Becoming connected refreshes `LastSeen`, since a live connection proves the peer was just seen.
- Connected peers are never pruned. A peer whose `GroupNames` becomes empty is kept.

## Persistence (`jsondb`, `config`)

- `jsondb.Store` keeps the value in memory and debounces writes: a change schedules a save after
  `SaveDelay`, further changes reset the timer. `Flush` is used on shutdown.
- Missing or corrupt files load as defaults instead of failing (same behavior as the other config services).
- `config.New` applies the platform default DHT mode only when `realm.json` doesn't exist yet; a persisted
  value always wins.

## Key pairs (`keypair`, `model/keypair.go`)

Only the base64 protobuf-marshaled private key is persisted. The public key and id are re-derived from it on
load and on import (never trusted from input).

## Features

### `common/maps`

- Small `Map<String,String>` stores scoped to a group, replicated as a signed, last-write-wins event log.
  Deletes are tombstones so they replicate like any mutation.
- The group signature is the only write authorization (every member holds the group key), so the feature
  declares no permission actions; subscribing only requires being a confirmed group member.
- Subscriptions:
  - A peer only receives pushes for the stores it subscribed to.
  - When a peer becomes available for a group (connected, or later confirmed by a challenge), we subscribe to
    the system stores (`common` and `_realmMaps`), then to every store listed in `_realmMaps`.
  - Reconciliation uses the full desired set, not a diff, so a reconnecting peer still gets stores created
    while it was away. A store removed from `_realmMaps` is unsubscribed and purged locally.
  - Pushes are fire-and-forget; a missed push is caught up on the next subscribe. Received events that
    change something are relayed to our own subscribers except the sender.
  - In-memory subscriptions are dropped on disconnect.
- Catch-up cursors are per (group, store, peer) rather than one watermark per store: otherwise a push missed
  from one peer could be permanently shadowed by a newer event received from another peer.
- LWW merge rejects only strictly older events, so same-millisecond mutations still apply in call order.
  Only a real content change is logged, persisted and re-broadcast, so periodic identical re-posts don't
  flood the mesh.
- `persist` snapshots entries before unlocking since JSON marshaling would otherwise race with concurrent
  `ApplyEvent` calls.
- `Store.Subscribe` doesn't dedupe listeners, so the feature installs its own listener only once even though
  `RegisterHandlers` runs on every host creation.
- Auto-delete sweep: at most hourly, at a minute chosen randomly at startup so members don't all sweep at the
  same time. One member sweeping is enough since the tombstone propagates.
- An empty map has no events, so it isn't visible to other members until its first key is set.
- Maps of groups no longer configured stay on disk but are hidden.

#### Encryption

- `RealmMapConfig` lives in `_realmMaps` and is never encrypted: every member sees whether a map is
  encrypted and to which identity, without being able to read it.
- A random symmetric key is sealed (`crypto_box_seal` style) to the identity's public key. The public key is
  derivable from the identity id alone (libp2p inlines small keys), so anyone can create a map encrypted to an
  identity; reading and writing requires holding it.
- Ed25519 keys are converted to X25519 for the box (`crypto_sign_ed25519_*_to_curve25519`), derived fresh
  each time and never persisted.
- The external key is a hash of `identityId + realKey`, so the real key stays hidden while members can still
  replicate the entry under a stable key.
- Key and value are sealed together in one secretbox with a fresh nonce: no nonce reuse across two
  ciphertexts, and the real key is recovered on decrypt.
- Encrypted events are also signed with the identity key (over the event minus that signature) and verified
  before decrypting, proving the writer holds the identity and not just the group key. The group signature
  covers the nonce and identity signature so neither can be moved onto another event.

### `common/announce`

- Every tick, posts this peer's services, scripts, reachability info and spec into each group's `common` map,
  then consumes the other peers' `peers/{peerId}` entries into the peer store.
- Reachability info is posted on change or every 24h; the (expensive) spec at most every 6h.
- A valid entry only proves the group key was held, not that the author is the peer named in the key. So it
  only records addresses; group membership still requires a challenge.
- The `common` map gets a default 7-day auto-delete setting the first time a peer notices it's missing,
  since `SetValue` creates stores without config.
- App-specific data (spec, hostname, version) is injected as constructor callbacks to keep the library free
  of app code.

### `common/scripts`

- Owner-defined commands only; callers can trigger a script but never pass arguments.
- The run request gets a synchronous ack (started or refused). The completion is a best-effort,
  fire-and-forget push without retry: silence doesn't mean failure. It needs no permission check since it
  only closes a run we initiated.
- Runs are kept only as long as a caller might poll them.

### `common/services`

- One forwarded TCP connection per tunnel stream: JSON header, ack, then raw bytes. The handshake deadline is
  cleared for the data phase so idle connections aren't killed.
- The local proxy port is deterministic: `hash(peerId + serviceName)` picks a start in the dynamic range and
  collisions probe forward.
- Explicitly started proxies are persisted and restored after a restart. `StopAll` (shutdown) keeps that
  record, `StopProxy` (user action) removes it. `Engine.Stop` doesn't touch feature-owned local state, hence
  `StopAll`.
- A peer is "in use" only while a proxy has a live connection; an idle proxy reconnects on demand.
- The local scan reports UDP entries as unverified since a TCP dial can't confirm them.

### `common/identity` and `common/group`

- Push a key pair to another peer, which imports it automatically if it granted the push action. The
  connection is already libp2p-authenticated so nothing extra is signed.
- The key pair id is re-derived from the received private key, never trusted from the wire.
- The app persists the import through the `onReceive` callback since features can't write the config.
- A pushed group arrives without permissions; the receiving user assigns them.
