# Web UI design notes

Design rationale for `internal/webserver/web/`.

## General

- No server-to-client push channel: every view polls through the WebSocket API. Pushed groups and identities
  also show up this way without a reload.
- The WebSocket can die at any time (tab throttling, server restart): in-flight calls are rejected and the
  socket reconnects so callers never hang.
- Every `realm.*` mutation returns the full config; `renderConfig` (in `realm.js`) fans it out to each subtab.
- Tables and selects are patched in place (`syncList`, `syncCells`, `syncOptions`) instead of rebuilt, so
  selections, checkbox/`<details>` state, focus and scroll position survive refreshes. Keys must be stable
  and unique (an id, not an index).
- `<md-outlined-select>` registers new options on an async `slotchange`, so setting `.value` in the same tick
  as appending options does nothing; wait a frame first.
- Hash format: `#tab`, `#tab/subtab` or `#tab/subtab/extra`. `extra` belongs to the subtab (e.g. SMS uses
  `groupId|storeName|phoneNumber`) so a refresh or notification click restores the view.
- Tab and subtab buttons are `<a href="#...">` links (so they can open in a new tab); every other button is a
  Material Web component themed through the `--md-sys-color-*` tokens.
- `#realm-output` shares its panel with tall subtabs, so it has its own capped height instead of flexing
  (it would otherwise be squeezed to a sliver).
- The own peer never appears in the peers list, but its id appears in maps (its own specs, scripts,
  services), so a pseudo-peer built from the config is merged into the list for labels.
- Unknown peer ids are displayed as short ids (e.g. a peer that posted into a map but wasn't seen directly).

## Subtabs

- Specs, Scripts, Services: aggregate the `specs/`, `scripts/` and `services/` entries every peer posts into
  each group's `common` map (see `common/announce`).
- Scripts: a triggered run is polled until its completion arrives, giving up after 2 minutes since completion
  delivery is best-effort.
- Peers: delete is only shown for disconnected peers since a connected one would be re-added right away.
- Listen port mode: saved right away so a background config poll doesn't revert it, except when switching to
  "specific" without a port yet (the backend rejects port 0).
- Camera: local-device config only (not in the Realm config); it only touches Realm to optionally expose
  itself as a service, so that checkbox is disabled while Realm is off. The status poll also recovers the
  device list when the first fetch raced camera readiness. Unchecking "enabled" hides the save button, so it
  saves immediately.
- SMS:
  - Management config is Android-only; the store picker and conversations are on every platform.
  - Only encrypted stores can be managed; an unencrypted `SMS-*` map can still be viewed.
  - "Send from peer" is restricted to peers with an `enabled` marker (the ones able to fulfill a send
    request). It defaults once per opened conversation to the peer that recorded the latest incoming message
    (or latest message).
  - Android has no callback for the permission dialog result, so `hasPermission()` is polled until answered
    or timed out.
  - The store list is re-fetched on each refresh so new or synced stores appear.
- Group troubleshooting: see `design-foilen-box.md`. The expiration entry alone tells whether a session is
  running, finished, or never ran. Diagram colors: gray = no connection and no entry, blue = something
  connects to it but it hasn't published yet, green = it published; solid blue edge = direct, dashed yellow =
  relayed (`/realm-relay`). The last diagram per group is kept when switching groups.

## Vendor JS (`scripts/fetch-vendor-js.mjs`)

- Mirrors esm.sh module graphs locally, rewriting every import to a relative path, so the browser never hits
  esm.sh at runtime. Source maps comments are stripped since they point at esm.sh.
- Every URL is pinned to an exact version: a range would change the fetched content (and the Nix
  `outputHash`) whenever upstream releases.
- The import regex avoids matching `import`/`export` inside strings or identifiers of minified bundles
  (e.g. `ur="@import"` in stylis), and restricts the clause before `from` to identifier-like characters.
- The entry module is saved as `vendor-js/<name>/entry.mjs` so the app imports a stable path.
