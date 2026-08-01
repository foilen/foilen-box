# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Foilen Box: a single tray/WebView app bundling several utilities. All business logic is plain Go; the UI is
a vanilla HTML/CSS/JS app (no npm, no build step) served by an embedded HTTP+WebSocket server bound to
`127.0.0.1` only.

- **Desktop** — a system tray-only Go binary (Linux/macOS/Windows, `cmd/foilenbox`). "Open" launches the
  default browser at the local UI; "Quit" stops the server.
- **Android** — a native Android Studio app (`android/`) whose `MainActivity` starts the same Go server
  in-process (built as an AAR via `gomobile bind` from `cmd/mobile`) and shows the UI in a `WebView`. GPS
  uses the standard `navigator.geolocation` Web API in JS — no custom native location bridge.

Two Go modules joined by `go.work`: the root module `foilen-box` (app/UI/platform glue) and `foilen-realm`
at `realm/` (a standalone, general-purpose libp2p library — peer identity, group membership, discovery,
permissions — with no dependency back on `foilen-box`).

## Build & test

```bash
./step-compile.sh              # go build ./... + go test ./... (run this after any Go change)
./step-compile-no-tests.sh      # go build only
./step-clean.sh                 # go clean + remove dist/
./step-clean-compile.sh         # clean + compile (with tests)
./step-package.sh               # package desktop binary/archive + Android APK (step-package-desktop.sh + step-package-android.sh)
./create-local-release.sh [ver] # full local release, with tests
./create-local-release-no-tests.sh
```

`go test ./...` must be run from both the root module and `realm/` to cover everything — `go.work` makes
`go build`/`go test` from the root span both when using `./...`, but double-check when in doubt (`cd realm
&& go test ./...`).

Single test: `go test ./internal/webserver/ -run TestServeIndexAndWebSocketRoundTrip -v` (same pattern for
`realm/...` packages).

Run the desktop app during development: `./start-dev-desktop.sh` (also `start-dev-desktop-2.sh` for a second
instance — useful for testing peer-to-peer Realm features locally between two local peers). Directories
`_desktop_1/` and `_desktop_2/` are the local dev instances' persisted config/data dirs.

Android packaging needs `gomobile` (auto-installed by `step-package-android.sh` if missing), Android
SDK/NDK, and JDK 17; `android/` has no committed Gradle wrapper. `install-dev-apk.sh` signs (generating a
local keystore under `_local_keys/` on first run) and installs onto a connected device via `adb`.

## Architecture

### `internal/webserver`

The glue layer, used identically by desktop and Android. `Start(configDir, defaultDhtMode,
hostnameOverride)` boots everything: sets up logging, resolves the config dir, creates the `api` struct
(wires up Realm's `Engine`, Early client, etc.), generates a random session token, binds a random free port
on `127.0.0.1`, and serves the embedded `web/` static files plus a WebSocket API (`ws.go`). The session
token is embedded into `index.html` and required on the WebSocket handshake — this is the only auth
boundary (everything's `127.0.0.1`-only). `api_*.go` files group WebSocket message handlers by feature area
(`api_early.go`, `api_realm.go`, `api_realm_maps.go`, `api_misc.go`).

### `realm/` (module `foilen-realm`)

A standalone libp2p library: peer identity/keys (`keypair/`), group membership and challenge-based group
join (`group_challenge.go`), discovery (`discovery_mdns.go`, `discovery_dht.go`), a known/connected-peers
store (`peers/`), permissions, and connection shaping — everything an application can *do* over the wire is
a pluggable **Feature**.

- **`Engine`** (`engine.go`) owns the libp2p host lifecycle: `Start`/`Reconcile`/`Stop`, diffing config on
  reconcile, and a keep-alive tick (every 10 min) that runs connection shaping and periodic hooks.
- **Connection shaping** (`connection_ring.go`): on each tick, for every configured group the engine sorts
  confirmed members (including itself) alphabetically by peer ID and tries to stay connected to the
  `ringNeighborCount` (2) peers immediately before/after itself, wrapping around. Any other connected group
  peer outside every ring is disconnected unless a `PeerInUseHook` reports it's still in use.
- **`Feature` interface** (`feature.go`): `Name()` (namespaces actions, e.g. `common/scripts`),
  `Actions()` (permission actions this feature's incoming handlers check), `RegisterHandlers(reg
  *Registrar)` (registers libp2p stream handlers). Optional hooks: `PeerConnectedHook`, `PeriodicHook`,
  `PeerRemovedHook`, `PeerInUseHook`, `GroupConfirmedHook`.
- **`Registrar`**: the narrow facade a feature gets instead of touching `Engine` internals —
  `SetStreamHandler`, `Host()`, `PrivKey()`, `Context()`, `Config()`, `IsAllowed(peerID, action)` (deny-by-
  default permission check), `Peers()`.
- **Built-in features** (`realm/features/`): `maps` (realm map/location sharing), `scripts` (remote script
  execution), `services` (proxying — implements `PeerRemovedHook`/`PeerInUseHook` to keep a peer connected
  while actively proxying).
- See `docs/features.md` for the full step-by-step guide to adding a new feature, including the wire-
  protocol conventions used by existing features (worth reading in full before adding one).

### Other `internal/` packages

- `internal/early` — Early.co time-tracking API client, config persistence, aggregation.
- `internal/troubleshooting` — DNS subdomain crawl and WHOIS client/parser.
- `internal/spec` — system information report (OS/CPU/RAM/disk).
- `internal/browseropen` — opens the OS default browser (desktop only).
- `internal/logging` — logging setup.
- `internal/speedtest` — the "box/speedtest" Realm feature (peer-to-peer download/upload throughput test).
  Implements `realm.Feature` like the ones under `realm/features/`, but lives here instead of in the `realm`
  module because it's specific to this app, not something every Realm-based application would want. Wired up
  in `internal/webserver/api.go` alongside the built-in features. This is the pattern to follow for any future
  feature that's app-specific rather than general-purpose: implement `realm.Feature` under `internal/`, not
  `realm/features/`.

### Web UI (`internal/webserver/web/`)

Vanilla JS, one file per feature area under `web/js/` (`realm-peers.js`, `realm-groups.js`, `realm-maps.js`,
`realm-services.js`, `gps.js`, `early.js`, `troubleshooting.js`, `spec.js`, `logs.js`, `android-config.js`,
etc.), talking to the Go backend exclusively over the WebSocket API defined in `internal/webserver/ws.go`
and the `api_*.go` handlers. No build step — edit and reload.

App-specific Realm features (as opposed to the library ones under `realm/features/`) still get their UI as
a subtab of the "Realm" tab, alongside Permissions/Scripts/Services/etc. — e.g. `realm-speedtest.js` (Speed
Test), backed by `internal/speedtest`.

### `cmd/`

- `cmd/foilenbox` — desktop entry point: starts `internal/webserver` and the systray icon.
- `cmd/mobile` — `gomobile bind` entry point wrapping `internal/webserver` for the Android AAR; this is the
  only consumer the Android app (`android/`, Kotlin/Java `MainActivity` + WebView) talks to.
