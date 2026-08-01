# Writing a Realm feature

Realm (module `foilen-realm`, at `realm/`) is a standalone libp2p library:
peer identity, group membership, discovery (mDNS/DHT), and a permission
system, with everything a peer can actually *do* implemented as a pluggable
**Feature**. Foilen Box (the `foilen-box` module) is just one consumer of
this library — it happens to register every built-in feature
(`common/maps`, `common/scripts`, `common/services`), but any application
can register only the ones it needs.

This doc explains how to add a new feature.

## The `Feature` interface

Defined in `realm/feature.go`:

```go
type Feature interface {
	// Name namespaces this feature's actions, e.g. "common/scripts".
	Name() string

	// Actions lists the fully-qualified permission actions this feature's
	// incoming handlers check, of the form Name()+"/"+verb (e.g.
	// "common/scripts/run"). Engine.AvailableActions aggregates
	// these across every registered feature to build the dynamic
	// permission catalog.
	Actions() []model.PermissionAction

	// RegisterHandlers is called once whenever the engine's host is
	// (re)created, so the feature can register its own libp2p stream
	// handler(s) via reg.SetStreamHandler.
	RegisterHandlers(reg *Registrar)
}
```

A feature is an ordinary Go type (usually `*Feature` by convention) that:

1. Owns whatever state/store it needs (e.g. `maps`'s persisted key-value
   stores).
2. Implements one or more libp2p protocols for talking to the same feature
   on another peer.
3. Declares the permission action(s) that gate its incoming handlers.
4. Exposes its own public methods for the application to call (there is no
   generic "call a feature" API on `Engine` — you hold a reference to the
   concrete feature and call its methods directly).

Optionally, a feature can also implement one or more of:

```go
// Called (in its own goroutine) whenever a peer connects.
type PeerConnectedHook interface {
	OnPeerConnected(reg *Registrar, id peer.ID)
}

// Called on the engine's keep-alive tick (currently every 10 minutes).
type PeriodicHook interface {
	RunPeriodic(reg *Registrar)
}

// Called whenever a known peer is dropped from the peer store (currently:
// pruned for being unseen past the configured retention window).
type PeerRemovedHook interface {
	OnPeerRemoved(id string)
}

// Called before Engine disconnects a peer that's outside every group's
// current connection ring (see "Connection shaping" below); returning true
// keeps the connection open.
type PeerInUseHook interface {
	IsPeerInUse(id peer.ID) bool
}
```

Use these for anything that needs to react to connectivity changes, run on
a schedule, clean up per-peer state, or protect an actively-used connection
from being closed (see `maps.Feature` for `PeerConnectedHook`/
`GroupConfirmedHook`, `services.Feature` for `PeerRemovedHook`/
`PeerInUseHook`).

## Connection shaping

Engine doesn't keep every known group peer connected. On each keep-alive
tick (`realm/connection_ring.go`), for every configured group it sorts that
group's confirmed members (including itself) alphabetically by peer id and
tries to stay connected to the `ringNeighborCount` (currently 2) peers
immediately before and after itself in that list, wrapping around; an
unreachable neighbor is skipped in favor of the next one further out. Once
every group's ring has been checked, any other known group peer that's
still connected — one outside every ring it belongs to — is disconnected,
*unless* some registered `PeerInUseHook` reports it's still in use (e.g.
`services.Feature` keeps a peer connected while it has a proxy actively
forwarding data to it).

## `Registrar`: what a feature can do

`RegisterHandlers`, and the two optional hooks above, are all handed a
`*realm.Registrar` — a narrow facade onto the engine, since features must
not reach into `Engine`'s internals directly:

| Method | Purpose |
|---|---|
| `SetStreamHandler(id protocol.ID, h network.StreamHandler)` | Register a libp2p stream handler. Only valid to call from inside `RegisterHandlers`. |
| `Host() host.Host` | The running libp2p host, or `nil` if not running. Use this to dial peers (`host.NewStream(ctx, pid, protocolID)`). |
| `PrivKey() crypto.PrivKey` | This peer's private key, e.g. for signing wire payloads. |
| `Context() context.Context` | The engine's lifetime context — cancelled on `Stop()`; derive per-call timeouts from it. |
| `Config() model.Config` | The currently-applied config (as of the last `Start`/`Reconcile`). Read whatever fields your feature needs (e.g. `scripts.Feature` reads `Config().Scripts`). |
| `IsAllowed(id peer.ID, action model.PermissionAction) bool` | Permission check against `Config().Permissions`, deny-by-default. Call this at the top of every incoming stream handler. |
| `Peers() *peers.Store` | The shared known/connected-peers store. |

A feature typically stores the `*Registrar` it's given (from
`RegisterHandlers`) so its own public methods (called later, from
whatever goroutine the application calls from) can use it — see the
pattern in every built-in feature.

## Step by step: adding a feature

Using a hypothetical `common/ping` feature (a synchronous request/response
"are you there" check) as the example:

1. **Create the package**: `realm/features/ping/feature.go`.

2. **Define the protocol ID, feature name, and action(s)**:

   ```go
   package ping

   const (
       ProtocolID = protocol.ID("/foilen-box/ping/1.0.0")

       FeatureName = "common/ping"

       // ActionRespond gates handling of an incoming ping.
       ActionRespond model.PermissionAction = FeatureName + "/respond"
   )
   ```

3. **Define the `Feature` type**, storing whatever state it needs plus the
   `*realm.Registrar` it's given (guarded by a mutex, since it may be read
   from a different goroutine than it was set on):

   ```go
   type Feature struct {
       mu  sync.Mutex
       reg *realm.Registrar
   }

   func New() *Feature { return &Feature{} }

   func (f *Feature) registrar() *realm.Registrar {
       f.mu.Lock()
       defer f.mu.Unlock()
       return f.reg
   }

   func (f *Feature) Name() string { return FeatureName }

   func (f *Feature) Actions() []model.PermissionAction {
       return []model.PermissionAction{ActionRespond}
   }

   func (f *Feature) RegisterHandlers(reg *realm.Registrar) {
       f.mu.Lock()
       f.reg = reg
       f.mu.Unlock()
       reg.SetStreamHandler(ProtocolID, f.handleStream(reg))
   }
   ```

4. **Implement the incoming stream handler**, checking permission first:

   ```go
   func (f *Feature) handleStream(reg *realm.Registrar) network.StreamHandler {
       return func(s network.Stream) {
           defer s.Close()
           if !reg.IsAllowed(s.Conn().RemotePeer(), ActionRespond) {
               return
           }
           _, _ = s.Write([]byte("pong"))
       }
   }
   ```

5. **Implement the outgoing call** as a public method the application calls
   directly:

   ```go
   func (f *Feature) Ping(to string) (bool, error) {
       reg := f.registrar()
       if reg == nil {
           return false, fmt.Errorf("realm ping: not registered on an engine")
       }
       h := reg.Host()
       if h == nil {
           return false, fmt.Errorf("realm ping: not running")
       }
       pid, err := peer.Decode(to)
       if err != nil {
           return false, err
       }
       ctx, cancel := context.WithTimeout(reg.Context(), 10*time.Second)
       defer cancel()
       s, err := h.NewStream(ctx, pid, ProtocolID)
       if err != nil {
           return false, err
       }
       defer s.Close()
       buf := make([]byte, 4)
       _, err = io.ReadFull(s, buf)
       return err == nil && string(buf) == "pong", nil
   }
   ```

6. **Wire it up** wherever an engine is constructed:

   ```go
   engine := realm.New(dataDir, peerStore)
   pingFeature := ping.New()
   engine.Register(pingFeature)
   // ... engine.Start(cfg) / engine.Reconcile(cfg) as usual ...

   // later, from application code:
   ok, err := pingFeature.Ping(somePeerID)
   ```

   An application that only wants this feature registers nothing else —
   there's no dependency on maps, scripts, or services unless you register
   those features too.

That's the whole contract. `Engine.AvailableActions()` will automatically
include `common/ping/respond` once `pingFeature` is registered, so it shows
up in any permission UI/config validation built on top of it without
further wiring.

## Where the built-in features live

`realm/features/maps`, `realm/features/spec`, and
`realm/features/scripts` are all real, non-trivial examples to crib from —
in particular:

- `maps` shows a signed wire payload and a `PeerConnectedHook`/
  `GroupConfirmedHook` pair that drives a per-store subscribe/unsubscribe
  protocol: a peer only receives pushes for the stores it has explicitly
  subscribed to (tracked via a reserved `_realmMaps` config store), rather
  than syncing everything under a shared group indiscriminately.
- `spec` shows a `TextProvider func() string` constructor argument, used to
  keep an application-specific concern (foilen-box's own
  `internal/spec.Report` machine-info dump) out of the library — a feature
  that needs a piece of app-specific behavior should take it as a
  constructor argument like this, not import the app's code.
- `scripts` shows a feature reading its data (the list of runnable
  scripts) out of `reg.Config()` rather than its own constructor, since
  `model.Config.Scripts` is already part of the shared config shape.

## How foilen-box wires features up

See `internal/webserver/api.go`'s `newAPI`: it constructs one `realm.Engine`
and registers `maps.New(...)`, `spec.New(...)`, and `scripts.New()` against
it, then stores each feature instance on the `api` struct so the WebSocket
handlers in `internal/webserver/api_realm.go` can call their public methods
directly (`a.realmMapsFeature.CreateMap(...)`, `a.realmSpecFeature.RequestSpec(...)`,
`a.realmScripts.ListScripts(...)`, etc.) instead of going through `Engine`.
